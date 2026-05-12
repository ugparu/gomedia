# mjpeg-rtsp-to-jpeg

Save the first 10 frames of an MJPEG RTSP source as `frame_000.jpg` … `frame_009.jpg`. MJPEG packets already contain a complete JPEG image, so no decoder is involved — `pkt.Data()` is written directly to disk.

## Run

```bash
RTSP_URL=rtsp://camera/mjpeg go run .
```

The program will refuse to continue if the stream is not MJPEG.
