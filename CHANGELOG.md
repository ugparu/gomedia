# Changelog

All notable changes to this project are documented here. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.20] - 2026-10-14

### Fixed

- WebRTC peer writer no longer accumulates multi-second latency under CPU load:
  when behind the delay target it stops sleeping (soft catch-up), and only if the
  peer queue keeps growing past a threshold it force-skips to a keyframe.
