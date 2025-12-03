package rtsp

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

var rtspURL = "rtsp://83.234.151.179/Streaming/Channels/1"

func BenchmarkReader(b *testing.B) {
	logrus.SetLevel(logrus.DebugLevel)

	dmx := New(rtspURL)
	par, err := dmx.Demux()
	require.NoError(b, err)
	require.NotNil(b, par)

	for b.Loop() {
		pkt, err := dmx.ReadPacket()
		require.NoError(b, err)
		if pkt == nil {
			continue
		}
	}

	dmx.Close()
}
