# rkmpp-rtsp-info

Parallel RTSP ingest + Rockchip MPP hardware decode with per-session frame counters, data-rate reporting, and a final summary. Used to validate RK3588-class hardware decode under load.

## Run

```bash
go run -tags rkmpp . -url rtsp://camera/stream -sessions 4 -frames 500 -timeout 60
```

Flags:

- `-url`        RTSP URL (required, or set `RTSP_URL`).
- `-sessions`   Number of parallel read+decode pipelines.
- `-frames`     Frames to decode per session before exiting (`0` = unlimited).
- `-timeout`    Hard timeout in seconds.
- `-verbose`    Debug-level logging.
- `-no-audio`   Skip the audio stream.

Build tag: `rkmpp`. Native deps: Rockchip MPP, `librga`.
