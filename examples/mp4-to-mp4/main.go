package main

import (
	"log"
	"os"

	"github.com/ugparu/gomedia/format/mp4"
)

func main() {
	dmx := mp4.NewDemuxer("input.mp4")
	param, err := dmx.Demux()
	if err != nil {
		log.Fatal(err)
	}

	f, err := os.Create("output.mp4")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	mux := mp4.NewMuxer(f)
	if err := mux.Mux(param); err != nil {
		log.Fatal(err)
	}

	for {
		packet, err := dmx.ReadPacket()
		if err != nil {
			break
		}
		if err := mux.WritePacket(packet); err != nil {
			log.Fatal(err)
		}
	}

	if err := mux.WriteTrailer(); err != nil {
		log.Fatal(err)
	}
}
