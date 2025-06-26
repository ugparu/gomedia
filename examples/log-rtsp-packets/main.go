package main

import (
	"log"
	"os"

	"github.com/ugparu/gomedia/format/rtsp"
	"github.com/ugparu/gomedia/utils/logger"
)

func main() {
	rtspURL := os.Getenv("RTSP_URL")

	dmx := rtsp.New(rtspURL)
	if _, err := dmx.Demux(); err != nil {
		log.Fatal(err)
	}

	i := 0
	for {
		packet, err := dmx.ReadPacket()
		if err != nil {
			log.Fatal(err)
		}
		if packet != nil {
			logger.Infof("DEMO", "packet %d; stream %d; size %d", i, packet.StreamIndex(), len(packet.Data()))
			i++
		}
	}
}
