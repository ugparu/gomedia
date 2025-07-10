package main

import (
	"image/jpeg"
	"log"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/decoder/video/cpu"
	"github.com/ugparu/gomedia/format/mp4"
)

func main() {
	logrus.SetLevel(logrus.DebugLevel)

	for {
		dmx := mp4.NewDemuxer("inp0.mp4")
		_, err := dmx.Demux()
		if err != nil {
			log.Fatal(err)
		}

		dcd := decoder.NewVideo(0, -1, func() decoder.InnerVideoDecoder {
			return cpu.NewFFmpegCPUDecoder()
		})
		dcd.Decode()

	main:
		for {
			packet, err := dmx.ReadPacket()
			if err != nil {
				break main
			}
			if vPkt, ok := packet.(gomedia.VideoPacket); ok {
				select {
				case dcd.Packets() <- vPkt:
					println(vPkt.IsKeyFrame())
				case img := <-dcd.Images():
					f, err := os.Create("output.jpg")
					if err != nil {
						log.Fatal(err)
					}
					defer f.Close()
					if err := jpeg.Encode(f, img, nil); err != nil {
						log.Fatal(err)
					}
					break main
				}
			}
		}

	}
}
