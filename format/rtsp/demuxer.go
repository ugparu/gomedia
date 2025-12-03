// nolint: mnd
package rtsp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/rtp"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/utils/sdp"
)

type innerRTSPDemuxer struct {
	url              string
	videoDemuxer     gomedia.Demuxer
	videoIdx         int8
	audioDemuxer     gomedia.Demuxer
	audioIdx         int8
	chTMP            int
	mediaSDP         []sdp.Media
	lastPktRcv       time.Time
	client           *client
	ticker           *time.Ticker
	buffer           buffer.RefBuffer
	packets          []gomedia.Packet
	noVideo, noAudio bool
	readBuffer       buffer.RefBuffer
}

func New(url string, inpPars ...gomedia.InputParameter) gomedia.Demuxer {
	d := &innerRTSPDemuxer{
		url:          url,
		videoDemuxer: nil,
		videoIdx:     -128,
		audioDemuxer: nil,
		audioIdx:     -128,
		chTMP:        0,
		mediaSDP:     []sdp.Media{},
		lastPktRcv:   time.Now(),
		client:       newClient(),
		ticker:       time.NewTicker(pingTimeout),
		buffer:       buffer.Get(0),
		packets:      []gomedia.Packet{},
		noVideo:      false,
		noAudio:      false,
		readBuffer:   buffer.Get(0),
	}
	for _, inpPar := range inpPars {
		switch inpPar {
		case gomedia.NoVideo:
			d.noVideo = true
		case gomedia.NoAudio:
			d.noAudio = true
		}
	}
	return d
}

func (dmx *innerRTSPDemuxer) Demux() (params gomedia.CodecParametersPair, err error) {
	defer func() {
		params.URL = dmx.url
	}()

	if err = dmx.client.establishConnection(dmx.url); err != nil {
		return
	}

	if dmx.mediaSDP, err = dmx.client.describe(); err != nil {
		return
	}

	if params, err = dmx.findStreams(); err != nil {
		return
	}

	if err = dmx.client.play(); err != nil {
		return
	}

	return
}

// findStreams iterates through the SDP media information to find and set up
// video and audio streams.
//
//nolint:unparam //audio currently unused but presented in api
func (dmx *innerRTSPDemuxer) findStreams() (params gomedia.CodecParametersPair, err error) {
	params.URL = dmx.url

	sort.Slice(dmx.mediaSDP, func(i, j int) bool {
		getPriority := func(avType string) int {
			switch avType {
			case video:
				return 0
			case audio:
				return 1
			default:
				return 2
			}
		}
		return getPriority(dmx.mediaSDP[i].AVType) < getPriority(dmx.mediaSDP[j].AVType)
	})

	for index, i2 := range dmx.mediaSDP {
		if dmx.noVideo && i2.AVType == video || dmx.noAudio && i2.AVType == audio {
			continue
		}

		var idx int
		if idx, err = dmx.client.setup(dmx.chTMP, dmx.controlTrack(i2.Control)); err != nil {
			return
		}

		switch i2.AVType {
		case video:
			dmx.videoIdx = int8(idx) //nolint:gosec
			switch i2.Type {
			case gomedia.H264:
				var h264Pair gomedia.CodecParametersPair
				dmx.videoDemuxer = rtp.NewH264Demuxer(dmx.buffer, i2, uint8(index)) //nolint:gosec
				if h264Pair, err = dmx.videoDemuxer.Demux(); err != nil {
					return
				}
				params.VideoCodecParameters = h264Pair.VideoCodecParameters
			case gomedia.H265:
				var h265Pair gomedia.CodecParametersPair
				dmx.videoDemuxer = rtp.NewH265Demuxer(dmx.buffer, i2, uint8(index)) //nolint:gosec
				if h265Pair, err = dmx.videoDemuxer.Demux(); err != nil {
					return
				}
				params.VideoCodecParameters = h265Pair.VideoCodecParameters
			case gomedia.MJPEG:
				var mjpegPair gomedia.CodecParametersPair
				dmx.videoDemuxer = rtp.NewMJPEGDemuxer(dmx.buffer, i2, uint8(index)) //nolint:gosec
				if mjpegPair, err = dmx.videoDemuxer.Demux(); err != nil {
					return
				}
				params.VideoCodecParameters = mjpegPair.VideoCodecParameters
			default:
				logger.Debugf(dmx, "SDP video codec type %v not supported", i2.Type)
			}
		case audio:
			dmx.audioIdx = int8(idx) //nolint:gosec
			switch i2.Type {
			case gomedia.PCM, gomedia.PCMAlaw, gomedia.PCMUlaw:
				var pcmPair gomedia.CodecParametersPair
				dmx.audioDemuxer = rtp.NewPCMDemuxer(dmx.buffer, i2, uint8(index), i2.Type) //nolint:gosec
				if pcmPair, err = dmx.audioDemuxer.Demux(); err != nil {
					return
				}
				params.AudioCodecParameters = pcmPair.AudioCodecParameters
			case gomedia.AAC:
				var aacPair gomedia.CodecParametersPair
				dmx.audioDemuxer = rtp.NewAACDemuxer(dmx.buffer, i2, uint8(index)) //nolint:gosec
				if aacPair, err = dmx.audioDemuxer.Demux(); err != nil {
					return
				}
				params.AudioCodecParameters = aacPair.AudioCodecParameters
			case gomedia.OPUS:
				var opusPair gomedia.CodecParametersPair
				dmx.audioDemuxer = rtp.NewOPUSDemuxer(dmx.buffer, i2, uint8(index)) //nolint:gosec
				if opusPair, err = dmx.audioDemuxer.Demux(); err != nil {
					return
				}
				params.AudioCodecParameters = opusPair.AudioCodecParameters
			default:
				logger.Debugf(dmx, "SDP audio codec type %v not supported", i2.Type)
			}
		}

		// Increment the temporary channel index
		dmx.chTMP += 2
	}

	return
}

