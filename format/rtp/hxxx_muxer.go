package rtp

import "github.com/ugparu/gomedia/utils/buffer"

// DefaultMTU is a reasonable default for RTP over TCP. Since RTSP interleaved
// framing is used, we are not strictly constrained by UDP MTU, but keeping
// packets small helps interoperability.
const DefaultMTU = 1200

// hxxxMuxer provides generic NAL-unit based packetization on top of baseMuxer.
// It is codec-agnostic and expects the caller to provide NAL classification.
type hxxxMuxer struct {
	*baseMuxer
	fuBuf buffer.PooledBuffer
	mtu   int
}

// newHxxxMuxer constructs a new hxxxMuxer.
func newHxxxMuxer(b *baseMuxer, mtu int) *hxxxMuxer {
	if mtu <= 0 {
		mtu = DefaultMTU
	}
	return &hxxxMuxer{
		baseMuxer: b,
		fuBuf:     buffer.Get(DefaultMTU),
		mtu:       mtu,
	}
}
