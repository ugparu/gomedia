// Package gomedia is a toolkit for building real-time media pipelines in Go.
//
// The root package declares the interfaces that everything else implements:
// [CodecParameters] describe a stream, [Packet] carries a single encoded frame,
// [Demuxer] and [Muxer] handle container I/O, and [Reader] and [Writer] compose
// multi-source ingest and fan-out sinks on top of them. Concrete implementations
// live in sibling packages and never import each other — they only depend on the
// interfaces declared here.
//
// Packets are reference-counted. Clone(false) shares the backing buffer; every
// owner must call Release exactly once. Producers carve buffers from
// utils/buffer.RingAlloc (or GrowingRingAlloc); consumers receive a SlotHandle
// via the clone and release it when done.
//
// Several subpackages require CGo and external libraries:
//   - decoder/aac     — libfdk-aac
//   - decoder/opus    — libopus, libopusfile
//   - decoder/video/rkmpp — Rockchip MPP + librga (build tag: rkmpp)
//   - decoder/video/cuda  — NVIDIA Video Codec SDK (build tag: cuda)
//
// See the examples/ directory for end-to-end pipelines (RTSP→MP4, RTSP→HLS,
// RTSP→WebRTC, MP4→JPEG, and others).
package gomedia
