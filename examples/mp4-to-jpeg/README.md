# mp4-to-jpeg

Demux `inp0.mp4`, decode the first frame via the CPU/FFmpeg decoder, and save it as `output.jpg`.

## Run

```bash
go run .
```

The input filename is hardcoded — drop your test file as `inp0.mp4` in the current directory.

Native deps: `libavcodec-dev`, `libavutil-dev`, `libswscale-dev`.
