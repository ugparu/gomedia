# rtsp-to-rtsp

Relay video packets from one RTSP source to another RTSP server using the high-level `reader.RTSP` ingest and the `writer/rtsp` publisher.

## Run

```bash
go run . rtsp://source/stream rtsp://destination/relay
```

Positional arguments are optional; fallbacks are `$SRC_RTSP_URL` / `$DST_RTSP_URL`, then `rtsp://localhost:8554/src` and `rtsp://localhost:8554/dst`.

Only video packets are forwarded in this example.
