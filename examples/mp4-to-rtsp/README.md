# mp4-to-rtsp

Demux an MP4 file and publish it to an RTSP server. The publish handshake (OPTIONS → ANNOUNCE → SETUP → RECORD) runs end-to-end; actual RTP packet transmission is currently a stub and returns `rtsp.ErrRTPMuxerNotImplemented`.

## Run

```bash
go run . input.mp4 rtsp://localhost:8554/test
```

Both positional arguments are optional. Defaults: `input.mp4` and `$RTSP_URL` (or `rtsp://localhost:8554/test`).
