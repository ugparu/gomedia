package aac

import (
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
)

// Packet carries a single AAC access unit (raw AudioMuxElement payload,
// no ADTS header). Decoder configuration travels on the CodecParameters side.
type Packet struct {
	codec.AudioPacket[*CodecParameters]
}

func NewPacket(data []byte, ts time.Duration, url string,
	absTime time.Time, codecPar *CodecParameters, dur time.Duration) *Packet {
	return &Packet{
		AudioPacket: codec.AudioPacket[*CodecParameters]{
			BasePacket: codec.NewBasePacket(
				codecPar.StreamIndex(),
				ts,
				dur,
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
