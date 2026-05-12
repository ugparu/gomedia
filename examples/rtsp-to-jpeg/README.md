# rtsp-to-jpeg

RTSP → CUDA hardware decode → JPEG file. Initializes the CUDA decoder pool, reads packets via `reader.RTSP`, feeds them into a `decoder.NewVideo` backed by `cuda.NewFFmpegCUDADecoder`, then JPEG-encodes the first decoded frame.

## Run

```bash
RTSP_URL=rtsp://camera/stream go run -tags cuda .
```

Build tag: `cuda`. Native deps: NVIDIA Video Codec SDK, `libavcodec-dev`, `libavutil-dev`, `libswscale-dev`.

The program assumes a working CUDA installation under `/usr/local/cuda/`.
