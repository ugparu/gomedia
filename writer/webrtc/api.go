package webrtc

import (
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
	"github.com/ugparu/gomedia/utils/logger"
)

var api *webrtc.API
var conf webrtc.Configuration

// Init initializes the WebRTC configuration and API.
func Init(minPort, maxPort uint16, hosts []string, iceServers []webrtc.ICEServer) {
	// Configure WebRTC settings
	conf = webrtc.Configuration{
		ICEServers:           iceServers,
		ICETransportPolicy:   0,
		BundlePolicy:         0,
		RTCPMuxPolicy:        0,
		PeerIdentity:         "",
		Certificates:         []webrtc.Certificate{},
		ICECandidatePoolSize: 0,
		SDPSemantics:         webrtc.SDPSemanticsUnifiedPlanWithFallback,
	}

	// Create a new MediaEngine
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		logger.Fatal("WEBRTC", err.Error())
	}

	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH265,
			ClockRate:    90000, //nolint:mnd // 90k
			Channels:     0,
			SDPFmtpLine:  "level-id=93;profile-id=1;tier-flag=0;tx-mode=SRST",
			RTCPFeedback: nil,
		},
		PayloadType: 116, //nolint:mnd
	}, webrtc.RTPCodecTypeVideo); err != nil {
		logger.Fatal("WEBRTC", err.Error())
	}

	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH265,
			ClockRate:    90000, //nolint:mnd // 90k
			Channels:     0,
			SDPFmtpLine:  "level-id=93;profile-id=2;tier-flag=0;tx-mode=SRST",
			RTCPFeedback: nil,
		},
		PayloadType: 118, //nolint:mnd
	}, webrtc.RTPCodecTypeVideo); err != nil {
		logger.Fatal("WEBRTC", err.Error())
	}

	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH265,
			ClockRate:    90000, //nolint:mnd // 90k
			Channels:     0,
			SDPFmtpLine:  "level-id=180;profile-id=1;tier-flag=0;tx-mode=SRST",
			RTCPFeedback: nil,
		},
		PayloadType: 123, //nolint:mnd
	}, webrtc.RTPCodecTypeVideo); err != nil {
		logger.Fatal("WEBRTC", err.Error())
	}

	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH265,
			ClockRate:    90000, //nolint:mnd // 90k
			Channels:     0,
			SDPFmtpLine:  "level-id=180;profile-id=2;tier-flag=0;tx-mode=SRST",
			RTCPFeedback: nil,
		},
		PayloadType: 125, //nolint:mnd
	}, webrtc.RTPCodecTypeVideo); err != nil {
		logger.Fatal("WEBRTC", err.Error())
	}

	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     "video/h265",
			ClockRate:    90000, //nolint:mnd // 90k
			Channels:     0,
			SDPFmtpLine:  "level-id=180;profile-id=1;tier-flag=0;tx-mode=SRST",
			RTCPFeedback: nil,
		},
		PayloadType: 45, //nolint:mnd
	}, webrtc.RTPCodecTypeVideo); err != nil {
		logger.Fatal("WEBRTC", err.Error())
	}

	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     "video/h265",
			ClockRate:    90000, //nolint:mnd // 90k
			Channels:     0,
			SDPFmtpLine:  "level-id=180;profile-id=2;tier-flag=0;tx-mode=SRST",
			RTCPFeedback: nil,
		},
		PayloadType: 47, //nolint:mnd
	}, webrtc.RTPCodecTypeVideo); err != nil {
		logger.Fatal("WEBRTC", err.Error())
	}

	// Create a new interceptor.Registry
	i := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, i); err != nil {
		logger.Fatal("WEBRTC", err.Error())
	}

	// Create a new SettingEngine
	s := webrtc.SettingEngine{} //nolint: exhaustruct
	if minPort > 0 && maxPort > 0 && maxPort > minPort {
		logger.Infof("WEBRTC", "Setting port range from %d to %d", minPort, maxPort)
		if err := s.SetEphemeralUDPPortRange(minPort, maxPort); err != nil {
			logger.Fatal("WEBRTC", err.Error())
		}
	}

	// Set host candidates if provided
	if len(hosts) > 0 {
		logger.Infof("WEBRTC", "Setting host candidates to %v", hosts)
		s.SetNAT1To1IPs(hosts, webrtc.ICECandidateTypeHost)
	}

	// Create a new WebRTC API with the configured settings
	api = webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(i), webrtc.WithSettingEngine(s))
}
