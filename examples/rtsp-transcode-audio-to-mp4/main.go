package main

import (
	"log"
	"os"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/decoder/aac"
	"github.com/ugparu/gomedia/decoder/pcm"
	"github.com/ugparu/gomedia/encoder"
	aacEnc "github.com/ugparu/gomedia/encoder/aac"
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

	decoder := decoder.NewAudioDecoder(100, map[gomedia.CodecType]func() decoder.InnerAudioDecoder{
		gomedia.PCMAlaw: func() decoder.InnerAudioDecoder {
			return pcm.NewALAWDecoder()
		},
		gomedia.PCMUlaw: func() decoder.InnerAudioDecoder {
			return pcm.NewULAWDecoder()
		},
		gomedia.AAC: func() decoder.InnerAudioDecoder {
			return aac.NewAacDecoder()
		},
	})
	decoder.Decode()

	encoder := encoder.NewAudioEncoder(100, aacEnc.NewAacEncoder)
	encoder.Encode()

	go func() {
		for packet := range decoder.Samples() {
			encoder.Samples() <- packet
		}
	}()

	var packets []gomedia.Packet

	go func() {
		for packet := range encoder.Packets() {
			packets = append(packets, packet)
		}
	}()

	const packetsToMp4 = 100
	for {
		packet, err := dmx.ReadPacket()
		if err != nil {
			log.Fatal(err)
		}
		if packet == nil {
			continue
		}

		switch pkt := packet.(type) {
		case gomedia.AudioPacket:
			decoder.Packets() <- pkt
		case gomedia.VideoPacket:
			packets = append(packets, pkt)
		}

		if len(packets) >= packetsToMp4 {
			break
		}
	}

	f, err := os.Create("output.mp4")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	for _, packet := range packets {
		aPacket, ok := packet.(gomedia.AudioPacket)
		if !ok {
			continue
		}
		par.AudioCodecParameters = aPacket.CodecParameters()
		break
	}

	mx := mp4.NewMuxer(f)
	if err = mx.Mux(par); err != nil {
		log.Fatal(err)
	}

	for _, packet := range packets {
		if err := mx.WritePacket(packet); err != nil {
			log.Fatal(err)
		}
	}

	if err := mx.WriteTrailer(); err != nil {
		log.Fatal(err)
	}
}
