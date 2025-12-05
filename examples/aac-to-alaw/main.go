package main

import (
	"os"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/decoder/aac"
	"github.com/ugparu/gomedia/encoder"
	"github.com/ugparu/gomedia/encoder/pcm"
	"github.com/ugparu/gomedia/reader"
	"github.com/ugparu/gomedia/utils/buffer"
)

func main() {
	rdr := reader.NewRTSP(100)
	rdr.Read()
	defer rdr.Close()
	rdr.AddURL() <- os.Getenv("RTSP_URL")

	audioDecoder := decoder.NewAudioDecoder(100, map[gomedia.CodecType]func() decoder.InnerAudioDecoder{
		gomedia.AAC: aac.NewAacDecoder,
	})
	audioDecoder.Decode()

	go func() {
		for pkt := range rdr.Packets() {
			if inPkt, ok := pkt.(gomedia.AudioPacket); ok {
				audioDecoder.Packets() <- inPkt
			}
		}
	}()

	alawEnc := encoder.NewAudioEncoder(100, pcm.NewAlawEncoder)
	alawEnc.Encode()

	go func() {
		for pkt := range audioDecoder.Samples() {
			alawEnc.Samples() <- pkt
		}
	}()

	f, err := os.Create("output.pcm")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	packets := 0
	for pkt := range alawEnc.Packets() {
		pkt.View(func(data buffer.PooledBuffer) {
			f.Write(data.Data())
		})
		packets++
		if packets > 100 {
			break
		}
	}

}
