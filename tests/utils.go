// Package tests provides utilities for loading captured RTSP test data from
// JSON files produced by the rtsp-to-json example tool.
package tests

// ---------------------------------------------------------------------------
// JSON schema (mirrors examples/rtsp-to-json output)
// ---------------------------------------------------------------------------

// ParametersJSON is the top-level parameters file structure.
type ParametersJSON struct {
	URL   string           `json:"url"`
	Video *VideoParamsJSON `json:"video,omitempty"`
	Audio *AudioParamsJSON `json:"audio,omitempty"`
}

// VideoParamsJSON holds video codec parameters.
type VideoParamsJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	Record      string `json:"record,omitempty"` // base64
	SPS         string `json:"sps,omitempty"`    // base64
	PPS         string `json:"pps,omitempty"`    // base64
	VPS         string `json:"vps,omitempty"`    // base64
}

// AudioParamsJSON holds audio codec parameters.
type AudioParamsJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	Config      string `json:"config,omitempty"` // base64
}

// PacketsJSON is the top-level packets file structure.
type PacketsJSON struct {
	Packets []PacketJSON `json:"packets"`
}

// PacketJSON represents a single captured packet.
type PacketJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	TimestampNs int64  `json:"timestamp_ns"`
	DurationNs  int64  `json:"duration_ns"`
	IsKeyframe  bool   `json:"is_keyframe,omitempty"`
	Size        int    `json:"size"`
	Data        string `json:"data"` // base64
}
