package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/mp4"
	"github.com/ugparu/gomedia/utils/logger"
)

func main() {
	srcDir := os.Getenv("SRC_DIR")
	if srcDir == "" {
		srcDir = "./src"
	}

	files, err := os.ReadDir(srcDir)
	if err != nil {
		log.Fatal(err)
	}

	if len(files) == 0 {
		return
	}

	mpDmx := mp4.NewDemuxer(filepath.Join(srcDir, files[0].Name()))
	pair, err := mpDmx.Demux()
	if err != nil {
		log.Fatal(err)
	}

	mp4Path := "./merged.mp4"
	f, err := os.Create(mp4Path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	mp4wr := mp4.NewMuxer(f)
	if err = mp4wr.Mux(pair); err != nil {
		logger.Errorf(mpDmx, "mux error: %v", err)
	}

	var lastTimestamp time.Duration
	for {
		var pkt gomedia.Packet
		pkt, err = mpDmx.ReadPacket()
		if err != nil {
			if err.Error() == io.EOF.Error() {
				break
			}
			logger.Errorf(mpDmx, "read packet error: %v", err)
			break
		}
		lastTimestamp = pkt.Timestamp()
		if err = mp4wr.WritePacket(pkt); err != nil {
			logger.Errorf(mpDmx, "write packet error: %v", err)
			break
		}
	}
	mpDmx.Close()

	for _, file := range files[1:] {
		mpDmx = mp4.NewDemuxer(filepath.Join(srcDir, file.Name()))
		_, err = mpDmx.Demux()
		if err != nil {
			logger.Errorf(mpDmx, "demux error: %v", err)
			break
		}

		var firstPacketTimestamp time.Duration
		var timestampOffset time.Duration
		firstPacketRead := false

		for {
			var pkt gomedia.Packet
			pkt, err = mpDmx.ReadPacket()
			if err != nil {
				if err.Error() == io.EOF.Error() {
					break
				}
				logger.Errorf(mpDmx, "read packet error: %v", err)
				break
			}

			// For the first packet from this file, calculate the timestamp offset
			if !firstPacketRead {
				firstPacketTimestamp = pkt.Timestamp()
				// Add a small buffer to ensure continuation and avoid timestamp overlap
				timestampOffset = lastTimestamp + time.Millisecond - firstPacketTimestamp
				firstPacketRead = true
			}

			// Adjust the packet timestamp to continue from where the previous file ended
			adjustedTimestamp := pkt.Timestamp() + timestampOffset
			pkt.SetTimestamp(adjustedTimestamp)
			lastTimestamp = adjustedTimestamp

			if err = mp4wr.WritePacket(pkt); err != nil {
				logger.Errorf(mpDmx, "write packet error: %v", err)
				break
			}
		}
		mpDmx.Close()
	}

	if err = mp4wr.WriteTrailer(); err != nil {
		logger.Errorf(mpDmx, "write trailer error: %v", err)
	}
}
