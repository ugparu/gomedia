# rkmpp-rtsp-to-jpeg

RTSP → Rockchip MPP hardware decode → JPEG file. Reads packets via the high-level `reader.RTSP`, pushes them into a `decoder.NewVideo` backed by `rkmpp.NewFFmpegRKMPPDecoder`, then encodes the first decoded frame as JPEG.

## Run

```bash
go run -tags rkmpp . -url rtsp://camera/stream -output ./output.jpg
```

Flags:

- `-url`     RTSP URL (required, or set `RTSP_URL`).
- `-output`  Output JPEG path (default `./output.jpg`).

Build tag: `rkmpp`. Native deps: Rockchip MPP, `librga`.
