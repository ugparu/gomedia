package main

import (
	"log"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/ugparu/gomedia/reader"
	writerRtsp "github.com/ugparu/gomedia/writer/rtsp"
)

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
	logrus.SetLevel(logrus.DebugLevel)

	srcURL, dstURL := resolveArgs()

	log.Printf("Using source RTSP URL: %s", srcURL)
	log.Printf("Publishing to destination RTSP URL: %s", dstURL)

	// 1. Create RTSP reader and writer.
	const chanSize = 1024

	rdr := reader.NewRTSP(chanSize)
	wr := writerRtsp.New(srcURL, dstURL, chanSize)

	// Start reader and writer.
	rdr.Read()
	wr.Write()

	defer rdr.Close()
	defer wr.Close()

	// Configure source URL for reader and writer.
	rdr.AddURL() <- srcURL
	wr.AddSource() <- srcURL

	log.Println("RTSP relay established, starting to forward packets via reader/writer...")

	// 2. Forward all packets from reader to writer until the writer is done.
	packetsCh := rdr.Packets()
	writerPacketsCh := wr.Packets()

	for {
		select {
		case pkt := <-packetsCh:
			if pkt == nil {
				continue
			}
			// Forward packet; the RTSP writer will internally ignore
			// non-video packets if they are not supported.
			writerPacketsCh <- pkt
		case <-wr.Done():
			log.Println("RTSP writer signaled completion, stopping relay")
			log.Println("rtsp-to-rtsp example finished")
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
		log.Printf("SRC_RTSP_URL not set and no source RTSP URL argument provided, using default %q", srcURL)
	}
	if dstURL == "" {
		dstURL = "rtsp://localhost:8554/dst"
		log.Printf("DST_RTSP_URL not set and no destination RTSP URL argument provided, using default %q", dstURL)
	}

	return srcURL, dstURL
}
