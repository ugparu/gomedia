package main

import (
	"image/jpeg"
	"log"
	"os"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/decoder/video/cpu"
	"github.com/ugparu/gomedia/format/mp4"
)

func main() {
	dmx := mp4.NewDemuxer("input.mp4")
	_, err := dmx.Demux()
	if err != nil {
		log.Fatal(err)
	}

	dcd := decoder.NewVideo(0, -1, func() decoder.InnerVideoDecoder {
		return cpu.NewFFmpegCPUDecoder()
	})
	dcd.Decode()

	f, err := os.Create("output.jpg")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	for {
		packet, err := dmx.ReadPacket()
		if err != nil {
			log.Fatal(err)
		}
		if vPkt, ok := packet.(gomedia.VideoPacket); ok {
			select {
			case dcd.Packets() <- vPkt:
				println(vPkt.IsKeyFrame())
			case img := <-dcd.Images():
				if err := jpeg.Encode(f, img, nil); err != nil {
					log.Fatal(err)
				}
				return
			}
		}
	}
}
