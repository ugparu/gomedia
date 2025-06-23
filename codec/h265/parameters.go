package h265

import (
	"errors"
	"fmt"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
)

type CodecParameters struct {
	codec.BaseParameters
	Record     []byte
	RecordInfo HEVCDecoderConfRecord
	SPSInfo    SPSInfo
}

func NewCodecDataFromAVCDecoderConfRecord(record []byte) (codecPar CodecParameters, err error) {
	codecPar.Record = record
	if _, err = (&codecPar.RecordInfo).Unmarshal(record); err != nil {
		return
	}
	if len(codecPar.RecordInfo.SPS) == 0 {
		err = errors.New("h265parser: no SPS found in AVCDecoderConfRecord")
		return
	}
	if len(codecPar.RecordInfo.PPS) == 0 {
		err = errors.New("h265parser: no PPS found in AVCDecoderConfRecord")
		return
	}
	if len(codecPar.RecordInfo.VPS) == 0 {
		err = errors.New("h265parser: no VPS found in AVCDecoderConfRecord")
		return
	}
	if codecPar.SPSInfo, err = ParseSPS(codecPar.RecordInfo.SPS[0]); err != nil {
		err = fmt.Errorf("h265parser: parse SPS failed(%w)", err)
		return
	}
	codecPar.CodecType = gomedia.H265
	// Calculate bitrate based on width, FPS and a constant factor
	codecPar.BRate = uint(
		float64(codecPar.Width()) *
			(bitrateEstimationFactor * (referenceFrameRate / float64(codecPar.FPS()))) *
			kbpsToBpsMultiplier,
	)

	return
}

func NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps []byte) (codecPar CodecParameters, err error) {
	if len(sps) == 0 || len(pps) == 0 || len(vps) == 0 {
		return
	}

	recordinfo := new(HEVCDecoderConfRecord)
	recordinfo.AVCProfileIndication = sps[3]
	recordinfo.ProfileCompatibility = sps[4]
	recordinfo.AVCLevelIndication = sps[5]
	recordinfo.SPS = [][]byte{sps}
	recordinfo.PPS = [][]byte{pps}
	recordinfo.VPS = [][]byte{vps}
	recordinfo.LengthSizeMinusOne = 3
	if codecPar.SPSInfo, err = ParseSPS(sps); err != nil {
		return
	}
	buf := make([]byte, recordinfo.Len())
	recordinfo.Marshal(buf)
	codecPar.RecordInfo = *recordinfo
	codecPar.Record = buf
	codecPar.CodecType = gomedia.H265
	// Calculate bitrate based on width, FPS and a constant factor
	codecPar.BRate = uint(
		float64(codecPar.Width()) *
			(bitrateEstimationFactor * (referenceFrameRate / float64(codecPar.FPS()))) *
			kbpsToBpsMultiplier,
	)

	return
}

func (par *CodecParameters) AVCDecoderConfRecordBytes() []byte {
	return par.Record
}

func (par *CodecParameters) SPS() []byte {
	if len(par.RecordInfo.SPS) == 0 {
		return []byte{}
	}
	return par.RecordInfo.SPS[0]
}

func (par *CodecParameters) PPS() []byte {
	if len(par.RecordInfo.PPS) == 0 {
		return []byte{}
	}
	return par.RecordInfo.PPS[0]
}

func (par *CodecParameters) VPS() []byte {
	if len(par.RecordInfo.VPS) == 0 {
		return []byte{}
	}
	return par.RecordInfo.VPS[0]
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

// Tag returns a string tag representing the codec information.
func (par *CodecParameters) Tag() string {
	return fmt.Sprintf("hev1.%01X.%01X.L%02X.90",
		par.RecordInfo.AVCProfileIndication,
		par.RecordInfo.ProfileCompatibility,
		par.RecordInfo.AVCLevelIndication)
}
