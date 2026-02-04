package rtsp

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/codec/h265"
	"github.com/ugparu/gomedia/codec/mjpeg"
	"github.com/ugparu/gomedia/codec/opus"
	"github.com/ugparu/gomedia/codec/pcm"
	"github.com/ugparu/gomedia/format/rtp"
	"github.com/ugparu/gomedia/utils"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/utils/sdp"
)

// ErrRTPMuxerNotImplemented indicates that the RTP muxer is not yet implemented.
var ErrRTPMuxerNotImplemented = utils.UnimplementedError{}

// Muxer represents an RTSP muxer for publishing streams.
type Muxer struct {
	url        string
	client     *client
	medias     []sdp.Media
	videoMuxer rtpVideoMuxer
}

// rtpVideoMuxer is the subset of methods required from an RTP video muxer.
type rtpVideoMuxer interface {
	WritePacket(gomedia.VideoPacket) error
}

// NewMuxer creates a new RTSP muxer for the given URL.
func NewMuxer(url string) gomedia.Muxer {
	return &Muxer{url: url, client: newClient(), medias: nil}
}

// Mux initializes the muxer with stream parameters and performs the publish workflow:
// OPTIONS (via establishConnection) -> ANNOUNCE -> SETUP (per track) -> RECORD.
func (m *Muxer) Mux(streams gomedia.CodecParametersPair) (err error) {
	logger.Debugf(m, "Muxing streams: %+v", streams)

	if err = m.client.establishConnection(m.url); err != nil {
		return err
	}

	if !m.client.supportsPublish() {
		return errors.New("rtsp: server does not support ANNOUNCE and RECORD (publishing)")
	}

	logger.Debugf(m, "Converting codec parameters to SDP medias")
	m.medias, err = codecParamsToSDPMedias(streams)
	if err != nil {
		return err
	}

	logger.Debugf(m, "SDP medias: %+v", m.medias)

	if len(m.medias) == 0 {
		return errors.New("rtsp: no video or audio streams to publish")
	}

	logger.Debugf(m, "Creating SDP session")
	sess := sdp.Session{URI: m.client.control}
	logger.Debugf(m, "Announcing streams")
	if err = m.client.announce(sess, m.medias); err != nil {
		return err
	}

	chTMP := 0
	for _, media := range m.medias {
		uri := controlTrack(m.client.control, media.Control)
		var ch int
		if ch, err = m.client.setup(chTMP, uri, "record"); err != nil {
			return err
		}

		// Only video is supported for now.
		if media.AVType == video && streams.VideoCodecParameters != nil {
			switch v := streams.VideoCodecParameters.(type) {
			case *h264.CodecParameters:
				logger.Debugf(m, "Creating H264 RTP muxer on channel %d", ch)
				m.videoMuxer = rtp.NewH264Muxer(m.client.conn, media, uint8(ch), v, 0) //nolint:gosec
			case *h265.CodecParameters:
				logger.Debugf(m, "Creating H265 RTP muxer on channel %d", ch)
				m.videoMuxer = rtp.NewH265Muxer(m.client.conn, media, uint8(ch), v, 0) //nolint:gosec
			default:
				logger.Debugf(m, "RTP muxer for codec %T not implemented yet", streams.VideoCodecParameters)
			}
		}

		chTMP += 2
	}

	logger.Debugf(m, "Recording streams")
	if err = m.client.record(); err != nil {
		return err
	}

	return nil
}

// WritePacket writes a packet using the underlying RTP muxer.
func (m *Muxer) WritePacket(pkt gomedia.Packet) error {
	if m.videoMuxer == nil {
		return fmt.Errorf("%w: RTP muxer not initialized", ErrRTPMuxerNotImplemented)
	}

	vp, ok := pkt.(gomedia.VideoPacket)
	if !ok {
		return fmt.Errorf("rtsp: only video packets are supported for now, got %T", pkt)
	}

	if err := m.client.conn.SetWriteDeadline(time.Now().Add(readWriteTimeout)); err != nil {
		return err
	}

	return m.videoMuxer.WritePacket(vp)
}

// Close closes the RTSP connection and sends TEARDOWN.
func (m *Muxer) Close() {
	m.client.Close()
}

