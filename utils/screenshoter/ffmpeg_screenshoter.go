// Package screenshoter captures JPEG screenshots from RTSP video streams
// by wiring up a short-lived Reader + CPU decoder pipeline per call.
package screenshoter

//go:generate mockgen -source=ffmpeg_screenshoter.go -destination=../../mocks/mock_screenshoter.go -package=mocks

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/decoder/video/cpu"
	"github.com/ugparu/gomedia/frame/rgb"
	"github.com/ugparu/gomedia/reader"
	"github.com/ugparu/gomedia/utils/logger"
	"golang.org/x/image/draw"
)

const bufSize = 100

type Screenshoter interface {
	Screenshot(url string) ([]byte, error)
}

type ffmpegScreenshoter struct {
	log logger.Logger
}

func NewScreenshoter() Screenshoter {
	return &ffmpegScreenshoter{log: logger.Default}
}

// Screenshot connects to url, waits for a decoded keyframe, and returns it as a JPEG.
// Returns an error if the full pipeline (connect + decode) does not complete within 30s.
func (screenshoter *ffmpegScreenshoter) Screenshot(url string) ([]byte, error) {
	screenshotReader := reader.NewRTSP(bufSize)
	defer screenshotReader.Close()

	const screenshotProbeTime = 30 * time.Second
	timer := time.After(screenshotProbeTime)

	select {
	case screenshotReader.AddURL() <- url:
	case <-timer:
		return nil, errors.New("timed out")
	}

	screenshotReader.Read()

	screenshotDecoder := decoder.NewVideo(bufSize, -1, map[gomedia.CodecType]func() decoder.InnerVideoDecoder{
		gomedia.H264:  cpu.NewFFmpegCPUDecoder,
		gomedia.H265:  cpu.NewFFmpegCPUDecoder,
		gomedia.MJPEG: cpu.NewFFmpegCPUDecoder,
	})
	defer screenshotDecoder.Close()

	screenshotDecoder.Decode()

	for {
		select {
		case <-timer:
			return nil, errors.New("timed out")
		case packet := <-screenshotReader.Packets():
			if vPkt, ok := packet.(gomedia.VideoPacket); ok {
				select {
				case screenshotDecoder.Packets() <- vPkt:
				case <-screenshotDecoder.Done():
					vPkt.Release()
					return nil, errors.New("decoder stopped")
				case <-timer:
					vPkt.Release()
					return nil, errors.New("timed out")
				}
			} else {
				packet.Release()
			}
		case mat := <-screenshotDecoder.Images():
			buff := new(bytes.Buffer)

			const screenshotWidth = 640
			const screenshotHeight = 480

			smallImg := rgb.NewRGB(image.Rect(0, 0, screenshotWidth, screenshotHeight))
			draw.NearestNeighbor.Scale(smallImg, smallImg.Rect, mat, mat.Bounds(), draw.Over, nil)
			mat.Release()

			if err := jpeg.Encode(buff, smallImg, nil); err != nil {
				return nil, err
			}

			screenshoter.log.Debugf(screenshoter, "Screenshot extracted successfully from %s", url)

			return buff.Bytes(), nil
		}
	}
}
