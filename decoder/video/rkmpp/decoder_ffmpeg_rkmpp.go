//go:build linux && arm64

package rkmpp

//#cgo pkg-config: rockchip_mpp
//#cgo CFLAGS: -I/usr/include/rockchip
//#cgo LDFLAGS: -lrga
//#include "decoder_rkmpp_native.h"
import "C"
import (
	"image"
	"unsafe"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/codec/h265"
	"github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/decoder/video"
	"github.com/ugparu/gomedia/frame/rgb"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/logger"
)

type ffmpegRKMPPDecoder struct {
	dcd         *C.NativeRkmppDecoder
	headersSent bool
}

func NewFFmpegRKMPPDecoder() decoder.InnerVideoDecoder {
	return &ffmpegRKMPPDecoder{
		dcd:         new(C.NativeRkmppDecoder),
		headersSent: false,
	}
}

func (dcd *ffmpegRKMPPDecoder) Init(codecPar gomedia.VideoCodecParameters) (err error) {
	var codecID C.int
	var width, height C.int

	switch par := codecPar.(type) {
	case *h264.CodecParameters:
		codecID = 1
		width = C.int(par.Width())
		height = C.int(par.Height())
	case *h265.CodecParameters:
		codecID = 2
		width = C.int(par.Width())
		height = C.int(par.Height())
	default:
		return video.NewFFmpegError("unsupported codec parameters type", -1)
	}

	logger.Debugf(dcd, "Initializing native rkmpp decoder with codec ID %d, width %d, height %d", codecID, width, height)

	dcd.dcd = new(C.NativeRkmppDecoder)
	if ret := C.init_rkmpp_decoder_native(dcd.dcd, codecID, width, height); ret < 0 {
		return video.NewFFmpegError("can not init native rkmpp decoder", int(ret))
	}

	return nil
}

// sendCodecHeaders sends SPS/PPS (and VPS for H.265) as separate Annex-B NAL units
// before the first regular packet is fed into RKMPP.
func (dcd *ffmpegRKMPPDecoder) sendCodecHeaders(vpar gomedia.VideoCodecParameters) error {
	if dcd.headersSent {
		return nil
	}

	logger.Debugf(dcd, "Sending codec headers for video codec parameters %+v", vpar)

	var nalUnits [][]byte

	switch par := vpar.(type) {
	case *h264.CodecParameters:
		sps := par.SPS()
		pps := par.PPS()
		if len(sps) > 0 {
			nal := make([]byte, 4+len(sps))
			nal[0], nal[1], nal[2], nal[3] = 0, 0, 0, 1
			copy(nal[4:], sps)
			nalUnits = append(nalUnits, nal)
		}
		if len(pps) > 0 {
			nal := make([]byte, 4+len(pps))
			nal[0], nal[1], nal[2], nal[3] = 0, 0, 0, 1
			copy(nal[4:], pps)
			nalUnits = append(nalUnits, nal)
		}
	case *h265.CodecParameters:
		vps := par.VPS()
		sps := par.SPS()
		pps := par.PPS()
		if len(vps) > 0 {
			nal := make([]byte, 4+len(vps))
			nal[0], nal[1], nal[2], nal[3] = 0, 0, 0, 1
			copy(nal[4:], vps)
			nalUnits = append(nalUnits, nal)
		}
		if len(sps) > 0 {
			nal := make([]byte, 4+len(sps))
			nal[0], nal[1], nal[2], nal[3] = 0, 0, 0, 1
			copy(nal[4:], sps)
			nalUnits = append(nalUnits, nal)
		}
		if len(pps) > 0 {
			nal := make([]byte, 4+len(pps))
			nal[0], nal[1], nal[2], nal[3] = 0, 0, 0, 1
			copy(nal[4:], pps)
			nalUnits = append(nalUnits, nal)
		}
	default:
		// Неизвестный тип — просто не шлём заголовки
		return nil
	}

	for _, nal := range nalUnits {
		ret := C.feed_rkmpp_packet_native(
			dcd.dcd,
			(*C.uint8_t)(unsafe.Pointer(&nal[0])),
			C.int(len(nal)),
			C.int64_t(0), // параметры считаем вне временной шкалы кадров
		)
		if ret < 0 {
			return video.NewFFmpegError("can not feed codec header to rkmpp", int(ret))
		}
	}

	dcd.headersSent = true
	return nil
}

