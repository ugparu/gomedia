package webrtc

import (
	"fmt"
	"net"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/ugparu/gomedia/utils/logger"
)

var api *webrtc.API
var Conf webrtc.Configuration

const (
	// iceSTUNGatherTimeout caps how long ICE waits for a STUN/TURN response
	// per candidate. Pion's default is 5s, so a single lost response under a
	// burst of simultaneous connections stalls gathering for the full 5s.
	// A valid response on LAN arrives in <50ms and on WAN typically <1s, so a
	// lower cap turns those stalls into a short, bounded wait.
	iceSTUNGatherTimeout = time.Second
	// udpReadBufferSize enlarges the ICE UDP mux socket receive buffer so a
	// burst of STUN/TURN responses isn't dropped by the kernel. The value may
	// be clamped by net.core.rmem_max.
	udpReadBufferSize = 8 * 1024 * 1024 // 8 MiB
)

// Init builds the shared pion API and registers every H.264/H.265
// profile-level-id + RTX payload the server is willing to answer with, so
// browsers can negotiate whichever matches.
// When maxPort is 0 the server uses a single ICE UDP mux bound to minPort;
// otherwise it allocates ephemeral UDP ports in the [minPort, maxPort] range.
// hosts lets the server advertise 1:1 NAT public addresses when running
// behind a mapped UDP port.
func Init(minPort, maxPort uint16, hosts []string, iceServers []webrtc.ICEServer) (err error) {
	Conf = webrtc.Configuration{
		ICEServers:           iceServers,
		ICETransportPolicy:   webrtc.ICETransportPolicyAll,
		BundlePolicy:         webrtc.BundlePolicyMaxBundle,
		RTCPMuxPolicy:        webrtc.RTCPMuxPolicyRequire,
		PeerIdentity:         "",
		Certificates:         []webrtc.Certificate{},
		ICECandidatePoolSize: 0,
		SDPSemantics:         webrtc.SDPSemanticsUnifiedPlanWithFallback,
	}

	m := new(webrtc.MediaEngine)

	for _, codec := range []webrtc.RTPCodecParameters{
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeOpus,
				ClockRate:    48000,
				Channels:     2,
				SDPFmtpLine:  "minptime=10;useinbandfec=1;stereo=1;sprop-stereo=1",
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
			return err
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
		// {
		// 	RTPCodecCapability: webrtc.RTPCodecCapability{
		// 		MimeType:     webrtc.MimeTypeH265,
		// 		ClockRate:    90000,
		// 		RTCPFeedback: videoRTCPFeedback,
		// 	},
		// 	PayloadType: 116,
		// },
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
				SDPFmtpLine:  "level-id=186;profile-id=1;tier-flag=0;tx-mode=SRST",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 49, //nolint:mnd
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeRTX,
				ClockRate:    90000,
				Channels:     0,
				SDPFmtpLine:  "apt=49",
				RTCPFeedback: nil,
			},
			PayloadType: 50,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH265,
				ClockRate:    90000, //nolint:mnd // 90k
				Channels:     0,
				SDPFmtpLine:  "level-id=186;profile-id=2;tier-flag=0;tx-mode=SRST",
				RTCPFeedback: videoRTCPFeedback,
			},
			PayloadType: 51, //nolint:mnd
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
			PayloadType: 124, //nolint:mnd
		},
	} {
		if err := m.RegisterCodec(codec, webrtc.RTPCodecTypeVideo); err != nil {
			return err
		}
	}

	i := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, i); err != nil {
		return err
	}

	var s webrtc.SettingEngine
	s.SetSTUNGatherTimeout(iceSTUNGatherTimeout)
	if maxPort == 0 {
		udpListener, err := net.ListenPacket("udp", fmt.Sprintf(":%d", minPort))
		if err != nil {
			return err
		}
		if udpConn, ok := udpListener.(*net.UDPConn); ok {
			if bufErr := udpConn.SetReadBuffer(udpReadBufferSize); bufErr != nil {
				logger.Default.Infof("WEBRTC", "Failed to set UDP read buffer to %d: %v", udpReadBufferSize, bufErr)
			}
		}
		s.SetICEUDPMux(webrtc.NewICEUDPMux(nil, udpListener))
	} else if err := s.SetEphemeralUDPPortRange(minPort, maxPort); err != nil {
		return err
	}

	if len(hosts) > 0 {
		logger.Default.Infof("WEBRTC", "Setting host candidates to %v", hosts)
		if err := s.SetICEAddressRewriteRules(webrtc.ICEAddressRewriteRule{
			External:        hosts,
			AsCandidateType: webrtc.ICECandidateTypeHost,
			Mode:            webrtc.ICEAddressRewriteReplace,
		}); err != nil {
			return err
		}
	}

	api = webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(i), webrtc.WithSettingEngine(s))

	return nil
}
