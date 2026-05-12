# log-rtsp-packets

Open an RTSP demuxer and read packets in a tight loop indefinitely. Useful for stress-testing the RTSP client and the packet allocator.

## Run

```bash
RTSP_URL=rtsp://camera/stream go run .
```

The program never exits — terminate with `Ctrl+C`.
