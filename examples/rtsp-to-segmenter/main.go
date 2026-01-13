package main

import (
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/reader"
	"github.com/ugparu/gomedia/writer/segmenter"
)

// Get RTSP URLs from environment variable
// Example: RTSP_URLS="rtsp://camera1:554/stream,rtsp://camera2:554/stream,rtsp://camera3:554/stream"
var rtspURLs = strings.Split(os.Getenv("RTSP_URLS"), ",")

func main() {
	logrus.Info("Starting multi-URL RTSP to Segmenter example...")
	logrus.SetLevel(logrus.InfoLevel)

	if len(rtspURLs) == 0 || rtspURLs[0] == "" {
		logrus.Fatal("RTSP_URLS environment variable not set. Example: RTSP_URLS=\"rtsp://camera1:554/stream,rtsp://camera2:554/stream\"")
	}

	logrus.Infof("Connecting to %d RTSP stream(s): %v", len(rtspURLs), rtspURLs)

	// Initialize Segmenter for MP4 recording
	// Files will be saved to ./recordings/ with index-based naming:
	// - Stream 0: recordings/YYYY/MM/DD/0_2026-01-13T15:04:05.mp4
	// - Stream 1: recordings/YYYY/MM/DD/1_2026-01-13T15:04:05.mp4
	// - Stream 2: recordings/YYYY/MM/DD/2_2026-01-13T15:04:05.mp4
	const (
		segmentDuration = 15 * time.Second
		channelSize     = 100
	)

	seg := segmenter.New("./recordings/", segmentDuration, gomedia.Always, channelSize)
	seg.Write()
	logrus.Infof("Segmenter initialized: destination=./recordings/, segment_duration=%v, record_mode=Always", segmentDuration)

	// Initialize RTSP reader
	rdr := reader.NewRTSP(channelSize)
	rdr.Read()

	// Add all RTSP URLs to both the segmenter and reader
	for i, rtspURL := range rtspURLs {
		logrus.Infof("Adding source %d: %s", i, rtspURL)
		seg.AddSource() <- rtspURL
		rdr.AddURL() <- rtspURL
	}

	// Log recorded segments
	go func() {
		for fileInfo := range seg.Files() {
			logrus.Infof("✓ Recorded segment: %s (size: %d bytes, duration: %v)",
				fileInfo.Name, fileInfo.Size, fileInfo.Stop.Sub(fileInfo.Start))
		}
	}()

	// Monitor recording status
	go func() {
		for status := range seg.RecordCurStatus() {
			if status {
				logrus.Info("📹 Recording started")
			} else {
				logrus.Info("⏸️  Recording stopped")
			}
		}
	}()

	// Process packets from all RTSP streams
	go func() {
		logrus.Info("Starting packet processing...")
		packetCount := 0
		lastLog := time.Now()
		urlPacketCounts := make(map[string]int)

		for pkt := range rdr.Packets() {
			if pkt == nil {
				continue
			}

			packetCount++
			urlPacketCounts[pkt.URL()]++

			// Send packet to segmenter
			seg.Packets() <- pkt.Clone(false)
			pkt.Close()

			// Log progress every 5 seconds
			if time.Since(lastLog) > 5*time.Second {
				logrus.Infof("📊 Processed %d total packets:", packetCount)
				for i, url := range rtspURLs {
					logrus.Infof("   Stream %d (%s): %d packets", i, url, urlPacketCounts[url])
				}
				lastLog = time.Now()
			}
		}
	}()

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	logrus.Info("✅ All systems running. Press Ctrl+C to stop.")
	logrus.Info("📁 Recording files to: ./recordings/YYYY/MM/DD/")
	logrus.Info("📝 File naming format: <stream_index>_<timestamp>.mp4")

	// Wait for interrupt signal
	<-sigChan
	logrus.Info("🛑 Shutdown signal received, closing...")

	// Cleanup
	rdr.Close()
	seg.Close()

	logrus.Info("👋 Shutdown complete")
}
