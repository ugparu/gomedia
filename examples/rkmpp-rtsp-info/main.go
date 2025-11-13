package main

import (
	"flag"
	"fmt"
	"log"
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
		maxFrames int
		timeout   int
		verbose   bool
		noAudio   bool
	)

	flag.StringVar(&rtspURL, "url", "", "RTSP URL to decode (required, can also use RTSP_URL env var)")
	flag.IntVar(&maxFrames, "frames", 100, "Number of frames to decode before exiting (0 = unlimited)")
	flag.IntVar(&timeout, "timeout", 60, "Timeout in seconds")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging")
	flag.BoolVar(&noAudio, "no-audio", false, "Skip audio stream")
	flag.Parse()

	if rtspURL == "" {
		flag.Usage()
		log.Fatal("RTSP URL is required (use -url flag)")
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
	fmt.Printf("Max Frames: %d\n", maxFrames)
	fmt.Printf("Timeout: %d seconds\n", timeout)
	fmt.Printf("========================================\n\n")

	// Create RTSP demuxer
	var dmx gomedia.Demuxer
	if noAudio {
		dmx = rtsp.New(rtspURL, gomedia.NoAudio)
	} else {
		dmx = rtsp.New(rtspURL)
	}

	fmt.Println("Connecting to RTSP stream...")
	params, err := dmx.Demux()
	if err != nil {
		log.Fatalf("Failed to connect to RTSP stream: %v", err)
	}

	fmt.Println("✓ Connected successfully!\n")

	// Display stream information
	printStreamInfo(params)

	// Create RKMPP video decoder
	if params.VideoCodecParameters != nil {
		fmt.Println("\n========================================")
		fmt.Println("Initializing RKMPP hardware decoder...")
		fmt.Println("========================================")
		dcd := decoder.NewVideo(100, -1, rkmpp.NewFFmpegRKMPPDecoder)
		dcd.Decode()

		fmt.Println("✓ RKMPP decoder initialized\n")
		fmt.Println("Starting decode process...\n")

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
					fmt.Printf("\n⏱ Timeout reached after %d seconds\n", timeout)
					return

				default:
					// Read packet from RTSP stream
					packet, err := dmx.ReadPacket()
					if err != nil {
						fmt.Printf("Error reading packet: %v\n", err)
						return
					}

					if packet == nil {
						continue
					}

					totalDataSize += uint64(len(packet.Data()))

					// Process video packets
					if vPkt, ok := packet.(gomedia.VideoPacket); ok {
						packetCount++

						// Print packet info every second
						if time.Since(lastInfoTime) >= time.Second {
							elapsed := time.Since(startTime).Seconds()
							dataRateMbps := float64(totalDataSize*8) / elapsed / 1000000

							fmt.Printf("[Packet] Count: %d | Data rate: %.2f Mbps | Keyframe: %v | Size: %d bytes | PTS: %v\n",
								packetCount,
								dataRateMbps,
								vPkt.IsKeyFrame(),
								len(vPkt.Data()),
								vPkt.Timestamp())
							lastInfoTime = time.Now()
						}

						dcd.Packets() <- vPkt
					} else if aPkt, ok := packet.(gomedia.AudioPacket); ok {
						audioPacketCount++
						if audioPacketCount%100 == 0 {
							fmt.Printf("[Audio] Packets: %d | Size: %d bytes | PTS: %v\n",
								audioPacketCount,
								len(aPkt.Data()),
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
				fmt.Printf("\n⏱ Timeout reached after %d seconds\n", timeout)
				break main
			default:
				img := <-dcd.Images()
				frameCount++
				elapsed := time.Since(startTime).Seconds()
				fps := float64(frameCount) / elapsed

				fmt.Printf("[Frame %d] Decoded: %dx%d | Avg FPS: %.2f | Elapsed: %.2fs\n",
					frameCount,
					img.Bounds().Dx(),
					img.Bounds().Dy(),
					fps,
					elapsed)

				if maxFrames > 0 && frameCount >= maxFrames {
					fmt.Printf("\n✓ Reached maximum frame count (%d)\n", maxFrames)
					break main
				}
			}
		}

		// Cleanup
		dcd.Close()
		dmx.Close()

		// Print summary
		fmt.Printf("\n========================================\n")
		fmt.Printf("Summary\n")
		fmt.Printf("========================================\n")
		elapsed := time.Since(startTime).Seconds()
		fmt.Printf("Video frames decoded: %d\n", frameCount)
		fmt.Printf("Video packets received: %d\n", packetCount)
		fmt.Printf("Audio packets received: %d\n", audioPacketCount)
		fmt.Printf("Total time: %.2f seconds\n", elapsed)
		if frameCount > 0 {
			fmt.Printf("Average FPS: %.2f\n", float64(frameCount)/elapsed)
		}
		fmt.Printf("Total data received: %.2f MB\n", float64(totalDataSize)/1024/1024)
		fmt.Printf("Average data rate: %.2f Mbps\n", float64(totalDataSize*8)/elapsed/1000000)
		fmt.Printf("========================================\n")
	} else {
		fmt.Println("No video stream found!")
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
