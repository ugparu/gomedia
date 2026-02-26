package main

import (
	"flag"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/decoder/video/rkmpp"
	"github.com/ugparu/gomedia/format/rtsp"
)

func main() {
	var (
		rtspURL   string
		sessions  int
		maxFrames int
		timeout   int
		verbose   bool
		noAudio   bool
	)

	flag.StringVar(&rtspURL, "url", "", "RTSP URL to decode (required, can also use RTSP_URL env var)")
	flag.IntVar(&sessions, "sessions", 1, "Number of parallel read+decode sessions")
	flag.IntVar(&maxFrames, "frames", 100, "Number of frames to decode before exiting (0 = unlimited)")
	flag.IntVar(&timeout, "timeout", 60, "Timeout in seconds")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging")
	flag.BoolVar(&noAudio, "no-audio", false, "Skip audio stream")
	flag.Parse()

	if rtspURL == "" {
		flag.Usage()
		log.Fatal("RTSP URL is required (use -url flag)")
	}

	if sessions <= 0 {
		sessions = 1
	}

	if verbose {
		logrus.SetLevel(logrus.DebugLevel)
	} else {
		logrus.SetLevel(logrus.InfoLevel)
	}

	fmt.Printf("========================================\n")
	fmt.Printf("RKMPP RTSP Stream Info & Decoder\n")
	fmt.Printf("========================================\n")
	fmt.Printf("RTSP URL: %s\n", rtspURL)
	fmt.Printf("Sessions: %d\n", sessions)
	fmt.Printf("Max Frames: %d\n", maxFrames)
	fmt.Printf("Timeout: %d seconds\n", timeout)
	fmt.Printf("========================================\n\n")

	var wg sync.WaitGroup

	for i := 1; i <= sessions; i++ {
		sessionID := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			runSession(sessionID, rtspURL, maxFrames, timeout, verbose, noAudio)
		}()
	}

	wg.Wait()
}

func runSession(id int, rtspURL string, maxFrames, timeout int, verbose, noAudio bool) {
	prefix := fmt.Sprintf("[Session %d] ", id)

	// Create RTSP demuxer
	var dmx gomedia.Demuxer
	if noAudio {
		dmx = rtsp.New(rtspURL, rtsp.NoAudio())
	} else {
		dmx = rtsp.New(rtspURL)
	}

	fmt.Printf("%sConnecting to RTSP stream...\n", prefix)
	params, err := dmx.Demux()
	if err != nil {
		log.Printf("%sFailed to connect to RTSP stream: %v\n", prefix, err)
		return
	}

	fmt.Printf("%s✓ Connected successfully!\n", prefix)

	// Display stream information (only for first session to reduce noise)
	if id == 1 {
		printStreamInfo(params)
	}

	// Create RKMPP video decoder
	if params.VideoCodecParameters != nil {
		fmt.Printf("\n%s========================================\n", prefix)
		fmt.Printf("%sInitializing RKMPP hardware decoder...\n", prefix)
		fmt.Printf("%s========================================\n", prefix)
		dcd := decoder.NewVideo(100, -1, rkmpp.NewFFmpegRKMPPDecoder)
		dcd.Decode()

		fmt.Printf("%s✓ RKMPP decoder initialized\n", prefix)
		fmt.Printf("%sStarting decode process...\n", prefix)

		frameCount := 0
		packetCount := 0
		audioPacketCount := 0
		startTime := time.Now()
		timeoutChan := time.After(time.Duration(timeout) * time.Second)
		lastInfoTime := time.Now()
		var totalDataSize uint64

		go func() {
			for {
				select {
				case <-timeoutChan:
					fmt.Printf("\n%s⏱ Timeout reached after %d seconds\n", prefix, timeout)
					return

				default:
					// Read packet from RTSP stream
					packet, err := dmx.ReadPacket()
					if err != nil {
						fmt.Printf("%sError reading packet: %v\n", prefix, err)
						return
					}

					if packet == nil {
						continue
					}

					totalDataSize += uint64(packet.Len())

					// Process video packets
					if vPkt, ok := packet.(gomedia.VideoPacket); ok {
						packetCount++

						// Print packet info every second
						if time.Since(lastInfoTime) >= time.Second {
							elapsed := time.Since(startTime).Seconds()
							dataRateMbps := float64(totalDataSize*8) / elapsed / 1000000

							fmt.Printf("%s[Packet] Count: %d | Data rate: %.2f Mbps | Keyframe: %v | PTS: %v\n",
								prefix,
								packetCount,
								dataRateMbps,
								vPkt.IsKeyFrame(),
								vPkt.Timestamp())
							lastInfoTime = time.Now()
						}

						dcd.Packets() <- vPkt
					} else if aPkt, ok := packet.(gomedia.AudioPacket); ok {
						audioPacketCount++
						if audioPacketCount%100 == 0 {
							fmt.Printf("%s[Audio] Packets: %d | PTS: %v\n",
								prefix,
								audioPacketCount,
								aPkt.Timestamp())
						}
					}
				}
			}
		}()

	main:
		for {
			select {
			case <-timeoutChan:
				fmt.Printf("\n%s⏱ Timeout reached after %d seconds\n", prefix, timeout)
				break main
			default:
				img := <-dcd.Images()
				frameCount++
				elapsed := time.Since(startTime).Seconds()
				fps := float64(frameCount) / elapsed

				fmt.Printf("%s[Frame %d] Decoded: %dx%d | Avg FPS: %.2f | Elapsed: %.2fs\n",
					prefix,
					frameCount,
					img.Bounds().Dx(),
					img.Bounds().Dy(),
					fps,
					elapsed)

				if maxFrames > 0 && frameCount >= maxFrames {
					fmt.Printf("\n%s✓ Reached maximum frame count (%d)\n", prefix, maxFrames)
					break main
				}
			}
		}

		// Cleanup
		dcd.Close()
		dmx.Close()

		// Print summary
		fmt.Printf("\n%s========================================\n", prefix)
		fmt.Printf("%sSummary\n", prefix)
		fmt.Printf("%s========================================\n", prefix)
		elapsed := time.Since(startTime).Seconds()
		fmt.Printf("%sVideo frames decoded: %d\n", prefix, frameCount)
		fmt.Printf("%sVideo packets received: %d\n", prefix, packetCount)
		fmt.Printf("%sAudio packets received: %d\n", prefix, audioPacketCount)
		fmt.Printf("%sTotal time: %.2f seconds\n", prefix, elapsed)
		if frameCount > 0 {
			fmt.Printf("%sAverage FPS: %.2f\n", prefix, float64(frameCount)/elapsed)
		}
		fmt.Printf("%sTotal data received: %.2f MB\n", prefix, float64(totalDataSize)/1024/1024)
		fmt.Printf("%sAverage data rate: %.2f Mbps\n", prefix, float64(totalDataSize*8)/elapsed/1000000)
		fmt.Printf("%s========================================\n", prefix)
	} else {
		fmt.Printf("%sNo video stream found!\n", prefix)
		dmx.Close()
	}
}

