package aac

//#cgo pkg-config: libavcodec libavutil libswresample libavformat
//#include "aac_decoder_ffmpeg.h"
import "C"
import (
	"fmt"
	"unsafe"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/decoder"
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

func AudioParametersToFFmpeg(aPar gomedia.AudioCodecParameters, ptr unsafe.Pointer) error {
	cPtr := (*C.struct_AVCodecParameters)(ptr)
	cPtr.codec_type = C.AVMEDIA_TYPE_AUDIO
	cPtr.sample_rate = C.int(aPar.SampleRate())
	cPtr.ch_layout.nb_channels = C.int(aPar.Channels())

	// Set channel layout based on number of channels using av_channel_layout_default
	C.av_channel_layout_default(&cPtr.ch_layout, C.int(aPar.Channels()))

	switch par := aPar.(type) {
	case *aac.CodecParameters:
		cPtr.codec_id = C.AV_CODEC_ID_AAC

		// Set up extradata with AAC decoder config
		configBytes := par.MPEG4AudioConfigBytes()
		if len(configBytes) > 0 {
			cPtr.extradata_size = C.int(len(configBytes))
			// Use malloc for compatibility - extradata will be freed by avcodec_parameters_free
			cPtr.extradata = (*C.uchar)(C.malloc(C.ulong(cPtr.extradata_size)))
			if cPtr.extradata == nil {
				return fmt.Errorf("failed to allocate extradata memory")
			}
			extra := unsafe.Slice((*byte)(cPtr.extradata), int(cPtr.extradata_size))
			copy(extra, configBytes)
		}
	default:
		return fmt.Errorf("unsupported audio codec type: %T", aPar)
	}

	return nil
}

type aacDecoder struct {
	dec *C.aacDecoder
}

func NewAacDecoder() decoder.InnerAudioDecoder {
	aacDec := &aacDecoder{
		dec: new(C.aacDecoder),
	}

	return aacDec
}

func (d *aacDecoder) Init(param gomedia.AudioCodecParameters) error {
	cPar := C.avcodec_parameters_alloc()
	defer C.avcodec_parameters_free(&cPar) //nolint:gocritic // CGO function call

	if err := AudioParametersToFFmpeg(param, unsafe.Pointer(cPar)); err != nil {
		return err
	}

	d.dec = new(C.aacDecoder)
	if ret := C.init_aac_decoder(d.dec, cPar); ret < 0 {
		return NewFFmpegError("can not init aac decoder", int(ret))
	}

	return nil
}

func (d *aacDecoder) Decode(inData []byte) (outData []byte, err error) {
	// Ensure packet is clean and fill with input data
	if grow := len(inData) - int(d.dec.packet.size); grow > 0 {
		C.av_grow_packet(d.dec.packet, C.int(grow))
	} else if grow < 0 {
		C.av_shrink_packet(d.dec.packet, C.int(len(inData)))
	}

	slice := unsafe.Slice((*byte)(d.dec.packet.data), int(d.dec.packet.size))
	copy(slice, inData)

	var outputSize C.int
	ret := C.decode_aac_packet(d.dec, &outputSize)
	if ret != 0 {
		if ret > 0 {
			return nil, nil // Need more data or no output available
		}
		return nil, NewFFmpegError("can not decode AAC packet", int(ret))
	}

	if outputSize == 0 {
		return nil, nil
	}

	result := make([]byte, int(outputSize))
	copy(result, unsafe.Slice((*byte)(d.dec.audio_buf), int(outputSize)))

	return result, nil
}

func (d *aacDecoder) Close() {
	C.close_aac_decoder(d.dec)
}
