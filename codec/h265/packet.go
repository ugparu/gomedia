package h265

import (
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
)

type Packet struct {
	codec.VideoPacket[*CodecParameters]
}

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
			BasePacket: codec.NewBasePacket(
				param.StreamIndex(),
				timestamp,
				0,
				url,
				data,
				absTime,
				param,
			),
			IsKeyFrm: key,
		},
	}
}

func (pkt *Packet) Clone(copyData bool) gomedia.Packet {
	return &Packet{
		VideoPacket: pkt.VideoPacket.Clone(copyData),
	}
}
