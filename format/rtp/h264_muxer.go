package rtp

import (
	"fmt"
	"io"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/nal"
	"github.com/ugparu/gomedia/utils/sdp"
)

// h264Muxer performs RTP packetization of H264 video on top of hxxxMuxer.
type h264Muxer struct {
	*hxxxMuxer
	codec *h264.CodecParameters
	sps   []byte
	pps   []byte
}

// NewH264Muxer constructs an RTP muxer for H264 video.
func NewH264Muxer(w io.Writer, media sdp.Media, channel uint8, codec *h264.CodecParameters, mtu int) *h264Muxer {
	base := newBaseMuxer(w, media, channel, 0)
	m := &h264Muxer{
		hxxxMuxer: newHxxxMuxer(base, mtu),
		codec:     codec,
	}

	// Prefer SPS/PPS from SDP if available, otherwise from codec parameters.
	if len(media.SpropParameterSets) >= 2 {
		m.sps = media.SpropParameterSets[0]
		m.pps = media.SpropParameterSets[1]
	} else if codec != nil {
		m.sps = codec.SPS()
		m.pps = codec.PPS()
	}

	return m
}

// WritePacket writes a single H264 packet as one or more RTP packets.
func (m *h264Muxer) WritePacket(pkt gomedia.VideoPacket) error {
	hp, ok := pkt.(*h264.Packet)
	if !ok {
		return fmt.Errorf("rtp: expected *h264.Packet, got %T", pkt)
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

	nals, _ := nal.SplitNALUs(avccData)
	if len(nals) == 0 {
		return nil
	}

	// For every keyframe, prepend SPS/PPS before the access unit so that
	// decoders that join mid-stream can correctly initialize.
	if isKey && len(m.sps) > 0 && len(m.pps) > 0 {
		withParamSets := make([][]byte, 0, len(nals)+2)
		withParamSets = append(withParamSets, m.sps, m.pps)
		withParamSets = append(withParamSets, nals...)
		nals = withParamSets
	}

	for i, nalu := range nals {
		// Decide whether to send this NAL as a single RTP packet or fragment
		// it into FU-A units as per RFC 6184 section 5.8.
		isLastNAL := i == len(nals)-1

		// Small enough to fit into a single RTP packet payload – use Single NAL
		// Unit Packet mode (no fragmentation).
		if len(nalu) <= m.mtu {
			if err := m.writeRTP(nalu, ts, isLastNAL); err != nil {
				return err
			}
			continue
		}

		// Too large for a single RTP packet – use FU-A fragmentation.
		if len(nalu) < 2 {
			// Malformed NAL, skip quietly.
			continue
		}

		origHdr := nalu[0]
		fmt.Printf("%+v\n", nalu[:12])
		nalPayload := nalu[1:]

		// FU Indicator: F and NRI copied from original header, type set to 28 (FU-A).
		fuIndicator := (origHdr & 0xE0) | 28

		// Base FU Header: type from original NAL header (S/E bits set per-fragment).
		baseFuHeaderType := origHdr & 0x1F

		// Maximum fragment payload size excluding FU-A headers (2 bytes).
		maxFragPayload := m.mtu - 2
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

			// Build FU Header for this fragment.
			fuHeader := baseFuHeaderType
			if start {
				fuHeader |= 0x80 // S bit
			}
			if end {
				fuHeader |= 0x40 // E bit
			}

			// Allocate buffer for FU-A payload: 2-byte FU headers + fragment data.
			fuPayload := make([]byte, 2+fragSize)
			fuPayload[0] = fuIndicator
			fuPayload[1] = fuHeader
			copy(fuPayload[2:], nalPayload[offset:offset+fragSize])

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
