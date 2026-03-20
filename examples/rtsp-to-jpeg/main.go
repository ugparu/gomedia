package main

import (
	"image/jpeg"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/decoder/video/cuda"
	examplelogger "github.com/ugparu/gomedia/examples/logger"
	"github.com/ugparu/gomedia/format/rtsp"
)

func main() {
	println(cuda.CheckCuda())
	cuda.InitCuda(100)

	log := examplelogger.New(logrus.TraceLevel)

	logrus.SetLevel(logrus.DebugLevel)

	rtspURL := os.Getenv("RTSP_URL")
	if rtspURL == "" {
		log.Errorf(log, "RTSP_URL environment variable is required")
	}

	dmx := rtsp.New(rtspURL)
	par, err := dmx.Demux()
	if err != nil {
		log.Errorf(log, err.Error())
	}

	if par.VideoCodecParameters == nil {
		log.Errorf(log, "No video stream found in RTSP source")
	}

	dcd := decoder.NewVideo(100, -1, map[gomedia.CodecType]func() decoder.InnerVideoDecoder{
		gomedia.H264: cuda.NewFFmpegCUDADecoder,
		gomedia.H265: cuda.NewFFmpegCUDADecoder,
	}, decoder.VideoWithLogger(log))
	dcd.Decode()

main:
	for {
		packet, err := dmx.ReadPacket()
		if err != nil {
			log.Errorf(log, err.Error())
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
					log.Errorf(log, err.Error())
				}
				defer f.Close()
				if err := jpeg.Encode(f, img, nil); err != nil {
					log.Errorf(log, err.Error())
				}
				log.Infof(log, "Successfully saved frame to output.jpg")
				break main
			}
		}
	}
}