func (dmx *innerRTSPDemuxer) controlTrack(track string) string {
	if strings.Contains(track, "rtsp://") {
		return track
	}
	if !strings.HasSuffix(dmx.client.control, "/") {
		track = "/" + track
	}
	return dmx.client.control + track
}

func (dmx *innerRTSPDemuxer) ReadPacket() (packet gomedia.Packet, err error) {
	defer func() {
		if packet != nil {
			packet.SetURL(dmx.url)
		}
	}()

	if len(dmx.packets) > 0 {
		packet = dmx.packets[0]
		dmx.packets = dmx.packets[1:]
		return
	}

	if time.Since(dmx.lastPktRcv) >= minPacketInterval {
		err = errors.New("packet timeout expired")
		return
	}

	var header [headerSize]byte
	for {
		if time.Since(dmx.lastPktRcv) >= minPacketInterval {
			err = errors.New("packet timeout expired")
			return
		}

		dmx.readBuffer.Resize(1)
		if err = dmx.client.Read(dmx.readBuffer.Data()); err != nil {
			return
		}
		header[0] = dmx.readBuffer.Data()[0]
		if header[0] != rtspPacket && header[0] != rtpPacket {
			logger.Warningf(dmx, "packet reading desync: first symbol is %s. Trying to recover", string(header[0]))
			continue
		}

		dmx.readBuffer.Resize(headerSize - 1)
		if err = dmx.client.Read(dmx.readBuffer.Data()); err != nil {
			return
		}
		copy(header[1:], dmx.readBuffer.Data())
		break
	}

	switch header[0] {
	case rtspPacket:
		err = dmx.processRTSPPacket(header)
	case rtpPacket:
		var targetDmx gomedia.Demuxer

		switch int8(header[1]) {
		case dmx.videoIdx + 1:
			fallthrough
		case dmx.videoIdx:
			targetDmx = dmx.videoDemuxer
		case dmx.audioIdx + 1:
			fallthrough
		case dmx.audioIdx:
			targetDmx = dmx.audioDemuxer
		default:
			logger.Warningf(dmx, "Unknown stream index %d. Possible desync", header[1])
		}

		length := int32(binary.BigEndian.Uint16(header[2:]))
		if length > 65535 || length < 12 {
			logger.Warningf(dmx, "RTSP client incorrect packet size %v. Possible desync", length)
			return
		}

		dmx.readBuffer.Resize(int(length))
		if err = dmx.client.Read(dmx.readBuffer.Data()); err != nil {
			return
		}

		if targetDmx == nil {
			return
		}

		if _, err = dmx.buffer.Write(header[:]); err != nil {
			return
		}
		if _, err = dmx.buffer.Write(dmx.readBuffer.Data()[:length]); err != nil {
			return
		}

		var pkt gomedia.Packet
		for {
			if pkt, err = targetDmx.ReadPacket(); err != nil {
				if errors.Is(err, io.EOF) {
					err = nil
					break
				}
				return
			}
			dmx.lastPktRcv = time.Now()
			dmx.packets = append(dmx.packets, pkt)
		}
	}

	select {
	case <-dmx.ticker.C:
		if err = dmx.client.ping(); err != nil {
			return
		}
	default:
	}

	if len(dmx.packets) > 0 {
		packet = dmx.packets[0]
		dmx.packets = dmx.packets[1:]
	}

	return
}

func (dmx *innerRTSPDemuxer) processRTSPPacket(header [headerSize]byte) (err error) {
	if string(header[:]) != "RTSP" {
		logger.Warningf(dmx, "rtsp packet reading desync: first symbols are %s. Trying to recover", string(header[:]))
		return
	}

	const maxRTSPHeadersMessageSize = 2 << 9
	var dummyBuffer [maxRTSPHeadersMessageSize]byte
	var idx int

	for {
		dmx.readBuffer.Resize(1)
		if err = dmx.client.Read(dmx.readBuffer.Data()); err != nil {
			return err
		}
		dummyBuffer[idx] = dmx.readBuffer.Data()[0]
		idx++

		if idx >= maxRTSPHeadersMessageSize {
			return fmt.Errorf("failed to parse RTSP headers after %d bytes", maxRTSPHeadersMessageSize)
		}

		if idx > headerSize &&
			bytes.Equal(dummyBuffer[idx-headerSize:idx], []byte("\r\n\r\n")) {
			logger.Debug(dmx, "consumed rtsp message")

			if !strings.Contains(string(dummyBuffer[:idx]), "Content-Length:") {
				break
			}
			var si int
			if si, err = strconv.Atoi(stringInBetween(string(dummyBuffer[:idx]), "Content-Length: ", "\r\n")); err != nil {
				return err
			}

			dmx.readBuffer.Resize(si)
			if err = dmx.client.Read(dmx.readBuffer.Data()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (dmx *innerRTSPDemuxer) Close() {
	dmx.client.Close()
	dmx.buffer.Close()
	dmx.readBuffer.Close()
}

func (dmx *innerRTSPDemuxer) String() string {
	return fmt.Sprintf("RTSP_CLIENT url=%s", dmx.client.pURL.String())
}
