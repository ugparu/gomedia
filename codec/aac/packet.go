package aac

import (
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
)

// Packet stores raw aac data without adts headers.
type Packet struct {
	codec.AudioPacket[*CodecParameters]
}

func NewPacket(data []byte, ts time.Duration, url string,
	absTime time.Time, codecPar *CodecParameters, dur time.Duration) *Packet {
	buf := codec.GetMemBuffer()
	buf.SetData(data)
	return &Packet{
		AudioPacket: codec.AudioPacket[*CodecParameters]{
			BasePacket: codec.BasePacket[*CodecParameters]{
				Idx:          codecPar.StreamIndex(),
				RelativeTime: ts,
				Dur:          dur,
				InpURL:       url,
				Buffer:       buf,
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
