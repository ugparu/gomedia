package video

//#cgo pkg-config: libavutil libavcodec libavformat
//#include <libavutil/avutil.h>
//#include <libavcodec/avcodec.h>
//#include <libavformat/avformat.h>
import "C"
import (
	"fmt"
	"unsafe"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/codec/h265"
	"github.com/ugparu/gomedia/utils/buffer"
)

const errBufSize = 50

type FFmpegError struct {
	error
}

func NewFFmpegError(msg string, ret int) error {
	errBuf := (*C.char)(C.malloc(errBufSize))
	defer C.free(unsafe.Pointer(errBuf))
	C.av_strerror(C.int(ret), errBuf, errBufSize)
	return &FFmpegError{fmt.Errorf("%s: code=%v msg=%s", msg, ret, C.GoString(errBuf))}
}

func ParametersToFFmpeg(vPar gomedia.VideoCodecParameters, ptr unsafe.Pointer) error {
	cPtr := (*C.struct_AVCodecParameters)(ptr)
	cPtr.bit_rate = 1
	cPtr.width = C.int(vPar.Width())
	cPtr.height = C.int(vPar.Height())
	cPtr.codec_type = C.AVMEDIA_TYPE_VIDEO
	cPtr.format = C.AV_PIX_FMT_YUV420P

	switch par := vPar.(type) {
	case *h264.CodecParameters:
		cPtr.codec_id = C.AV_CODEC_ID_H264
		cPtr.profile = C.int(par.RecordInfo.AVCProfileIndication)
		cPtr.level = C.int(par.RecordInfo.AVCLevelIndication)

		sps, pps := par.SPS(), par.PPS()

		bytes := make([]byte, 0, 8+len(sps)+len(pps))
		bytes = append(bytes, 0, 0, 0, 1)
		bytes = append(bytes, sps...)
		bytes = append(bytes, 0, 0, 0, 1)
		bytes = append(bytes, pps...)

		cPtr.extradata_size = C.int(len(bytes))
		cPtr.extradata = (*C.uchar)(C.malloc(C.ulong(cPtr.extradata_size)))
		extra := unsafe.Slice((*byte)(cPtr.extradata), int(cPtr.extradata_size))
		copy(extra, bytes)
	case *h265.CodecParameters:
		cPtr.codec_id = C.AV_CODEC_ID_H265
		cPtr.profile = C.int(par.RecordInfo.AVCProfileIndication)
		cPtr.level = C.int(par.RecordInfo.AVCLevelIndication)

		vps, sps, pps := par.VPS(), par.SPS(), par.PPS()

		bytes := make([]byte, 0, 12+len(vps)+len(sps)+len(pps))
		bytes = append(bytes, 0, 0, 0, 1)
		bytes = append(bytes, vps...)
		bytes = append(bytes, 0, 0, 0, 1)
		bytes = append(bytes, sps...)
		bytes = append(bytes, 0, 0, 0, 1)
		bytes = append(bytes, pps...)

		cPtr.extradata_size = C.int(len(bytes))
		cPtr.extradata = (*C.uchar)(C.malloc(C.ulong(cPtr.extradata_size)))
		extra := unsafe.Slice((*byte)(cPtr.extradata), int(cPtr.extradata_size))
		copy(extra, bytes)
	default:
		return fmt.Errorf("unsupported codec type: %T", vPar)
	}

	return nil
}

// convertLengthPrefixedToAnnexB converts length-prefixed NAL units to Annex-B format
// This is needed for H.265 packets which come with 4-byte length prefixes
func convertLengthPrefixedToAnnexB(data []byte) {
	offset := 0
	for offset < len(data) {
		if offset+4 > len(data) {
			break
		}

		// Read the 4-byte length prefix
		nalLength := int(data[offset])<<24 | int(data[offset+1])<<16 | int(data[offset+2])<<8 | int(data[offset+3])

		// Replace length prefix with Annex-B start code (0x00 0x00 0x00 0x01)
		data[offset] = 0x00
		data[offset+1] = 0x00
		data[offset+2] = 0x00
		data[offset+3] = 0x01

		// Move to next NAL unit
		offset += 4 + nalLength
	}
}

func PacketToFFmpeg(vPkt gomedia.VideoPacket, ptr unsafe.Pointer) error {
	cPkt := (*C.struct_AVPacket)(ptr)

	switch pkt := vPkt.(type) {
	case *h264.Packet:
		cPkt.stream_index = C.int(pkt.StreamIndex())
		cPkt.dts = C.long(pkt.Timestamp().Milliseconds())
		cPkt.pts = cPkt.dts
		cPkt.time_base.num = 1
		cPkt.time_base.den = 1000000

		C.av_grow_packet(cPkt, C.int(pkt.Len()))

		slice := unsafe.Slice((*byte)(cPkt.data), int(cPkt.size))
		pkt.View(func(data buffer.PooledBuffer) {
			copy(slice, data.Data())
		})

		if len(slice) != 0 {
			slice[0] = 0
			slice[1] = 0
			slice[2] = 0
			slice[3] = 1
		}
		return nil
	case *h265.Packet:
		cPkt.stream_index = C.int(pkt.StreamIndex())
		cPkt.dts = C.long(pkt.Timestamp().Milliseconds())
		cPkt.pts = cPkt.dts
		cPkt.time_base.num = 1
		cPkt.time_base.den = 1000000

		C.av_grow_packet(cPkt, C.int(pkt.Len()))

		slice := unsafe.Slice((*byte)(cPkt.data), int(cPkt.size))
		pkt.View(func(data buffer.PooledBuffer) {
			copy(slice, data.Data())
		})
		// Convert length-prefixed NAL units to Annex-B format for H.265
		convertLengthPrefixedToAnnexB(slice)
		return nil
	default:
		return fmt.Errorf("unsupported packet type: %T", vPkt)
	}
}
