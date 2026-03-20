package main

import (
	"flag"
	"image/jpeg"
	"log"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/decoder/video/rkmpp"
	examplelogger "github.com/ugparu/gomedia/examples/logger"
	"github.com/ugparu/gomedia/format/rtsp"
	"github.com/ugparu/gomedia/reader"
)

func main() {
	logrus.SetLevel(logrus.DebugLevel)

	var (
		rtspURL string
		output  string
	)

	flag.StringVar(&rtspURL, "url", "", "RTSP URL (required, can also use RTSP_URL env var)")
	flag.StringVar(&output, "output", "./output.jpg", "Output JPEG file path")
	flag.Parse()

	if rtspURL == "" {
		rtspURL = os.Getenv("RTSP_URL")
	}
	if rtspURL == "" {
		flag.Usage()
		log.Fatal("RTSP URL is required (use -url flag or RTSP_URL environment variable)")
	}

	reader := reader.NewRTSP(100, reader.WithLogger(examplelogger.New(logrus.InfoLevel)), reader.WithRTSPParams(rtsp.WithLogger(examplelogger.New(logrus.InfoLevel))))
	reader.Read()
	reader.AddURL() <- rtspURL

	dcd := decoder.NewVideo(100, -1, map[gomedia.CodecType]func() decoder.InnerVideoDecoder{
		gomedia.H264: rkmpp.NewFFmpegRKMPPDecoder,
		gomedia.H265: rkmpp.NewFFmpegRKMPPDecoder,
	})
	dcd.Decode()
	defer dcd.Close()

main:
	for {
		var packet gomedia.Packet
		select {
		case packet = <-reader.Packets():
		case <-dcd.Done():
			log.Fatal("Decoder stopped")
		}

		if vPkt, ok := packet.(gomedia.VideoPacket); ok {
			select {
			case dcd.Packets() <- vPkt:
			case img := <-dcd.Images():
				f, err := os.Create(output)
				if err != nil {
					log.Fatal(err)
				}
				defer f.Close()
				if err := jpeg.Encode(f, img, nil); err != nil {
					log.Fatal(err)
				}
				log.Printf("Successfully saved frame to %s", output)
				break main
			}
		}
	}
}
