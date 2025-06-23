package rtsp

import "time"

// rtspMethod represents the RTSP methods used in the protocol.
type rtspMethod string

// Constants representing RTSP methods.
const (
	video = "video"
	audio = "audio"

	describe     rtspMethod = "DESCRIBE"
	announce     rtspMethod = "ANNOUNCE"
	getParameter rtspMethod = "GET_PARAMETER"
	options      rtspMethod = "OPTIONS"
	pause        rtspMethod = "PAUSE"
	record       rtspMethod = "RECORD"
	teardown     rtspMethod = "TEARDOWN"
	play         rtspMethod = "PLAY"
	setup        rtspMethod = "SETUP"
	setParameter rtspMethod = "SET_PARAMETER"
	redirect     rtspMethod = "REDIRECT"

	dialTimeout      = time.Second * 10
	readWriteTimeout = time.Second * 10

	headerSize = 4

	rtpPacket  = 0x24
	rtspPacket = 0x52

	RTSP     = "rtsp"
	RTSPS    = "rtsps"
	RTSPPort = 554

	pingTimeout       = 15 * time.Second
	minPacketInterval = 30 * time.Second

	tcpBufSize = 8192 * (10 * 10) // nolint:mnd
)
