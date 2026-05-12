# aac-to-alaw

Decode AAC audio from an RTSP source, re-encode it as PCM A-law, and write the first 100 packets to `output.pcm`.

The pipeline: `reader.RTSP` → `decoder.NewAudioDecoder` (AAC) → `encoder.NewAudioEncoder` (A-law) → file.

## Run

```bash
RTSP_URL=rtsp://camera/stream go run .
```

Native deps: `libfdk-aac-dev`.

Output: `output.pcm` (raw 8-bit A-law samples).
