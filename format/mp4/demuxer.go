// Package mp4 provides functionality for working with MP4 files, specifically for demuxing video streams.
package mp4

import (
	"errors"
	"io"
	"os"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/codec/h265"
	"github.com/ugparu/gomedia/format/mp4/mp4io"
)

type Demuxer struct {
	r              *os.File
	streams        []*Stream
	movieAtom      *mp4io.Movie
	url            string
	videoCodecData gomedia.VideoCodecParameters
	audioCodecData gomedia.AudioCodecParameters
}

func NewDemuxer(url string) gomedia.Demuxer {
	dmx := new(Demuxer)
	dmx.url = url
	return dmx
}

func (dmx *Demuxer) Demux() (params gomedia.CodecParametersPair, err error) {
	dmx.r, err = os.Open(dmx.url)
	if err != nil {
		return
	}
	if err = dmx.probe(); err != nil {
		return
	}

	params.URL = dmx.url
	params.VideoCodecParameters = dmx.videoCodecData
	params.AudioCodecParameters = dmx.audioCodecData

	return params, nil
}

func (dmx *Demuxer) Close() {
	dmx.r.Close()
}

func (dmx *Demuxer) ReadPacket() (pkt gomedia.Packet, err error) {
	if err = dmx.probe(); err != nil {
		return
	}
	if len(dmx.streams) == 0 {
		err = errors.New("mp4: no streams available while trying to read a packet")
		return
	}

	var chosen *Stream
	for _, stream := range dmx.streams {
		if chosen == nil || stream.tsToTime(stream.dts) < chosen.tsToTime(chosen.dts) {
			chosen = stream
		}
	}

	tm := chosen.tsToTime(chosen.dts)
	return chosen.readPacket(tm, dmx.url)
}

// VideoParameters returns the video codec parameters associated with the demuxer.
func (dmx *Demuxer) VideoParameters() gomedia.VideoCodecParameters {
	return dmx.videoCodecData
}

// AudioParameters returns the audio codec parameters associated with the demuxer.
func (dmx *Demuxer) AudioParameters() gomedia.AudioCodecParameters {
	return dmx.audioCodecData
}

func (dmx *Demuxer) readat(pos int64, b []byte) (err error) {
	if _, err = dmx.r.Seek(pos, 0); err != nil {
		return
	}
	if _, err = io.ReadFull(dmx.r, b); err != nil {
		return
	}
	return
}

func (dmx *Demuxer) probe() (err error) {
	if dmx.movieAtom != nil {
		return
	}

	var moov *mp4io.Movie
	var atoms []mp4io.Atom

	if atoms, err = mp4io.ReadFileAtoms(dmx.r); err != nil {
		return
	}
	if _, err = dmx.r.Seek(0, 0); err != nil {
		return
	}

	for _, atom := range atoms {
		if atom.Tag() == mp4io.MOOV {
			moov, _ = atom.(*mp4io.Movie)
		}
	}

	if moov == nil {
		err = errors.New("mp4: 'moov' atom not found")
		return
	}

	dmx.streams = []*Stream{}
	for i, atrack := range moov.Tracks {
		if atrack.Media.Info.Sample.SyncSample != nil && len(atrack.Media.Info.Sample.SyncSample.Entries) == 0 {
			atrack.Media.Info.Sample.SyncSample = nil
		}

		stream := new(Stream)
		stream.trackAtom = atrack
		stream.index = i
		stream.demuxer = dmx

		if atrack.Media != nil && atrack.Media.Info != nil && atrack.Media.Info.Sample != nil {
			stream.sample = atrack.Media.Info.Sample
			stream.timeScale = int64(atrack.Media.Header.TimeScale)
		} else {
			err = errors.New("mp4: sample table not found")
			return
		}

		if avc1 := atrack.GetAVC1Conf(); avc1 != nil {
			var res h264.CodecParameters
			if res, err = h264.NewCodecDataFromHevcDecoderConfRecord(avc1.Data); err != nil {
				return err
			}
			res.SetStreamIndex(uint8(i)) //nolint:gosec
			stream.CodecParameters = &res
			dmx.videoCodecData = &res
			dmx.streams = append(dmx.streams, stream)
			continue
		}
		if hv1 := atrack.GetHV1Conf(); hv1 != nil {
			var res h265.CodecParameters
			if res, err = h265.NewCodecDataFromAVCDecoderConfRecord(hv1.Data); err != nil {
				return err
			}
			res.SetStreamIndex(uint8(i)) //nolint:gosec
			stream.CodecParameters = &res
			dmx.videoCodecData = &res
			dmx.streams = append(dmx.streams, stream)
			continue
		}
		if esds := atrack.GetElemStreamDesc(); esds != nil {
			var res aac.CodecParameters
			if res, err = aac.NewCodecDataFromMPEG4AudioConfigBytes(esds.DecConfig); err != nil {
				return
			}
			res.SetStreamIndex(uint8(i)) //nolint:gosec

			stream.CodecParameters = &res
			dmx.audioCodecData = &res
			dmx.streams = append(dmx.streams, stream)
			continue
		}
	}

	dmx.movieAtom = moov
	return
}
