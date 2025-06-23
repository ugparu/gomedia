package cuda

//#cgo LDFLAGS: -L/usr/local/cuda/lib64 -lnppc -lnppial -lnppicc -lnppidei -lnppif -lnppig  -lnppim -lnppist -lnppisu -lnppitc -lnpps -lcudart
//#cgo CFLAGS: -I/usr/local/cuda/include
//#cgo pkg-config: libavcodec libavutil libswscale
//#include "decoder_cuda.h"
import "C"
import (
	"errors"
	"image"
	"unsafe"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/decoder/video"
	"github.com/ugparu/gomedia/frame/rgb"
)

type ffmpegCUDADecoder struct {
	dcd *C.cudaDecoder
}

func NewFFmpegCUDADecoder() decoder.InnerVideoDecoder {
	return &ffmpegCUDADecoder{
		dcd: new(C.cudaDecoder),
	}
}

func (dcd *ffmpegCUDADecoder) Init(codecPar gomedia.VideoCodecParameters) (err error) {
	cPar := C.avcodec_parameters_alloc()
	defer C.avcodec_parameters_free(&cPar) //nolint:gocritic // CGO function call

	if err = video.ParametersToFFmpeg(codecPar, unsafe.Pointer(cPar)); err != nil {
		return
	}

	dcd.dcd = new(C.cudaDecoder)

	var idx int
	select {
	case idx = <-cudaMatIdxs:
	default:
		idx = <-freeMatIdxs
	}

	dcd.dcd.mat_index = C.int(idx)

	if ret := C.init_cuda_decoder(dcd.dcd, cPar); ret < 0 {
		return video.NewFFmpegError("can not init cuda decoder", int(ret))
	}

	return
}

func (dcd *ffmpegCUDADecoder) Feed(pkt gomedia.VideoPacket) error {
	if err := video.PacketToFFmpeg(pkt, unsafe.Pointer(dcd.dcd.packet)); err != nil {
		return err
	}

	ret := C.decode_cuda_packet(dcd.dcd, nil)
	if ret < 0 {
		return video.NewFFmpegError("can not decode packet", int(ret))
	}
	return nil
}

func (dcd *ffmpegCUDADecoder) Decode(pkt gomedia.VideoPacket) (image.Image, error) {
	if err := video.PacketToFFmpeg(pkt, unsafe.Pointer(dcd.dcd.packet)); err != nil {
		return nil, err
	}

	img := rgb.NewRGB(image.Rect(0, 0, int(pkt.CodecParameters().Width()), int(pkt.CodecParameters().Height())))
	ret := C.decode_cuda_packet(dcd.dcd, (*C.uint8_t)(unsafe.Pointer(&img.Pix[0])))
	if ret != 0 {
		if ret == -999 {
			return nil, errors.New("libnpp error")
		}
		if ret > 0 {
			return nil, decoder.ErrNeedMoreData
		}
		return nil, video.NewFFmpegError("can not decode packet", int(ret))
	}
	return img, nil
}

func (dcd *ffmpegCUDADecoder) Close() {
	if dcd.dcd != nil {
		freeMatIdxs <- int(dcd.dcd.mat_index)
		dcd.dcd.mat_index = -1
		C.close_cuda_decoder(dcd.dcd)
	}
}
