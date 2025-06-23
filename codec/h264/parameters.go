package h264

import (
	"errors"
	"fmt"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
	"github.com/ugparu/gomedia/utils/nal"
)

type CodecParameters struct {
	codec.BaseParameters
	Record     []byte
	RecordInfo AVCDecoderConfRecord
	SPSInfo    SPSInfo
}

func NewCodecDataFromSPSAndPPS(sps, pps []byte) (codecPar CodecParameters, err error) {
	recordinfo := AVCDecoderConfRecord{
		AVCProfileIndication: sps[1],
		ProfileCompatibility: sps[2],
		AVCLevelIndication:   sps[3],
		LengthSizeMinusOne:   nal.MinNaluSize - 1,
		SPS:                  [][]byte{sps},
		PPS:                  [][]byte{pps},
	}

	buf := make([]byte, recordinfo.Len())
	recordinfo.Marshal(buf)

	codecPar.RecordInfo = recordinfo
	codecPar.Record = buf
	codecPar.CodecType = gomedia.H264

	if codecPar.SPSInfo, err = parseSPS(sps); err != nil {
		return
	}
	// Calculate bitrate based on width, scale factor, and frame rate
	widthFactor := float64(codecPar.Width())
	fpsRatio := bitrateFrameRate / float64(codecPar.FPS())
	codecPar.BRate = uint(widthFactor*float64(bitrateScaleFactor)*fpsRatio) * bitrateMultiplier

	return
}

func NewCodecDataFromHevcDecoderConfRecord(record []byte) (codecPar CodecParameters, err error) {
	codecPar.Record = record
	if _, err = (&codecPar.RecordInfo).Unmarshal(record); err != nil {
		return
	}
	if len(codecPar.RecordInfo.SPS) == 0 {
		err = errors.New("h264parser: no SPS found in AVCDecoderConfRecord")
		return
	}
	if len(codecPar.RecordInfo.PPS) == 0 {
		err = errors.New("h264parser: no PPS found in AVCDecoderConfRecord")
		return
	}
	if codecPar.SPSInfo, err = parseSPS(codecPar.RecordInfo.SPS[0]); err != nil {
		err = fmt.Errorf("h264parser: parse SPS failed(%w)", err)
		return
	}

	codecPar.CodecType = gomedia.H264
	// Calculate bitrate based on width, scale factor, and frame rate
	widthFactor := float64(codecPar.Width())
	fpsRatio := bitrateFrameRate / float64(codecPar.FPS())
	codecPar.BRate = uint(widthFactor*float64(bitrateScaleFactor)*fpsRatio) * bitrateMultiplier

	return
}

var ErrDecconfInvalid = errors.New("h264parser: AVCDecoderConfRecord invalid")

func (par *CodecParameters) AVCDecoderConfRecordBytes() []byte {
	return par.Record
}

func (par *CodecParameters) SPS() []byte {
	return par.RecordInfo.SPS[0]
}

func (par *CodecParameters) PPS() []byte {
	return par.RecordInfo.PPS[0]
}

func (par *CodecParameters) Width() uint {
	return par.SPSInfo.Width
}

func (par *CodecParameters) Height() uint {
	return par.SPSInfo.Height
}

func (par *CodecParameters) FPS() uint {
	return par.SPSInfo.FPS
}

func (par *CodecParameters) Tag() string {
	return fmt.Sprintf("avc1.%02X%02X%02X",
		par.RecordInfo.AVCProfileIndication, par.RecordInfo.ProfileCompatibility, par.RecordInfo.AVCLevelIndication)
}
