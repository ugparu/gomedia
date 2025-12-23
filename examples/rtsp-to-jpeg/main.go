package main

import (
	"image/jpeg"
	"log"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/decoder/video/cpu"
	"github.com/ugparu/gomedia/format/rtsp"
)

func main() {
	logrus.SetLevel(logrus.DebugLevel)

	rtspURL := os.Getenv("RTSP_URL")
	if rtspURL == "" {
		log.Fatal("RTSP_URL environment variable is required")
	}

	dmx := rtsp.New(rtspURL)
	par, err := dmx.Demux()
	if err != nil {
		log.Fatal(err)
	}

	if par.VideoCodecParameters == nil {
		log.Fatal("No video stream found in RTSP source")
	}

	dcd := decoder.NewVideo(100, -1, func() decoder.InnerVideoDecoder {
		return cpu.NewFFmpegCPUDecoder()
	})
	dcd.Decode()

main:
	for {
		packet, err := dmx.ReadPacket()
		if err != nil {
			log.Fatal(err)
		}
		if packet == nil {
			continue
		}

		if vPkt, ok := packet.(gomedia.VideoPacket); ok {
			select {
			case dcd.Packets() <- vPkt:
			case img := <-dcd.Images():
				f, err := os.Create("output.jpg")
				if err != nil {
					log.Fatal(err)
				}
				defer f.Close()
				if err := jpeg.Encode(f, img, nil); err != nil {
					log.Fatal(err)
				}
				log.Println("Successfully saved frame to output.jpg")
				break main
			}
		}
	}
}

