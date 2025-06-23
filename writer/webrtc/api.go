package webrtc

import (
	"github.com/pion/interceptor"
	"github.com/pion/rtp"
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

	m := new(webrtc.MediaEngine)

	// Default Pion Audio Codecs
	for _, codec := range []webrtc.RTPCodecParameters{
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeOpus,
				ClockRate:    48000,
				Channels:     2,
				SDPFmtpLine:  "minptime=10;useinbandfec=1",
				RTCPFeedback: nil,
			},
			PayloadType: 111,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeG722,
				ClockRate:    8000,
				Channels:     0,
				SDPFmtpLine:  "",
				RTCPFeedback: nil,
			},
			PayloadType: rtp.PayloadTypeG722,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypePCMU,
				ClockRate:    8000,
				Channels:     0,
				SDPFmtpLine:  "",
				RTCPFeedback: nil,
			},
			PayloadType: rtp.PayloadTypePCMU,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypePCMA,
				ClockRate:    8000,
				Channels:     0,
				SDPFmtpLine:  "",
				RTCPFeedback: nil,
			},
			PayloadType: rtp.PayloadTypePCMA,
		},
	} {
		if err := m.RegisterCodec(codec, webrtc.RTPCodecTypeAudio); err != nil {
			logger.Fatal("WEBRTC", err.Error())
		}
	}

	videoRTCPFeedback := []webrtc.RTCPFeedback{{Type: "goog-remb", Parameter: ""}, {Type: "ccm", Parameter: "fir"}, {Type: "nack", Parameter: ""}, {Type: "nack", Parameter: "pli"}}
	for _, codec := range []webrtc.RTPCodecParameters{
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeVP8,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 96,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeRTX,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "apt=96",
				RTCPFeedback: nil,
			},
			PayloadType: 97,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH264,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 102,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeRTX,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "apt=102",
				RTCPFeedback: nil,
			},
			PayloadType: 103,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH264,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42001f",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 104,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeRTX,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "apt=104",
				RTCPFeedback: nil,
			},
			PayloadType: 105,
		},

		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH264,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 106,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeRTX,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "apt=106",
				RTCPFeedback: nil,
			},
			PayloadType: 107,
		},

		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH264,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42e01f",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 108,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeRTX,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "apt=108",
				RTCPFeedback: nil,
			},
			PayloadType: 109,
		},

		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH264,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=4d001f",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 127,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeRTX,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "apt=127",
				RTCPFeedback: nil,
			},
			PayloadType: 125,
		},

		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH264,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=4d001f",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 39,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeRTX,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "apt=39",
				RTCPFeedback: nil,
			},
			PayloadType: 40,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH265,
				ClockRate:    90000,
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 116,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeRTX,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "apt=116",
				RTCPFeedback: nil,
			},
			PayloadType: 117,
		},
		// {
		// 	RTPCodecCapability: webrtc.RTPCodecCapability{
		// 		MimeType:     webrtc.MimeTypeAV1,
		// 		ClockRate:    90000,
		// 		Channels:     0,
		// 		SDPFmtpLine:  "",
		// 		RTCPFeedback: videoRTCPFeedback,
		// 	},
		// 	PayloadType: 45,
		// },
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     "video/h265",
				ClockRate:    90000, //nolint:mnd // 90k
				Channels:     0,
				SDPFmtpLine:  "level-id=180;profile-id=1;tier-flag=0;tx-mode=SRST",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 45, //nolint:mnd
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     "video/h265",
				ClockRate:    90000, //nolint:mnd // 90k
				Channels:     0,
				SDPFmtpLine:  "level-id=180;profile-id=2;tier-flag=0;tx-mode=SRST",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 47, //nolint:mnd
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeRTX,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "apt=45",
				RTCPFeedback: nil,
			},
			PayloadType: 46,
		},

		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeVP9,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "profile-id=0",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 98,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeRTX,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "apt=98",
				RTCPFeedback: nil,
			},
			PayloadType: 99,
		},

		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeVP9,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "profile-id=2",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 100,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeRTX,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "apt=100",
				RTCPFeedback: nil,
			},
			PayloadType: 101,
		},

		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH264,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=64001f",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 112,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeRTX,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "apt=112",
				RTCPFeedback: nil,
			},
			PayloadType: 113,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH265,
				ClockRate:    90000, //nolint:mnd // 90k
				Channels:     0,
				SDPFmtpLine:  "level-id=93;profile-id=1;tier-flag=0;tx-mode=SRST",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 116,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH265,
				ClockRate:    90000, //nolint:mnd // 90k
				Channels:     0,
				SDPFmtpLine:  "level-id=93;profile-id=2;tier-flag=0;tx-mode=SRST",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 118, //nolint:mnd
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH265,
				ClockRate:    90000, //nolint:mnd // 90k
				Channels:     0,
				SDPFmtpLine:  "level-id=180;profile-id=1;tier-flag=0;tx-mode=SRST",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 123, //nolint:mnd
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH265,
				ClockRate:    90000, //nolint:mnd // 90k
				Channels:     0,
				SDPFmtpLine:  "level-id=180;profile-id=2;tier-flag=0;tx-mode=SRST",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 125, //nolint:mnd
		},
	} {
		if err := m.RegisterCodec(codec, webrtc.RTPCodecTypeVideo); err != nil {
			logger.Fatal("WEBRTC", err.Error())
		}
	}

	if err := m.RegisterCodec(webrtc.RTPCodecParameters{}, webrtc.RTPCodecTypeVideo); err != nil {
		logger.Fatal("WEBRTC", err.Error())
	}

	if err := m.RegisterCodec(webrtc.RTPCodecParameters{}, webrtc.RTPCodecTypeVideo); err != nil {
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
