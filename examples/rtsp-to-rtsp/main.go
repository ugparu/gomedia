package main

import (
	"os"

	"github.com/sirupsen/logrus"
	"github.com/ugparu/gomedia"
	examplelogger "github.com/ugparu/gomedia/examples/logger"
	"github.com/ugparu/gomedia/format/rtsp"
	"github.com/ugparu/gomedia/reader"
	"github.com/ugparu/gomedia/utils/logger"
	writerRtsp "github.com/ugparu/gomedia/writer/rtsp"
)

var log logger.Logger

// This example demonstrates how to:
//   - read packets from an RTSP source using the high-level gomedia.Reader
//   - publish packets to another RTSP server using the gomedia.Writer implementation for RTSP
//   - forward all packets from the source RTSP to the destination RTSP
//
// Usage:
//
//	go run main.go rtsp://source.example/test rtsp://dest.example/test
//
// If arguments are omitted, the example falls back to:
//
//	SRC_RTSP_URL and DST_RTSP_URL env vars or
//	    rtsp://localhost:8554/src and rtsp://localhost:8554/dst by default.
func main() {
	log = examplelogger.New(logrus.InfoLevel)

	srcURL, dstURL := resolveArgs()

	log.Infof(log, "Using source RTSP URL: %s", srcURL)
	log.Infof(log, "Publishing to destination RTSP URL: %s", dstURL)

	// 1. Create RTSP reader and writer.
	const chanSize = 1024

	rdr := reader.NewRTSP(chanSize, reader.WithLogger(examplelogger.New(logrus.InfoLevel)), reader.WithRTSPParams(rtsp.WithLogger(examplelogger.New(logrus.InfoLevel))))
	wr := writerRtsp.New(srcURL, dstURL, chanSize)

	// Start reader and writer.
	rdr.Read()
	wr.Write()

	defer rdr.Close()
	defer wr.Close()

	// Configure source URL for reader and writer.
	rdr.AddURL() <- srcURL
	wr.AddSource() <- srcURL

	log.Infof(log, "RTSP relay established, starting to forward packets via reader/writer...")

	// 2. Forward all packets from reader to writer until the writer is done.
	packetsCh := rdr.Packets()
	writerPacketsCh := wr.Packets()

	for {
		select {
		case pkt := <-packetsCh:
			if pkt == nil {
				continue
			}
			videoPkt, ok := pkt.(gomedia.VideoPacket)
			if !ok {
				continue
			}
			// Forward packet; the RTSP writer will internally ignore
			// non-video packets if they are not supported.
			writerPacketsCh <- videoPkt
		case <-wr.Done():
			log.Infof(log, "RTSP writer signaled completion, stopping relay")
			log.Infof(log, "rtsp-to-rtsp example finished")
			return
		}
	}
}

// resolveArgs determines source and destination RTSP URLs from:
//  1. command-line arguments: srcRTSP dstRTSP
//  2. environment variables: SRC_RTSP_URL and DST_RTSP_URL
//  3. sensible defaults if none are provided.
func resolveArgs() (srcURL, dstURL string) {
	// Defaults
	srcURL = os.Getenv("SRC_RTSP_URL")
	dstURL = os.Getenv("DST_RTSP_URL")

	args := os.Args
	if len(args) > 1 && args[1] != "" {
		srcURL = args[1]
	}
	if len(args) > 2 && args[2] != "" {
		dstURL = args[2]
	}

	if srcURL == "" {
		srcURL = "rtsp://localhost:8554/src"
		log.Infof(log, "SRC_RTSP_URL not set and no source RTSP URL argument provided, using default %q", srcURL)
	}
	if dstURL == "" {
		dstURL = "rtsp://localhost:8554/dst"
		log.Infof(log, "DST_RTSP_URL not set and no destination RTSP URL argument provided, using default %q", dstURL)
	}

	return srcURL, dstURL
}