// splitLengthPrefixedToNALUnits splits length-prefixed packet into individual NAL units
// Each NAL unit will have Annex-B start code (00 00 00 01) prepended
func splitLengthPrefixedToNALUnits(data []byte) [][]byte {
	var nalUnits [][]byte
	offset := 0

	for offset < len(data) {
		if offset+4 > len(data) {
			break
		}

		// Read 4-byte big-endian length prefix
		nalLength := int(data[offset])<<24 |
			int(data[offset+1])<<16 |
			int(data[offset+2])<<8 |
			int(data[offset+3])

		if nalLength <= 0 || offset+4+nalLength > len(data) {
			break
		}

		// Create new NAL unit with Annex-B start code
		nalUnit := make([]byte, 4+nalLength)
		nalUnit[0] = 0x00
		nalUnit[1] = 0x00
		nalUnit[2] = 0x00
		nalUnit[3] = 0x01
		copy(nalUnit[4:], data[offset+4:offset+4+nalLength])

		nalUnits = append(nalUnits, nalUnit)
		offset += 4 + nalLength
	}

	return nalUnits
}

func extractPacketData(pkt gomedia.VideoPacket) (data []byte, ptsMs int64, codecID int, err error) {
	switch p := pkt.(type) {
	case *h264.Packet:
		codecID = 1
		ptsMs = p.Timestamp().Milliseconds()
		data = make([]byte, p.Len())

		p.View(func(buf buffer.PooledBuffer) {
			copy(data, buf.Data())
		})
		return
	case *h265.Packet:
		codecID = 2
		ptsMs = p.Timestamp().Milliseconds()
		data = make([]byte, p.Len())

		p.View(func(buf buffer.PooledBuffer) {
			copy(data, buf.Data())
		})
		return
	default:
		err = video.NewFFmpegError("unsupported packet type", -1)
		return
	}
}

func (dcd *ffmpegRKMPPDecoder) Feed(pkt gomedia.VideoPacket) (err error) {
	// Перед первым обычным пакетом отправляем VPS/SPS/PPS
	if err := dcd.sendCodecHeaders(pkt.CodecParameters()); err != nil {
		return err
	}

	data, ptsMs, _, err := extractPacketData(pkt)
	if err != nil {
		return err
	}

	if len(data) == 0 {
		return nil
	}

	// Split length-prefixed packet into individual NAL units
	nalUnits := splitLengthPrefixedToNALUnits(data)

	// Feed each NAL unit separately to RKMPP
	for _, nalUnit := range nalUnits {
		ret := C.feed_rkmpp_packet_native(
			dcd.dcd,
			(*C.uint8_t)(unsafe.Pointer(&nalUnit[0])),
			C.int(len(nalUnit)),
			C.int64_t(ptsMs),
		)
		if ret < 0 {
			return video.NewFFmpegError("can not feed packet to rkmpp", int(ret))
		}
	}

	return nil
}

func (dcd *ffmpegRKMPPDecoder) Decode(pkt gomedia.VideoPacket) (image.Image, error) {
	// Перед первым обычным пакетом отправляем VPS/SPS/PPS
	if err := dcd.sendCodecHeaders(pkt.CodecParameters()); err != nil {
		return nil, err
	}

	data, ptsMs, _, err := extractPacketData(pkt)
	if err != nil {
		return nil, err
	}

	logger.Debugf(dcd, "Decoding packet with data length %d, ptsMs %d", len(data), ptsMs)

	if len(data) != 0 {
		// Split length-prefixed packet into individual NAL units
		nalUnits := splitLengthPrefixedToNALUnits(data)

		// Feed each NAL unit separately to RKMPP
		for _, nalUnit := range nalUnits {
			retFeed := C.feed_rkmpp_packet_native(
				dcd.dcd,
				(*C.uint8_t)(unsafe.Pointer(&nalUnit[0])),
				C.int(len(nalUnit)),
				C.int64_t(ptsMs),
			)
			if retFeed < 0 {
				return nil, video.NewFFmpegError("can not feed packet to rkmpp", int(retFeed))
			}
		}
	}

	width := int(pkt.CodecParameters().Width())
	height := int(pkt.CodecParameters().Height())

	img := rgb.NewRGB(image.Rect(0, 0, width, height))

	if len(img.Pix) == 0 {
		return nil, video.NewFFmpegError("empty image buffer", -1)
	}

	ret := C.decode_rkmpp_frame_native(
		dcd.dcd,
		(*C.uint8_t)(unsafe.Pointer(&img.Pix[0])),
		C.int(len(img.Pix)),
	)
	if ret != 0 {
		if ret > 0 {
			return nil, decoder.ErrNeedMoreData
		}
		return nil, video.NewFFmpegError("can not decode frame from rkmpp", int(ret))
	}

	return img, nil
}

func (dcd *ffmpegRKMPPDecoder) Close() {
	if dcd.dcd != nil {
		C.close_rkmpp_decoder_native(dcd.dcd)
		dcd.dcd = nil
	}
}
