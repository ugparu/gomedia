// nolint: mnd
package rtsp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/rtp"
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
	buffer           *bytes.Buffer
	packets          []gomedia.Packet
	noVideo, noAudio bool
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
		buffer:       &bytes.Buffer{},
		packets:      []gomedia.Packet{},
		noVideo:      false,
		noAudio:      false,
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

	select {
	case <-dmx.ticker.C:
		if err = dmx.client.ping(); err != nil {
			return
		}
	default:
	}

	var header []byte
	if header, err = dmx.client.Read(headerSize); err != nil {
		return
	}

	switch header[0] {
	case rtspPacket:
		err = dmx.processRTSPPacket()
	case rtpPacket:
		dmx.lastPktRcv = time.Now()

		length := int32(binary.BigEndian.Uint16(header[2:]))
		if length > 65535 || length < 12 {
			return nil, fmt.Errorf("RTSP client incorrect packet size %v", length)
		}

		var content []byte
		if content, err = dmx.client.Read(int(length)); err != nil {
			return nil, err
		}
		content = append(header, content...)

		dmx.buffer.Reset()
		_, _ = dmx.buffer.Write(content)

		var pkt gomedia.Packet
		var targetDmx gomedia.Demuxer

		switch int8(content[1]) {
		case dmx.videoIdx + 1:
			fallthrough
		case dmx.videoIdx:
			targetDmx = dmx.videoDemuxer
		case dmx.audioIdx + 1:
			fallthrough
		case dmx.audioIdx:
			targetDmx = dmx.audioDemuxer
		default:
			logger.Debugf(dmx, "Unknown stream index %d", content[1])
		}

		if targetDmx == nil {
			return
		}
		for {
			if pkt, err = targetDmx.ReadPacket(); err != nil {
				if errors.Is(err, io.EOF) {
					err = nil
					break
				}
				return
			}
			dmx.packets = append(dmx.packets, pkt)
		}
	default:
		err = fmt.Errorf("rtp packet reading desync: first symbol is %s", string(header[0]))
		return
	}

	if len(dmx.packets) > 0 {
		packet = dmx.packets[0]
		dmx.packets = dmx.packets[1:]
	}

	return
}

func (dmx *innerRTSPDemuxer) processRTSPPacket() (err error) {
	var responseTmp []byte

	const maxRTSPHeadersMessageSize = 2 << 10

	for {
		var oneb []byte
		if oneb, err = dmx.client.Read(1); err != nil {
			return err
		}
		responseTmp = append(responseTmp, oneb...)

		if len(responseTmp) > maxRTSPHeadersMessageSize {
			return fmt.Errorf("failed to parse RTSP headers after %d bytes", maxRTSPHeadersMessageSize)
		}

		if len(responseTmp) > headerSize &&
			bytes.Equal(responseTmp[len(responseTmp)-headerSize:], []byte("\r\n\r\n")) {
			logger.Debug(dmx, "consumed rtsp message")

			if !strings.Contains(string(responseTmp), "Content-Length:") {
				break
			}
			var si int
			if si, err = strconv.Atoi(stringInBetween(string(responseTmp), "Content-Length: ", "\r\n")); err != nil {
				return err
			}

			if _, err = dmx.client.Read(si); err != nil {
				return err
			}
		}
	}
	return nil
}

func (dmx *innerRTSPDemuxer) Close() {
	dmx.client.Close()
}

func (dmx *innerRTSPDemuxer) String() string {
	return fmt.Sprintf("RTSP_CLIENT url=%s", dmx.client.pURL.String())
}
