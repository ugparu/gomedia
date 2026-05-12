# rtsp-transcode-audio-to-mp4

Read RTSP, transcode audio (PCM A-law / μ-law / AAC) through `decoder.NewAudioDecoder` and `encoder.NewAudioEncoder` (AAC), then write the first 100 mixed audio/video packets to `output.mp4` so the resulting file has AAC audio regardless of what the source emitted.

## Run

```bash
RTSP_URL=rtsp://camera/stream go run .
```

Output: `output.mp4` (H.264/H.265 video passthrough + AAC audio).

Native deps: `libfdk-aac-dev`.
