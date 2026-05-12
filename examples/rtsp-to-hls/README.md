# rtsp-to-hls

Stream one or more RTSP sources as Low-Latency HLS (LL-HLS) over HTTP on port `8080`. AAC audio is forwarded directly; non-AAC audio is decoded and re-encoded to AAC before being added to the HLS stream.

## Run

```bash
RTSP_URLS="rtsp://cam1/stream,rtsp://cam2/stream" go run .
```

`RTSP_URLS` is a comma-separated list. The player is available at `http://localhost:8080/`.

Native deps: `libfdk-aac-dev`, `libopus-dev`, `libopusfile-dev`.
