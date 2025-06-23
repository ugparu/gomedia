package gomedia

// CodecType represents the type of a codec.
type CodecType uint32

// avCodecTypeMagic is a magic number used to create unique codec types.
const avCodecTypeMagic = 233333

// makeAudioCodecType creates an audio CodecType based on the provided base.
func makeAudioCodecType(base uint32) (c CodecType) {
	c = CodecType(base)<<codecTypeOtherBits | CodecType(codecTypeAudioBit)
	return
}

// makeVideoCodecType creates a video CodecType based on the provided base.
func makeVideoCodecType(base uint32) (c CodecType) {
	c = CodecType(base) << codecTypeOtherBits
	return
}

// variables representing specific codec types.
var (
	H264       = makeVideoCodecType(avCodecTypeMagic + 1) //nolint:mnd
	H265       = makeVideoCodecType(avCodecTypeMagic + 2) //nolint:mnd
	JPEG       = makeVideoCodecType(avCodecTypeMagic + 3) //nolint:mnd
	VP8        = makeVideoCodecType(avCodecTypeMagic + 4) //nolint:mnd
	VP9        = makeVideoCodecType(avCodecTypeMagic + 5) //nolint:mnd
	AV1        = makeVideoCodecType(avCodecTypeMagic + 6) //nolint:mnd
	MJPEG      = makeVideoCodecType(avCodecTypeMagic + 7) //nolint:mnd
	AAC        = makeAudioCodecType(avCodecTypeMagic + 1) //nolint:mnd
	PCMMulaw   = makeAudioCodecType(avCodecTypeMagic + 2) //nolint:mnd
	PCMAlaw    = makeAudioCodecType(avCodecTypeMagic + 3) //nolint:mnd
	SPEEX      = makeAudioCodecType(avCodecTypeMagic + 4) //nolint:mnd
	NELLYMOSER = makeAudioCodecType(avCodecTypeMagic + 5) //nolint:mnd
	PCM        = makeAudioCodecType(avCodecTypeMagic + 6) //nolint:mnd
	OPUS       = makeAudioCodecType(avCodecTypeMagic + 7) //nolint:mnd
)

// Bitwise flags for codec types.
const (
	codecTypeAudioBit  = 0x1
	codecTypeOtherBits = 1
)

// String returns the human-readable string representation of a CodecType.
func (ct CodecType) String() string {
	switch ct {
	case H264:
		return "H264"
	case H265:
		return "H265"
	case JPEG:
		return "JPEG"
	case VP8:
		return "VP8"
	case VP9:
		return "VP9"
	case AV1:
		return "AV1"
	case AAC:
		return "AAC"
	case PCMMulaw:
		return "PCM_MULAW"
	case PCMAlaw:
		return "PCM_ALAW"
	case SPEEX:
		return "SPEEX"
	case NELLYMOSER:
		return "NELLYMOSER"
	case PCM:
		return "PCM"
	case OPUS:
		return "OPUS"
	}
	return "UNKNOWN"
}

// IsAudio returns true if the CodecType represents an audio codec.
func (ct CodecType) IsAudio() bool {
	return ct&codecTypeAudioBit != 0
}

// IsVideo returns true if the CodecType represents a video codec.
func (ct CodecType) IsVideo() bool {
	return ct&codecTypeAudioBit == 0
}
