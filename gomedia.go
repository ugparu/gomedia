package gomedia

//go:generate mockgen -source=gomedia.go -destination=mocks/mock_gomedia.go -package=mocks

import (
	"context"
	"time"

	"github.com/ugparu/gomedia/frame/rgb"
)

// CodecParameters describes a stream (codec type, container tag, bitrate, position).
type CodecParameters interface {
	Type() CodecType
	Tag() string
	StreamIndex() uint8
	SetStreamIndex(uint8)
	Bitrate() uint
	SetBitrate(uint)
}

// VideoCodecParameters adds video-specific fields: pixel dimensions and frame rate.
type VideoCodecParameters interface {
	CodecParameters
	Width() uint
	Height() uint
	FPS() uint
}

// AudioCodecParameters adds audio-specific fields: sample rate, sample format, channel count.
type AudioCodecParameters interface {
	CodecParameters
	SampleRate() uint64
	SampleFormat() SampleFormat
	Channels() uint8
}

// CodecParametersPair bundles the audio and video parameters of a single source.
type CodecParametersPair struct {
	SourceID string
	AudioCodecParameters
	VideoCodecParameters
}

// Packet carries one encoded frame. Packets are reference-counted: Clone(false)
// shares the backing buffer, Clone(true) deep-copies. Every owner — original and
// each clone — must call Release exactly once.
type Packet interface {
	// Clone returns a new owner of the packet. If copyData is false the new
	// owner shares the underlying buffer (refcount++); if true the data is
	// heap-copied and the clone is fully independent.
	Clone(copyData bool) Packet
	// Release drops one reference. When the count reaches zero the backing
	// buffer is returned to the ring allocator (or becomes GC-eligible for
	// heap-backed packets). Must be called exactly once per owner.
	Release()
	SourceID() string
	SetSourceID(string)
	StreamIndex() uint8
	SetStreamIndex(uint8)
	Timestamp() time.Duration
	SetTimestamp(time.Duration)
	StartTime() time.Time
	SetStartTime(time.Time)
	Duration() time.Duration
	SetDuration(time.Duration)
	Len() int
	Data() []byte
}

// VideoPacket is a Packet that carries a video frame.
type VideoPacket interface {
	Packet
	IsKeyFrame() bool
	CodecParameters() VideoCodecParameters
}

// AudioPacket is a Packet that carries an audio frame.
type AudioPacket interface {
	Packet
	CodecParameters() AudioCodecParameters
}

// Demuxer reads a container and emits packets. ReadPacket may return (nil, nil)
// for filler/AUD packets and other no-ops the caller should skip.
type Demuxer interface {
	Demux() (CodecParametersPair, error)
	ReadPacket() (pkt Packet, err error)
	Close()
}

// Muxer consumes packets and writes a container. Mux must be called once with
// the detected parameters before the first WritePacket.
type Muxer interface {
	Mux(CodecParametersPair) (err error)
	WritePacket(pkt Packet) (err error)
	Close()
}

// Reader is a high-level multi-source ingest. Add and remove sources via the
// AddURL/RemoveURL channels; consume packets from Packets().
type Reader interface {
	Read()
	AddURL() chan<- string
	RemoveURL() chan<- string
	Packets() <-chan Packet
	Close()
}

// Writer is a high-level fan-out sink. Feed packets via Packets(); attach and
// detach source URLs through AddSource/RemoveSource. Done closes when Write
// terminates.
type Writer interface {
	Write()
	Packets() chan<- Packet
	RemoveSource() chan<- string
	AddSource() chan<- string
	Done() <-chan struct{}
	Close()
}

// Decoder is the generic async decode pipeline: feed encoded packets in,
// receive decoded output from a codec-specific channel exposed by the
// concrete VideoDecoder / AudioDecoder.
type Decoder[P Packet] interface {
	Decode()
	Packets() chan<- P
	Close()
	Done() <-chan struct{}
}

// VideoDecoder decodes VideoPacket frames into RGB images.
// FPS is a throttling signal: send 0 to pause, -1 for native rate.
type VideoDecoder interface {
	Decoder[VideoPacket]
	Images() <-chan rgb.ReleasableImage
	FPS() chan<- int
}

