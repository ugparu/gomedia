# gomedia

[![Go Reference](https://pkg.go.dev/badge/github.com/ugparu/gomedia.svg)](https://pkg.go.dev/github.com/ugparu/gomedia)
[![Go Report Card](https://goreportcard.com/badge/github.com/ugparu/gomedia)](https://goreportcard.com/report/github.com/ugparu/gomedia)
[![License](https://img.shields.io/github/license/ugparu/gomedia)](https://github.com/ugparu/gomedia/blob/master/LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/ugparu/gomedia)](https://github.com/ugparu/gomedia/go.mod)
[![Coverage](https://img.shields.io/endpoint?url=https://gist.githubusercontent.com/z1rachl/153dd1a7663fdc8715570f29d314a6fd/raw/gomedia-coverage.json)](https://github.com/ugparu/gomedia/actions/workflows/ci.yml)

`gomedia` is a Go toolkit for building real-time media pipelines: ingest RTSP or MP4, run codec, transcode, and stream the result as HLS, WebRTC, or recorded archive files. It targets the camera-ingest / live-streaming / recording use case with a small set of composable interfaces and zero hidden allocation on the hot path.

## Install

```bash
go get github.com/ugparu/gomedia
```

The pure-Go parts of the library compile with stock `go build`. Hardware-accelerated decoders and the libfdk-aac / libopus bindings need a C toolchain and native libraries — see [Build prerequisites](#build-prerequisites).

## Quick start

Record the first 100 packets from an RTSP source into an MP4 file:

```go
package main

import (
	"log"
	"os"

	"github.com/ugparu/gomedia/format/mp4"
	"github.com/ugparu/gomedia/format/rtsp"
)

func main() {
	dmx := rtsp.New(os.Getenv("RTSP_URL"))
	params, err := dmx.Demux()
	if err != nil {
		log.Fatal(err)
	}

	out, err := os.Create("output.mp4")
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	mx := mp4.NewMuxer(out)
	if err := mx.Mux(params); err != nil {
		log.Fatal(err)
	}

	for i := 0; i < 100; i++ {
		pkt, err := dmx.ReadPacket()
		if err != nil {
			log.Fatal(err)
		}
		if pkt == nil {
			continue
		}
		if err := mx.WritePacket(pkt); err != nil {
			log.Fatal(err)
		}
	}
	if err := mx.WriteTrailer(); err != nil {
		log.Fatal(err)
	}
}
```

More end-to-end examples live under [`examples/`](examples/): RTSP→HLS, RTSP→WebRTC, MP4→JPEG, fan-out to several sinks, hardware-accelerated decoding, and so on.

## Architecture

Data flows in one direction through small interfaces declared in the root package:

```
CodecParameters ──┐                                    ┌── HLS / WebRTC / MP4
                  ├── Demuxer ── Packet ── Muxer ──────┤
   raw stream ────┘            (ref-counted)           └── archive segmenter
```

- **`CodecParameters`** describe a stream. Implementations live in `codec/{aac,h264,h265,opus,pcm,mjpeg}`.
- **`Packet`** carries a single encoded frame. Packets are reference-counted: `Clone(false)` shares the backing buffer, `Clone(true)` deep-copies. Every owner calls `Release` exactly once.
- **`Demuxer`** reads a container (RTSP, MP4) and emits packets.
- **`Muxer`** consumes packets and writes a container (MP4, fMP4, HLS, WebRTC).
- **`Reader`** / **`Writer`** compose multi-source ingest and fan-out sinks on top of demuxers/muxers, with reconnect logic and lifecycle management.
- **`AudioDecoder`** / **`VideoDecoder`** wrap async decode pipelines; the inner codec is plugged in via a factory map.

Hot-path memory comes from `utils/buffer.RingAlloc` (a single-producer slab allocator for packet data) and `utils/buffer.PooledBuffer` (a `sync.Pool` for transient scratch buffers). `make([]byte, n)` is forbidden on per-frame paths.

Async components embed one of three lifecycle managers from `utils/lifecycle` — strict, fail-safe, or synchronous — and implement a single `Step(stopCh)` method.

## Build prerequisites

The pure-Go subset (codec parsers, format I/O, RTSP/MP4 demuxers, HLS/MP4 muxers, WebRTC streamer) needs only the Go toolchain.

CGo subpackages need additional native libraries:

| Package                  | Build tag | Native dependency                                |
|--------------------------|-----------|--------------------------------------------------|
| `decoder/aac`            | —         | `libfdk-aac-dev`                                 |
| `decoder/opus`           | —         | `libopus-dev`, `libopusfile-dev`                 |
| `decoder/video/cpu`      | —         | `libavcodec-dev`, `libswscale-dev`, `libavutil-dev` |
| `decoder/video/rkmpp`    | `rkmpp`   | Rockchip MPP, `librga`                           |
| `decoder/video/cuda`     | `cuda`    | NVIDIA Video Codec SDK                           |

On Debian / Ubuntu:

```bash
sudo apt-get install -y \
  libfdk-aac-dev libopus-dev libopusfile-dev \
  libavcodec-dev libavutil-dev libswscale-dev libavformat-dev libswresample-dev
```

## Supported formats and codecs

- Ingest (demuxers):
  - [x] RTSP
  - [-] RTP:
    - [x] H264/AVC
    - [x] H265/HEVC
    - [ ] MJPEG
    - [x] AAC
    - [x] Opus
    - [x] PCM
  - [x] MP4
- Output (muxers/streamers):
  - [x] MP4
  - [x] Fragmented MP4 (fMP4)
  - [x] HLS (single + multi-variant)
  - [x] WebRTC
  - [x] Archive segmenter/recorder
- Codecs:
  - [-] Video:
    - [x] H264/AVC
    - [x] H265/HEVC
    - [ ] MJPEG
  - [x] Audio:
    - [x] AAC
    - [x] Opus
    - [x] PCM (A-Law/μ-Law, linear PCM)

## Development

```bash
make test      # go test ./...
make lint      # golangci-lint run ./...
make cover     # coverage profile + summary
make generate  # regenerate mocks
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full developer guide and [SECURITY.md](SECURITY.md) to report a vulnerability.

## License

MIT — see [LICENSE](LICENSE).
