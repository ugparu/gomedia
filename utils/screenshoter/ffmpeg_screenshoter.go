// Package screenshoter provides functionality for capturing screenshots from RTSP video streams.
package screenshoter

import (
	"bytes"
	"errors"
	"fmt"
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

// bufSize represents the buffer size for channels.
const bufSize = 100

// Screenshoter is the interface for capturing screenshots from RTSP video streams.
type Screenshoter interface {
	Screenshot(url string) ([]byte, error)
}

// NewScreenshoter creates a new instance of the Screenshoter interface using ffmpegScreenshoter.
func NewScreenshoter() Screenshoter {
	return &ffmpegScreenshoter{}
}

// ffmpegScreenshoter is an implementation of the Screenshoter interface.
type ffmpegScreenshoter struct {
}

// Screenshot captures a screenshot from the provided RTSP video stream URL.
func (screenshoter *ffmpegScreenshoter) Screenshot(url string) ([]byte, error) {
	// Create a new RTSP reader with a buffer size.
	screenshotReader := reader.NewRTSP(bufSize)
	defer screenshotReader.Close()

	// Set a timer for a maximum waiting time (30 seconds).
	const screenshotProbeTime = 30 * time.Second
	timer := time.After(screenshotProbeTime)

	select {
	case screenshotReader.AddURL() <- url:
	case <-timer:
		return nil, errors.New("timed out")
	}

	// Read codec parameters from the RTSP stream.
	screenshotReader.Read()

	// Create a new video decoder with CPU acceleration and the specified buffer size.
	screenshotDecoder := decoder.NewVideo(bufSize, -1, cpu.NewFFmpegCPUDecoder)
	defer screenshotDecoder.Close()

	screenshotDecoder.Decode()

	for {
		select {
		// Handle timeout.
		case <-timer:
			return nil, errors.New("timed out")
		// Receive packets from the RTSP stream.
		case packet := <-screenshotReader.Packets():
			if vPkt, ok := packet.(gomedia.VideoPacket); ok {
				select {
				case screenshotDecoder.Packets() <- vPkt:
				case <-screenshotDecoder.Done():
					return nil, errors.New("decoder stopped")
				case <-timer:
					return nil, errors.New("timed out")
				}
			}
		// Receive images from the video decoder.
		case mat := <-screenshotDecoder.Images():
			buff := new(bytes.Buffer)

			// Define constants for the screenshot width and height.
			const screenshotWidth = 640
			const screenshotHeight = 480

			// Resize the image to the specified width and height using nearest-neighbor interpolation.
			smallImg := rgb.NewRGB(image.Rect(0, 0, screenshotWidth, screenshotHeight))
			draw.NearestNeighbor.Scale(smallImg, smallImg.Rect, mat, mat.Bounds(), draw.Over, nil)

			// Encode the resized image as PNG and write it to the buffer.
			if err := jpeg.Encode(buff, smallImg, nil); err != nil {
				return nil, err
			}

			// Log successful extraction of the screenshot.
			logger.Debugf(screenshoter, fmt.Sprintf("Screenshot extracted successfully from %s", url))

			// Return the PNG-encoded image bytes.
			return buff.Bytes(), nil
		}
	}
}
