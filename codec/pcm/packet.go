package pcm

import (
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
)

type Packet struct {
	codec.AudioPacket[*CodecParameters]
}

func NewPacket(data []byte, ts time.Duration, url string,
	absTime time.Time, codecPar *CodecParameters, duration time.Duration) *Packet {
	return &Packet{
		AudioPacket: codec.AudioPacket[*CodecParameters]{
			BasePacket: codec.BasePacket[*CodecParameters]{
				Idx:          codecPar.StreamIndex(),
				RelativeTime: ts,
				Dur:          duration,
				InpURL:       url,
				Buffer:       data,
				AbsoluteTime: absTime,
				CodecPar:     codecPar,
			},
		},
	}
}

func (p *Packet) Clone(copyData bool) gomedia.Packet {
	return &Packet{
		AudioPacket: p.AudioPacket.Clone(copyData),
	}
}
