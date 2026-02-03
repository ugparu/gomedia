package main

import (
	"errors"
	"io"
	"log"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/ugparu/gomedia/format/mp4"
	"github.com/ugparu/gomedia/format/rtsp"
)

// This example demonstrates how to:
//   - demux an MP4 file using the mp4.Demuxer
//   - publish its codec parameters as an RTSP stream using the RTSP muxer
//   - iterate over packets from the MP4 demuxer and attempt to send them via RTSP
//
// At the time of writing, rtsp.Muxer.WritePacket is not implemented and returns
// ErrRTPMuxerNotImplemented. This example is therefore illustrative: it performs
// the full RTSP publish handshake (OPTIONS → ANNOUNCE → SETUP → RECORD) and
// shows where packet transmission would occur once RTP muxing is implemented.
//
// Usage:
//
//	go run main.go input.mp4 rtsp://localhost:8554/test
//
// If arguments are omitted, the example falls back to:
//
//	input.mp4 and RTSP_URL env var or rtsp://localhost:8554/test by default.
func main() {
	logrus.SetLevel(logrus.DebugLevel)
	inputPath, rtspURL := resolveArgs()

	log.Printf("Using input MP4: %s", inputPath)
	log.Printf("Publishing to RTSP URL: %s", rtspURL)

	// 1. Set up MP4 demuxer and read codec parameters.
	dmx := mp4.NewDemuxer(inputPath)
	params, err := dmx.Demux()
	if err != nil {
		log.Fatalf("failed to demux MP4 %q: %v", inputPath, err)
	}
	defer dmx.Close()

	// 2. Initialize RTSP muxer and perform publish handshake.
	mx := rtsp.NewMuxer(rtspURL)
	if err := mx.Mux(params); err != nil {
		log.Fatalf("failed to initialize RTSP muxer for %q: %v", rtspURL, err)
	}
	defer mx.Close()

	log.Println("RTSP publish session established, starting to read MP4 packets...")

	// 3. Read packets from MP4 and attempt to send them via RTSP.
	var (
		packetsSent int
		lastTS      time.Duration
		haveTS      bool
	)
	for {
		pkt, err := dmx.ReadPacket()
		if err != nil {
			if errors.Is(err, io.EOF) || err == io.EOF {
				log.Printf("reached end of MP4 after sending %d packets", packetsSent)
				break
			}
			log.Fatalf("error reading packet from MP4: %v", err)
		}
		if pkt == nil {
			continue
		}

		// Emulate (approximate) realtime by sleeping based on packet timestamps.
		// MP4 demuxer sets pkt.Timestamp() as a monotonically increasing media time.
		ts := pkt.Timestamp()
		if haveTS {
			if delta := ts - lastTS; delta > 0 {
				time.Sleep(delta)
			}
		} else {
			haveTS = true
		}
		lastTS = ts

		if err := mx.WritePacket(pkt); err != nil {
			// Currently expected: rtsp.ErrRTPMuxerNotImplemented.
			if errors.Is(err, rtsp.ErrRTPMuxerNotImplemented) {
				log.Printf("RTSP write not implemented yet (ErrRTPMuxerNotImplemented), stopping after %d packets", packetsSent)
			} else {
				log.Printf("RTSP write error after %d packets: %v", packetsSent, err)
			}
			break
		}

		packetsSent++
	}

	log.Println("mp4-to-rtsp example finished")
}

// resolveArgs determines the input MP4 path and RTSP URL from:
//  1. command-line arguments: input.mp4 rtsp://...
//  2. environment variable RTSP_URL for the RTSP address
//  3. sensible defaults if none are provided.
func resolveArgs() (inputPath, rtspURL string) {
	// Defaults
	inputPath = "input.mp4"
	rtspURL = os.Getenv("RTSP_URL")

	args := os.Args
	if len(args) > 1 && args[1] != "" {
		inputPath = args[1]
	}
	if len(args) > 2 && args[2] != "" {
		rtspURL = args[2]
	}

	if rtspURL == "" {
		rtspURL = "rtsp://localhost:8554/test"
		log.Printf("RTSP_URL not set and no RTSP URL argument provided, using default %q", rtspURL)
	}

	return inputPath, rtspURL
}