func printStreamInfo(params gomedia.CodecParametersPair) {
	fmt.Println("========================================")
	fmt.Println("Stream Information")
	fmt.Println("========================================")

	// Video stream info
	if params.VideoCodecParameters != nil {
		fmt.Println("\n📹 VIDEO STREAM:")
		fmt.Printf("  Codec Type:    %s\n", getCodecTypeName(params.VideoCodecParameters.Type()))
		fmt.Printf("  Codec Tag:     %s\n", params.VideoCodecParameters.Tag())
		fmt.Printf("  Resolution:    %dx%d\n", params.VideoCodecParameters.Width(), params.VideoCodecParameters.Height())
		fmt.Printf("  Frame Rate:    %d fps\n", params.VideoCodecParameters.FPS())
		fmt.Printf("  Bitrate:       %d bps (%.2f Mbps)\n",
			params.VideoCodecParameters.Bitrate(),
			float64(params.VideoCodecParameters.Bitrate())/1000000)
		fmt.Printf("  Stream Index:  %d\n", params.VideoCodecParameters.StreamIndex())

		// Display additional H264/H265 specific info
		printVideoCodecDetails(params.VideoCodecParameters)
	} else {
		fmt.Println("\n📹 VIDEO STREAM: None")
	}

	// Audio stream info
	if params.AudioCodecParameters != nil {
		fmt.Println("\n🔊 AUDIO STREAM:")
		fmt.Printf("  Codec Type:    %s\n", getCodecTypeName(params.AudioCodecParameters.Type()))
		fmt.Printf("  Codec Tag:     %s\n", params.AudioCodecParameters.Tag())
		fmt.Printf("  Sample Rate:   %d Hz\n", params.AudioCodecParameters.SampleRate())
		fmt.Printf("  Channels:      %d\n", params.AudioCodecParameters.Channels())
		fmt.Printf("  Sample Format: %s\n", params.AudioCodecParameters.SampleFormat())
		fmt.Printf("  Bitrate:       %d bps (%.2f kbps)\n",
			params.AudioCodecParameters.Bitrate(),
			float64(params.AudioCodecParameters.Bitrate())/1000)
		fmt.Printf("  Stream Index:  %d\n", params.AudioCodecParameters.StreamIndex())
	} else {
		fmt.Println("\n🔊 AUDIO STREAM: None")
	}

	fmt.Println("\n========================================")
}

func getCodecTypeName(codecType gomedia.CodecType) string {
	switch codecType {
	case gomedia.H264:
		return "H.264/AVC"
	case gomedia.H265:
		return "H.265/HEVC"
	case gomedia.MJPEG:
		return "MJPEG"
	case gomedia.AAC:
		return "AAC"
	case gomedia.OPUS:
		return "OPUS"
	case gomedia.PCMAlaw:
		return "PCM A-law"
	case gomedia.PCMUlaw:
		return "PCM μ-law"
	default:
		return fmt.Sprintf("Unknown (%d)", codecType)
	}
}

func printVideoCodecDetails(params gomedia.VideoCodecParameters) {
	// Try to print H264-specific details if available
	codecTag := params.Tag()
	if len(codecTag) >= 4 && codecTag[:4] == "avc1" {
		fmt.Printf("\n  H.264 Profile Information:\n")
		if len(codecTag) >= 13 {
			// Parse profile from tag like "avc1.64001F"
			fmt.Printf("  Codec Tag Details: %s\n", codecTag)
		}
	} else if len(codecTag) >= 4 && codecTag[:4] == "hev1" {
		fmt.Printf("\n  H.265 Profile Information:\n")
		fmt.Printf("  Codec Tag Details: %s\n", codecTag)
	}
}