// AudioDecoder decodes AudioPacket frames into PCM samples.
type AudioDecoder interface {
	Decoder[AudioPacket]
	Samples() <-chan AudioPacket
}

// Encoder produces encoded packets on its Packets() channel.
type Encoder interface {
	Packets() <-chan Packet
}

// AudioEncoder consumes PCM samples on Samples() and produces encoded
// AudioPackets on Packets() (inherited from Encoder).
type AudioEncoder interface {
	Encoder
	Samples() chan<- AudioPacket
	Encode()
}

// HLSMuxer is a single-source HLS muxer. It rotates segments on
// segmentDuration and exposes the playlist/segment/fragment bytes via the
// Get* methods. UpdateCodecParameters injects an HLS discontinuity.
type HLSMuxer interface {
	Muxer
	UpdateCodecParameters(CodecParametersPair) error
	GetMasterEntry() (string, error)
	GetIndexM3u8(ctx context.Context, msn int64, prt int8) (string, error)
	GetInit() ([]byte, error)
	GetInitByVersion(version int) ([]byte, error)
	GetSegment(ctx context.Context, seg uint64) ([]byte, error)
	GetFragment(ctx context.Context, seg uint64, frag uint8) ([]byte, error)
}

// HLS is the read-side interface of a multi-source HLS streamer: it serves a
// master playlist plus per-source playlists, init segments, segments, and
// fragments. uid identifies the source.
type HLS interface {
	GetMasterPlaylist() (string, error)
	GetIndexM3u8(ctx context.Context, uid string, msn int64, prt int8) (string, error)
	GetInit(uid string) ([]byte, error)
	GetInitByVersion(uid string, version int) ([]byte, error)
	GetSegment(ctx context.Context, uid string, seg uint64) ([]byte, error)
	GetFragment(ctx context.Context, uid string, seg uint64, frag uint8) ([]byte, error)
}

// HLSStreamer is a multi-source HLS pipeline: Writer for ingest, HLS for read.
type HLSStreamer interface {
	Writer
	HLS
}

// WebRTCCodec is the JSON payload returned to clients describing the available
// video resolutions and whether an audio track is present.
type WebRTCCodec struct {
	HasAudio    bool         `json:"hasAudio"`
	Resolutions []Resolution `json:"resolutions"`
}

// Resolution is one selectable video rendition exposed to a WebRTC client.
type Resolution struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Codec  string `json:"codec"`
}

// WebRTCPeer is an SDP offer/answer exchange. The caller fills SDP+TargetURL,
// the streamer fills SDP (answer) and Err, then closes Done.
type WebRTCPeer struct {
	SDP       string
	TargetURL string
	Delay     int
	Err       error
	Done      chan struct{}
}

// WebRTC is the read-side interface of a WebRTC streamer: peer negotiation
// plus the list of available codec/resolution pairs.
type WebRTC interface {
	Peers() chan<- *WebRTCPeer
	SortedResolutions() *WebRTCCodec
}

// WebRTCStreamer is a multi-source WebRTC pipeline: Writer for ingest, WebRTC
// for client negotiation.
type WebRTCStreamer interface {
	Writer
	WebRTC
}

// RecordMode controls when the segmenter writes packets to disk.
type RecordMode uint8

const (
	Never  RecordMode = iota // never record
	Event                    // record only while an event is active
	Always                   // record continuously
)

// FileInfo describes one closed segment file produced by a Segmenter.
type FileInfo struct {
	Name       string
	Start      time.Time
	Stop       time.Time
	Size       int
	Resolution string
	URL        string
	Codec      string
}

// Segmenter records sources into rolling MP4 files. Events triggers an
// event-mode capture; RecordMode swaps the mode at runtime; Files yields
// FileInfo for every closed segment.
type Segmenter interface {
	Writer
	Events() chan<- struct{}
	RecordMode() chan<- RecordMode
	Files() <-chan FileInfo
	RecordCurStatus() <-chan bool
}
