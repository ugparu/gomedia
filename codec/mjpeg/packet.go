package mjpeg

import (
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
)

// Packet represents an MJPEG video packet
type Packet struct {
	codec.VideoPacket[*CodecParameters]
}

// NewPacket creates a new MJPEG packet
func NewPacket(
	key bool,
	timestamp time.Duration,
	absTime time.Time,
	data []byte,
	url string,
	param *CodecParameters,
) *Packet {
	return &Packet{
		VideoPacket: codec.VideoPacket[*CodecParameters]{
			BasePacket: codec.BasePacket[*CodecParameters]{
				Idx:          param.StreamIndex(),
				RelativeTime: timestamp,
				Dur:          0,
				InpURL:       url,
				Buffer:       data,
				AbsoluteTime: absTime,
				CodecPar:     param,
			},
			IsKeyFrm: key,
		},
	}
}

// Clone creates a copy of the packet
func (pkt *Packet) Clone(copyData bool) gomedia.Packet {
	return &Packet{
		VideoPacket: pkt.VideoPacket.Clone(copyData),
	}
}
