package rtsp

import (
	"os"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

var rtspURL = os.Getenv("RTSP_URL")

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