// codecParamsToSDPMedias converts CodecParametersPair to SDP media descriptions.
func codecParamsToSDPMedias(streams gomedia.CodecParametersPair) ([]sdp.Media, error) {
	var medias []sdp.Media
	trackID := 0

	if streams.VideoCodecParameters != nil {
		m, err := videoCodecToSDPMedia(streams.VideoCodecParameters, trackID)
		if err != nil {
			return nil, err
		}
		medias = append(medias, m)
		trackID++
	}

	if streams.AudioCodecParameters != nil {
		m, err := audioCodecToSDPMedia(streams.AudioCodecParameters, trackID)
		if err != nil {
			return nil, err
		}
		medias = append(medias, m)
		trackID++
	}

	sort.SliceStable(medias, func(i, j int) bool {
		pri := func(av string) int {
			switch av {
			case video:
				return 0
			case audio:
				return 1
			default:
				return 2
			}
		}
		return pri(medias[i].AVType) < pri(medias[j].AVType)
	})

	// Reassign trackID after sort so video=0, audio=1
	for i := range medias {
		medias[i].Control = fmt.Sprintf("trackID=%d", i)
	}

	return medias, nil
}

func videoCodecToSDPMedia(params gomedia.VideoCodecParameters, trackID int) (sdp.Media, error) {
	m := sdp.Media{
		AVType:      video,
		Control:     fmt.Sprintf("trackID=%d", trackID),
		PayloadType: 96,
		TimeScale:   90000,
		Width:       int(params.Width()),
		Height:      int(params.Height()),
		FPS:         int(params.FPS()),
	}

	switch p := params.(type) {
	case *h264.CodecParameters:
		m.Type = gomedia.H264
		sps := p.SPS()
		pps := p.PPS()
		if len(sps) == 0 || len(pps) == 0 {
			return m, errors.New("rtsp: H264 codec parameters must have SPS and PPS")
		}
		m.SpropParameterSets = [][]byte{sps, pps}
	case *h265.CodecParameters:
		m.Type = gomedia.H265
		m.PayloadType = 98
		vps := p.VPS()
		sps := p.SPS()
		pps := p.PPS()
		if len(vps) == 0 || len(sps) == 0 || len(pps) == 0 {
			return m, errors.New("rtsp: H265 codec parameters must have VPS, SPS and PPS")
		}
		m.SpropVPS = vps
		m.SpropSPS = sps
		m.SpropPPS = pps
	case *mjpeg.CodecParameters:
		m.Type = gomedia.MJPEG
		if m.Width == 0 {
			m.Width = int(p.Width())
		}
		if m.Height == 0 {
			m.Height = int(p.Height())
		}
		if m.FPS == 0 {
			m.FPS = int(p.FPS())
		}
	default:
		return m, fmt.Errorf("rtsp: unsupported video codec type: %T", params)
	}

	return m, nil
}

func audioCodecToSDPMedia(params gomedia.AudioCodecParameters, trackID int) (sdp.Media, error) {
	m := sdp.Media{
		AVType:       audio,
		Control:      fmt.Sprintf("trackID=%d", trackID),
		PayloadType:  96,
		TimeScale:    8000,
		ChannelCount: int(params.Channels()),
	}

	switch p := params.(type) {
	case *aac.CodecParameters:
		m.Type = gomedia.AAC
		m.TimeScale = int(p.SampleRate())
		m.ChannelCount = int(p.Channels())
		m.Config = p.MPEG4AudioConfigBytes()
		if len(m.Config) == 0 {
			return m, errors.New("rtsp: AAC codec parameters must have config")
		}
		m.SizeLength = 13
		m.IndexLength = 3
	case *opus.CodecParameters:
		m.Type = gomedia.OPUS
		m.TimeScale = 48000
		m.ChannelCount = int(p.Channels())
	case *pcm.CodecParameters:
		m.Type = p.CodecType
		m.TimeScale = int(p.SampleRate())
		m.ChannelCount = int(p.Channels())
		switch p.CodecType {
		case gomedia.PCMUlaw:
			m.PayloadType = 0
		case gomedia.PCMAlaw:
			m.PayloadType = 8
		default:
			m.PayloadType = 96
		}
	default:
		return m, fmt.Errorf("rtsp: unsupported audio codec type: %T", params)
	}

	return m, nil
}
