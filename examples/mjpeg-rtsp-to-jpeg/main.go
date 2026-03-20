// Example: save MJPEG frames from an RTSP stream as JPEG files.
//
// MJPEG packets already contain valid JPEG data — no decoder is needed.
// Each packet's Data() can be written directly to a .jpg file.
//
// Usage:
//
//	RTSP_URL=rtsp://camera:554/stream go run .
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/rtsp"
)

func main() {
	rtspURL := os.Getenv("RTSP_URL")
	if rtspURL == "" {
		log.Fatal("RTSP_URL environment variable is required")
	}

	dmx := rtsp.New(rtspURL)
	par, err := dmx.Demux()
	if err != nil {
		log.Fatal(err)
	}
	defer dmx.Close()

	if par.VideoCodecParameters == nil {
		log.Fatal("no video stream found in RTSP source")
	}

	if par.VideoCodecParameters.Type() != gomedia.MJPEG {
		log.Fatalf("expected MJPEG stream, got %v", par.VideoCodecParameters.Type())
	}

	log.Printf("stream: %s (%dx%d @ %d fps)",
		par.VideoCodecParameters.Tag(),
		par.VideoCodecParameters.Width(),
		par.VideoCodecParameters.Height(),
		par.VideoCodecParameters.FPS(),
	)

	const framesToSave = 10

	saved := 0
	for saved < framesToSave {
		packet, err := dmx.ReadPacket()
		if err != nil {
			log.Fatal(err)
		}
		if packet == nil {
			continue
		}

		vPkt, ok := packet.(gomedia.VideoPacket)
		if !ok {
			packet.Release()
			continue
		}

		// MJPEG packet data is a complete JPEG image — write it directly
		filename := fmt.Sprintf("frame_%03d.jpg", saved)
		if err := os.WriteFile(filename, vPkt.Data(), 0644); err != nil {
			log.Fatal(err)
		}

		log.Printf("saved %s (%d bytes, ts=%v)", filename, vPkt.Len(), vPkt.Timestamp())
		vPkt.Release()
		saved++
	}

	log.Printf("done, saved %d frames", saved)
}
