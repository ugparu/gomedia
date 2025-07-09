package main

import (
	"log"
	"os"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/mp4"
	"github.com/ugparu/gomedia/format/rtsp"
)

func main() {
	rtspURL := os.Getenv("RTSP_URL")

	dmx := rtsp.New(rtspURL)
	par, err := dmx.Demux()
	if err != nil {
		log.Fatal(err)
	}

	const packetsToMp4 = 100

	f, err := os.Create("output.mp4")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	mx := mp4.NewMuxer(f)
	if err := mx.Mux(par); err != nil {
		log.Fatal(err)
	}

	i := 0
	for {
		packet, err := dmx.ReadPacket()
		if err != nil {
			log.Fatal(err)
		}
		if packet == nil {
			continue
		}
		i++

		if i >= packetsToMp4 {
			break
		}
		println(packet.(gomedia.VideoPacket).CodecParameters() == par.VideoCodecParameters)
		if err := mx.WritePacket(packet); err != nil {
			log.Fatal(err)
		}
	}

	if err := mx.WriteTrailer(); err != nil {
		log.Fatal(err)
	}
}
