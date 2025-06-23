package gomedia

import (
	"image"
	"time"
)

// CodecParameters defines the interface for multimedia codec configuration.
type CodecParameters interface {
	Type() CodecType      // Returns the codec type (audio/video).
	Tag() string          // Returns the codec identifier string.
	StreamIndex() uint8   // Returns the index of the stream in a container.
	SetStreamIndex(uint8) // Sets the stream index value.
	Bitrate() uint        // Returns the codec's bitrate in bits per second.
	SetBitrate(uint)      // Sets the codec's target bitrate.
}

// VideoCodecParameters extends CodecParameters with video-specific properties.
type VideoCodecParameters interface {
	CodecParameters // Inherits all CodecParameters methods.
	Width() uint    // Returns the video frame width in pixels.
	Height() uint   // Returns the video frame height in pixels.
	FPS() uint      // Returns the video frame rate (frames per second).
}

// AudioCodecParameters extends CodecParameters with audio-specific properties.
type AudioCodecParameters interface {
	CodecParameters             // Inherits all CodecParameters methods.
	SampleRate() uint64         // Returns the audio sampling frequency in Hz.
	SampleFormat() SampleFormat // Returns the format of audio samples.
	Channels() uint8            // Returns the number of audio channels.
}

// CodecParametersPair bundles audio and video codec parameters for a multimedia stream.
type CodecParametersPair struct {
	URL string // The source URL of the multimedia stream.
	AudioCodecParameters
	VideoCodecParameters
}

// Packet defines the interface for multimedia data containers.
type Packet interface {
	Clone(copyData bool) Packet // Creates a packet copy, optionally copying the underlying data.
	URL() string                // Returns the source URL of the packet.
	SetURL(string)              // Sets the source URL for the packet.
	StreamIndex() uint8         // Returns the stream index this packet belongs to.
	SetStreamIndex(uint8)       // Sets the stream index for this packet.
	Timestamp() time.Duration   // Returns the presentation timestamp.
	SetTimestamp(time.Duration) // Sets the presentation timestamp.
	StartTime() time.Time       // Returns the absolute start time.
	SetStartTime(time.Time)     // Sets the absolute start time.
	Duration() time.Duration    // Returns the duration of the packet content.
	SetDuration(time.Duration)  // Sets the duration of the packet content.
	Data() []byte               // Returns the raw packet data.
}

// VideoPacket extends Packet with video-specific functionality.
type VideoPacket interface {
	Packet                                 // Inherits all Packet methods.
	IsKeyFrame() bool                      // Indicates if this packet contains a keyframe.
	CodecParameters() VideoCodecParameters // Returns the associated video codec configuration.
}

// AudioPacket extends Packet with audio-specific functionality.
type AudioPacket interface {
	Packet                                 // Inherits all Packet methods.
	CodecParameters() AudioCodecParameters // Returns the associated audio codec configuration.
}

// Demuxer defines the interface for extracting packets from multimedia containers.
type Demuxer interface {
	Demux() (CodecParametersPair, error) // Initializes and returns detected stream parameters.
	ReadPacket() (pkt Packet, err error) // Reads the next packet from the container.
	Close()                              // Releases resources used by the demuxer.
}

// Muxer defines the interface for packaging packets into multimedia containers.
type Muxer interface {
	Mux(CodecParametersPair) (err error) // Initializes the muxer with stream parameters.
	WritePacket(pkt Packet) (err error)  // Writes a packet to the container.
	Close()                              // Finalizes the container and releases resources.
}

// Reader defines the interface for multimedia stream reading operations.
type Reader interface {
	Read()                    // Starts the reading process.
	AddURL() chan<- string    // Channel to add new stream URLs.
	RemoveURL() chan<- string // Channel to remove stream URLs.
	Packets() <-chan Packet   // Channel providing read packets.
	Close()                   // Stops reading and releases resources.
}

// Writer defines the interface for multimedia stream writing operations.
type Writer interface {
	Write()                      // Starts the writing process.
	Packets() chan<- Packet      // Channel for packets to be written.
	RemoveSource() chan<- string // Channel to remove source streams.
	Done() <-chan struct{}       // Channel signaling completion.
	Close()                      // Stops writing and releases resources.
}

// Decoder defines a generic interface for decoding multimedia packets.
type Decoder[P Packet] interface {
	Decode()               // Starts the decoding process.
	Packets() chan<- P     // Channel for packets to be decoded.
	Close()                // Stops decoding and releases resources.
	Done() <-chan struct{} // Channel signaling completion.
}

// VideoDecoder specializes Decoder for video packet processing.
type VideoDecoder interface {
	Decoder[VideoPacket]        // Inherits Decoder methods for VideoPacket.
	Images() <-chan image.Image // Channel providing decoded video frames.
	FPS() chan<- int            // Channel to set frames per second.
}

// AudioDecoder specializes Decoder for audio packet processing.
type AudioDecoder interface {
	Decoder[AudioPacket]         // Inherits Decoder methods for AudioPacket.
	Samples() <-chan AudioPacket // Channel providing decoded audio samples.
}

// Encoder defines the interface for multimedia encoding operations.
type Encoder interface {
	Packets() <-chan Packet // Channel providing encoded packets.
}

// AudioEncoder specializes Encoder for audio encoding.
type AudioEncoder interface {
	Encoder                      // Inherits Encoder methods.
	Samples() chan<- AudioPacket // Channel for audio samples to encode.
	Encode()                     // Starts the encoding process.
}

// InputParameter defines flags for controlling media processing.
type InputParameter uint8

// Input parameter constants
const (
	NoVideo InputParameter = iota // Skip video processing.
	NoAudio                       // Skip audio processing.
)
