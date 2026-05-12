# mp4-to-mp4

Remux an MP4 file: demux `input.mp4`, write every packet through the MP4 muxer to `output.mp4`. No transcoding.

Useful for sanity-checking the demuxer/muxer round-trip and for fixing slightly malformed MP4s.

## Run

```bash
go run .
```

Input/output filenames are hardcoded as `input.mp4` and `output.mp4`.
