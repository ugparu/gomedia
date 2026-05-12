# rtsp-to-all

Fan-out demo: one or more RTSP sources are decoded and re-encoded as needed, then pushed simultaneously to:

- a Low-Latency HLS server (`writer/hls`),
- a WebRTC streamer (`writer/webrtc`),
- an MP4 archive segmenter (`writer/segmenter`).

A Gin HTTP server on port `8080` exposes HLS playlists/segments, an SDP offer/answer endpoint, and a static `index.html` player.

Audio is transcoded so each sink receives the codec it expects (AAC for HLS, PCM A-law for WebRTC).

## Run

```bash
RTSP_URLS="rtsp://cam1/stream,rtsp://cam2/stream" go run .
```

`RTSP_URLS` is a comma-separated list. Recordings land under `./recordings/`. Visit `http://localhost:8080/` for the player.

Native deps: `libfdk-aac-dev`, `libopus-dev`, `libopusfile-dev`.
