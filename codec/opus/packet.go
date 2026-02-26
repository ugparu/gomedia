package opus

import (
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
)

type Packet struct {
	codec.AudioPacket[*CodecParameters]
}

// NewPacket creates a new Opus packet with the given parameters
func NewPacket(
	data []byte,
	ts time.Duration,
	url string,
	absTime time.Time,
	codecPar *CodecParameters,
	duration time.Duration,
) *Packet {
	return &Packet{
		AudioPacket: codec.AudioPacket[*CodecParameters]{
			BasePacket: codec.NewBasePacket(
				codecPar.StreamIndex(),
				ts,
				duration,
				url,
				data,
				absTime,
				codecPar,
			),
		},
	}
}

func (p *Packet) Clone(copyData bool) gomedia.Packet {
	return &Packet{
		AudioPacket: p.AudioPacket.Clone(copyData),
	}
}
