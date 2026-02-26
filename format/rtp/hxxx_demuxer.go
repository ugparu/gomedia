package rtp

import (
	"errors"
	"io"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/utils/nal"
	"github.com/ugparu/gomedia/utils/sdp"
)

type hxxxDemuxer struct {
	*baseDemuxer
	nals [][]byte
}

func newHxxxDemuxer(rdr io.Reader, sdp sdp.Media, index uint8, options ...DemuxerOption) *hxxxDemuxer {
	bd := newBaseDemuxer(rdr, sdp, index, options...)
	return &hxxxDemuxer{
		baseDemuxer: bd,
		nals:        nil,
	}
}

func (d *hxxxDemuxer) ReadPacket() (pkt gomedia.Packet, err error) {
	if _, err = d.baseDemuxer.ReadPacket(); err != nil {
		return
	}

	d.nals, _ = nal.SplitNALUs(d.payload.Data()[d.offset:d.end])
	if len(d.nals) == 0 || len(d.nals[0]) == 0 {
		err = errors.New("empty nal unit")
		return
	}

	return
}
