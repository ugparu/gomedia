# gomedia

gomedia is a Go toolkit for building real‑time media pipelines. It provides reusable codecs, demuxers, muxers, decoders, encoders, and streaming adapters so you can ingest sources like RTSP or MP4, process audio/video, and serve them as HLS, WebRTC, or archived files. The primary goal is to give developers a modular, end‑to‑end foundation for camera ingest, live streaming, and recording workflows without having to wire the low‑level media plumbing by hand.

## Supported formats and codecs

- Ingest (demuxers):
  - [x] RTSP
  - [-] RTP:
    - [x] H264/AVC
    - [x] H265/HEVC
    - [ ] MJPEG
    - [x] AAC
    - [x] Opus
    - [x] PCM
  - [x] MP4
- Output (muxers/streamers):
  - [x] MP4
  - [x] Fragmented MP4 (fMP4)
  - [x] HLS (single + multi-variant)
  - [x] WebRTC
  - [x] Archive segmenter/recorder
- Codecs:
  - [-] Video:
    - [x] H264/AVC
    - [x] H265/HEVC
    - [ ] MJPEG
  - [x] Audio:
    - [x] AAC
    - [x] Opus
    - [x] PCM (A-Law/μ-Law, linear PCM)

