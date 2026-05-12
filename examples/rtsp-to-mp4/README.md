# rtsp-to-mp4

Minimal end-to-end pipeline: read 100 packets from an RTSP source and write them to `output.mp4`. This is the simplest gomedia example — useful as a starting template.

## Run

```bash
RTSP_URL=rtsp://camera/stream go run .
```

Output: `output.mp4` in the current directory.
