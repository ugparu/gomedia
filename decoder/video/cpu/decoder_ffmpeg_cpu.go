package cpu

//#cgo pkg-config: libavcodec libavutil libswscale
//#include "decoder_ffmpeg_cpu.h"
import "C"
import (
	"unsafe"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	"github.com/ugparu/gomedia/decoder/video"
	"github.com/ugparu/gomedia/frame/rgb"
)

type ffmpegCPUDecoder struct {
	dcd  *C.cpuDecoder
	pool *rgb.FramePool
}

func NewFFmpegCPUDecoder() decoder.InnerVideoDecoder {
	return &ffmpegCPUDecoder{
		dcd: new(C.cpuDecoder),
	}
}

func (dcd *ffmpegCPUDecoder) Init(codecPar gomedia.VideoCodecParameters) (err error) {
	cPar := C.avcodec_parameters_alloc()
	defer C.avcodec_parameters_free(&cPar) //nolint:gocritic // CGO function call

	if err = video.ParametersToFFmpeg(codecPar, unsafe.Pointer(cPar)); err != nil {
		return err
	}

	dcd.dcd = new(C.cpuDecoder)
	if ret := C.init_cpu_decoder(dcd.dcd, cPar); ret < 0 {
		return video.NewFFmpegError("can not init cpu decoder", int(ret))
	}

	w := int(dcd.dcd.rgb_frame.width)
	h := int(dcd.dcd.rgb_frame.height)
	dcd.pool = rgb.NewFramePool(w, h)

	return
}

func (dcd *ffmpegCPUDecoder) Feed(pkt gomedia.VideoPacket) (err error) {
	if err = video.PacketToFFmpeg(pkt, unsafe.Pointer(dcd.dcd.packet)); err != nil {
		return err
	}

	ret := C.decode_cpu_packet(dcd.dcd, nil)
	if ret < 0 {
		return video.NewFFmpegError("can not decode packet", int(ret))
	}
	return nil
}

func (dcd *ffmpegCPUDecoder) Decode(pkt gomedia.VideoPacket) (rgb.ReleasableImage, error) {
	if err := video.PacketToFFmpeg(pkt, unsafe.Pointer(dcd.dcd.packet)); err != nil {
		return nil, err
	}

	img := dcd.pool.Get()
	ret := C.decode_cpu_packet(dcd.dcd, (*C.uint8_t)(unsafe.Pointer(&img.Pix[0])))
	if ret != 0 {
		img.Release()
		if ret > 0 {
			return nil, decoder.ErrNeedMoreData
		}
		return nil, video.NewFFmpegError("can not decode packet", int(ret))
	}
	return img, nil
}

func (dcd *ffmpegCPUDecoder) Close() {
	C.close_cpu_decoder(dcd.dcd)
}
