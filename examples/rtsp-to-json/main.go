// rtsp-to-json connects to an RTSP source, dumps codec parameters and
// the first N raw RTP-demuxed packets to JSON files for offline testing.
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/rtsp"
)

// --- JSON output types ---

type ParametersJSON struct {
	URL   string           `json:"url"`
	Video *VideoParamsJSON `json:"video,omitempty"`
	Audio *AudioParamsJSON `json:"audio,omitempty"`
}

type VideoParamsJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	Record      string `json:"record,omitempty"`
	SPS         string `json:"sps,omitempty"`
	PPS         string `json:"pps,omitempty"`
	VPS         string `json:"vps,omitempty"`
}

type AudioParamsJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	Config      string `json:"config,omitempty"`
}

type PacketsJSON struct {
	Packets []PacketJSON `json:"packets"`
}

type PacketJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	TimestampNs int64  `json:"timestamp_ns"`
	DurationNs  int64  `json:"duration_ns"`
	IsKeyframe  bool   `json:"is_keyframe,omitempty"`
	Size        int    `json:"size"`
	Data        string `json:"data"`
}

// Optional interfaces that concrete codec parameter types may implement.
type spsProvider interface{ SPS() []byte }
type ppsProvider interface{ PPS() []byte }
type vpsProvider interface{ VPS() []byte }
type recordProvider interface{ AVCDecoderConfRecordBytes() []byte }
type configBytesProvider interface{ MPEG4AudioConfigBytes() []byte }

func main() {
	url := flag.String("url", "", "RTSP URL (required)")
	n := flag.Int("n", 100, "number of packets to capture")
	paramsFile := flag.String("params-file", "parameters.json", "output file for codec parameters")
	packetsFile := flag.String("packets-file", "packets.json", "output file for captured packets")
	split := flag.Bool("split", false, "write one file per codec (e.g. packets_H264.json, parameters_AAC.json)")
	flag.Parse()

	if *url == "" {
		fmt.Fprintln(os.Stderr, "error: -url is required")
		flag.Usage()
		os.Exit(1)
	}

	logrus.SetLevel(logrus.InfoLevel)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Create RTSP demuxer directly for full access to codec parameters.
	dmx := rtsp.New(*url)

	logrus.Infof("Connecting to %s", *url)
	params, err := dmx.Demux()
	if err != nil {
		logrus.Fatalf("Demux failed: %v", err)
	}
	logrus.Infof("Connected. Video: %t, Audio: %t",
		params.VideoCodecParameters != nil,
		params.AudioCodecParameters != nil)

	// --- Capture N packets, tracking last codec parameters per codec ---
	var lastVideoParams gomedia.VideoCodecParameters
	var lastAudioParams gomedia.AudioCodecParameters
	captured := make([]PacketJSON, 0, *n)
	// per-codec buckets used when -split is set
	perCodec := map[string][]PacketJSON{}
	logrus.Infof("Capturing %d packets...", *n)

	for len(captured) < *n {
		select {
		case <-sigCh:
			logrus.Warn("Interrupted, writing partial capture")
			goto done
		default:
		}

		pkt, err := dmx.ReadPacket()
		if err != nil {
			logrus.Fatalf("ReadPacket failed: %v", err)
		}
		if pkt == nil {
			continue
		}

		entry := PacketJSON{
			StreamIndex: pkt.StreamIndex(),
			TimestampNs: int64(pkt.Timestamp()),
			DurationNs:  int64(pkt.Duration()),
			Size:        pkt.Len(),
			Data:        b64(pkt.Data()),
		}

		switch p := pkt.(type) {
		case gomedia.VideoPacket:
			lastVideoParams = p.CodecParameters()
			entry.Codec = lastVideoParams.Type().String()
			entry.IsKeyframe = p.IsKeyFrame()
		case gomedia.AudioPacket:
			lastAudioParams = p.CodecParameters()
			entry.Codec = lastAudioParams.Type().String()
		}

		captured = append(captured, entry)
		if *split && entry.Codec != "" {
			perCodec[entry.Codec] = append(perCodec[entry.Codec], entry)
		}
		pkt.Release()

		if len(captured)%50 == 0 {
			logrus.Infof("Captured %d/%d packets", len(captured), *n)
		}
	}

done:
	dmx.Close()

	// --- Build and write parameters JSON from last received packets ---
	pj := ParametersJSON{URL: *url}

	if vp := lastVideoParams; vp != nil {
		vj := &VideoParamsJSON{
			Codec:       vp.Type().String(),
			StreamIndex: vp.StreamIndex(),
		}
		if p, ok := vp.(recordProvider); ok {
			vj.Record = b64(p.AVCDecoderConfRecordBytes())
		}
		if p, ok := vp.(spsProvider); ok {
			vj.SPS = b64(p.SPS())
		}
		if p, ok := vp.(ppsProvider); ok {
			vj.PPS = b64(p.PPS())
		}
		if p, ok := vp.(vpsProvider); ok {
			vj.VPS = b64(p.VPS())
		}
		pj.Video = vj
	}

	if ap := lastAudioParams; ap != nil {
		aj := &AudioParamsJSON{
			Codec:       ap.Type().String(),
			StreamIndex: ap.StreamIndex(),
		}
		if p, ok := ap.(configBytesProvider); ok {
			aj.Config = b64(p.MPEG4AudioConfigBytes())
		}
		pj.Audio = aj
	}

	if *split {
		// Write one parameters file per codec.
		if pj.Video != nil {
			path := splitPath(*paramsFile, pj.Video.Codec)
			if err := writeJSON(path, pj.Video); err != nil {
				logrus.Fatalf("Failed to write video parameters: %v", err)
			}
			logrus.Infof("Wrote video parameters to %s", path)
		}
		if pj.Audio != nil {
			path := splitPath(*paramsFile, pj.Audio.Codec)
			if err := writeJSON(path, pj.Audio); err != nil {
				logrus.Fatalf("Failed to write audio parameters: %v", err)
			}
			logrus.Infof("Wrote audio parameters to %s", path)
		}

		// Write one packets file per codec.
		for codec, pkts := range perCodec {
			path := splitPath(*packetsFile, codec)
			if err := writeJSON(path, PacketsJSON{Packets: pkts}); err != nil {
				logrus.Fatalf("Failed to write packets for %s: %v", codec, err)
			}
			logrus.Infof("Wrote %d %s packets to %s", len(pkts), codec, path)
		}
	} else {
		if err := writeJSON(*paramsFile, pj); err != nil {
			logrus.Fatalf("Failed to write parameters: %v", err)
		}
		logrus.Infof("Wrote codec parameters to %s", *paramsFile)

		if err := writeJSON(*packetsFile, PacketsJSON{Packets: captured}); err != nil {
			logrus.Fatalf("Failed to write packets: %v", err)
		}
		logrus.Infof("Wrote %d packets to %s", len(captured), *packetsFile)
	}
}

func b64(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
}

// splitPath derives a per-codec filename from a base path by inserting the
// codec name before the extension, e.g. "packets.json" → "packets_H264.json".
func splitPath(base, codec string) string {
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	return stem + "_" + codec + ext
}

func writeJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
