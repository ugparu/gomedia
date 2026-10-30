# Changelog

All notable changes to this project are documented here. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.25] - 2026-10-30

### Fixed

- MP4 muxer no longer locks a half-period frame grid when SPS/VUI has no
  timing info: boot from the median of the first gaps, and promote the
  nominal period if several consecutive gaps look like 2×avg. Stops 15 fps
  Trassir subs from writing ~6 s archive segments for ~11 s wall (160 frames
  at ~33 ms stts instead of ~67 ms).

## [1.0.24] - 2026-10-30

### Fixed

- H.264 SPS VUI FPS no longer divides by 2 a second time when
  `fixed_frame_rate_flag=1`. That double-halve reported 12 fps for streams
  that signal 25 (`time_scale=50000`, `num_units_in_tick=1000`), which seeded
  the MP4 muxer stts grid at ~83 ms and roughly doubled archive segment
  duration while wall-clock/DB time stayed correct.

## [1.0.23] - 2026-10-28

### Fixed

- Segmenter keeps a sticky PathFunc stream index per source URL across
  RemoveSource/AddSource, so offline timeout of the main stream can no longer
  renumber the sub stream onto `0_*.mp4` (and hand `1_*.mp4` to the recovered
  main).
- RTSP publisher waits for a video keyframe before announcing/writing, so
  MediaMTX republish after start/reconnect does not open mid-GOP (green frames).

## [1.0.21] - 2026-10-16

### Fixed

- MP4 muxer snaps jittery video timestamp gaps onto the nominal frame grid
  before writing stts, so smart-codec RTP wobble (20/60ms pairs around 40ms)
  no longer produces tempo jumps on archive playback. Dropped frames become a
  clean 2× hold; reconnect/outage clamps are unchanged.

## [1.0.20] - 2026-10-14

### Fixed

- WebRTC peer writer no longer accumulates multi-second latency under CPU load:
  when behind the delay target it stops sleeping (soft catch-up), and only if the
  peer queue keeps growing past a threshold it force-skips to a keyframe.
