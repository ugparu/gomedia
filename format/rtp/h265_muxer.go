package rtp

import (
	"fmt"
	"io"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/h265"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/nal"
	"github.com/ugparu/gomedia/utils/sdp"
)

// h265Muxer performs RTP packetization of H265 video on top of hxxxMuxer.
type h265Muxer struct {
	*hxxxMuxer
	codec *h265.CodecParameters
	vps   []byte
	sps   []byte
	pps   []byte
}

// NewH265Muxer constructs an RTP muxer for H265 video.
func NewH265Muxer(w io.Writer, media sdp.Media, channel uint8, codec *h265.CodecParameters, mtu int) *h265Muxer {
	base := newBaseMuxer(w, media, channel, 0)
	m := &h265Muxer{
		hxxxMuxer: newHxxxMuxer(base, mtu),
		codec:     codec,
	}

	// Prefer VPS/SPS/PPS from SDP if available, otherwise from codec parameters.
	if len(media.SpropVPS) > 0 && len(media.SpropSPS) > 0 && len(media.SpropPPS) > 0 {
		m.vps = media.SpropVPS
		m.sps = media.SpropSPS
		m.pps = media.SpropPPS
	} else if codec != nil {
		m.vps = codec.VPS()
		m.sps = codec.SPS()
		m.pps = codec.PPS()
	}

	return m
}

// WritePacket writes a single H265 packet as one or more RTP packets.
// nolint: mnd
func (m *h265Muxer) WritePacket(pkt gomedia.VideoPacket) error {
	hp, ok := pkt.(*h265.Packet)
	if !ok {
		return fmt.Errorf("rtp: expected *h265.Packet, got %T", pkt)
	}
	ts := hp.Timestamp()
	isKey := hp.IsKeyFrame()

	var avccData []byte
	hp.View(func(b buffer.PooledBuffer) {
		avccData = append(avccData[:0], b.Data()...)
	})

	if len(avccData) == 0 {
		return nil
	}

	nalus, _ := nal.SplitNALUs(avccData)
	if len(nalus) == 0 {
		return nil
	}

	// For every keyframe, prepend VPS/SPS/PPS before the access unit so that
	// decoders that join mid-stream can correctly initialize.
	if isKey && len(m.vps) > 0 && len(m.sps) > 0 && len(m.pps) > 0 {
		withParamSets := make([][]byte, 0, len(nalus)+3)
		withParamSets = append(withParamSets, m.vps, m.sps, m.pps)
		withParamSets = append(withParamSets, nalus...)
		nalus = withParamSets
	}

	for i, nalu := range nalus {
		isLastNAL := i == len(nalus)-1

		// Small enough to fit into a single RTP packet payload – send as is.
		if len(nalu) <= m.mtu {
			if err := m.writeRTP(nalu, ts, isLastNAL); err != nil {
				return err
			}
			continue
		}

		// Too large for a single RTP packet – use FU fragmentation (RFC 7798).
		if len(nalu) < 3 {
			// Malformed NAL, skip quietly.
			continue
		}

		// Original NAL header is 2 bytes in H265.
		origHdr0 := nalu[0]
		origHdr1 := nalu[1]
		nalPayload := nalu[2:]

		// Extract original nal_unit_type (6 bits).
		origType := (origHdr0 >> 1) & 0x3f

		// FU indicator: same F and reserved bits, nal_unit_type set to NalFU (49).
		fuIndicator0 := (origHdr0 & 0x81) | (h265.NalFU << 1)
		fuIndicator1 := origHdr1

		// Maximum fragment payload size excluding FU headers (3 bytes).
		maxFragPayload := m.mtu - 3
		if maxFragPayload <= 0 {
			// Pathological MTU, fall back to sending as-is to avoid infinite loop.
			if err := m.writeRTP(nalu, ts, isLastNAL); err != nil {
				return err
			}
			continue
		}

		for offset := 0; offset < len(nalPayload); {
			remaining := len(nalPayload) - offset
			fragSize := maxFragPayload
			if remaining < fragSize {
				fragSize = remaining
			}

			start := offset == 0
			end := remaining <= maxFragPayload

			// Build FU header for this fragment.
			fuHeader := origType
			if start {
				fuHeader |= 0x80 // S bit
			}
			if end {
				fuHeader |= 0x40 // E bit
			}

			// Allocate buffer for FU payload: 2-byte FU indicator + 1-byte FU header + fragment data.
			fuPayload := make([]byte, 3+fragSize)
			fuPayload[0] = fuIndicator0
			fuPayload[1] = fuIndicator1
			fuPayload[2] = fuHeader
			copy(fuPayload[3:], nalPayload[offset:offset+fragSize])

			// Marker bit must be set only on the last RTP packet of the last
			// NAL unit of the access unit. For fragmented NALs this is the
			// fragment with E=1 of the last NAL.
			marker := isLastNAL && end
			if err := m.writeRTP(fuPayload, ts, marker); err != nil {
				return err
			}

			offset += fragSize
		}
	}

	return nil
}
