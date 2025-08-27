# gomedia

A Go library for media processing and streaming, supporting popular codecs and formats.

## Features

- **Codecs**: H.264, H.265, AAC, Opus, MJPEG, PCM
- **Formats**: MP4, FMP4, HLS, RTP, RTSP  
- **Streaming**: RTSP to WebRTC, HLS, MP4
- **Hardware acceleration**: CUDA support for video decoding

## Installation

```bash
go get github.com/ugparu/gomedia
```

## Quick Start

Convert RTSP stream to MP4:

```go
package main

import (
    "os"
    "github.com/ugparu/gomedia/format/mp4"
    "github.com/ugparu/gomedia/format/rtsp"
)

func main() {
    // Connect to RTSP stream
    dmx := rtsp.New("rtsp://example.com/stream")
    par, _ := dmx.Demux()
    
    // Create MP4 output
    f, _ := os.Create("output.mp4")
    defer f.Close()
    
    mx := mp4.NewMuxer(f)
    mx.Mux(par)
}
```

## Examples

Check the `examples/` directory for more usage patterns:
- RTSP to MP4/HLS/WebRTC
- MP4 processing and merging
- Audio transcoding

## License

See LICENSE file for details.