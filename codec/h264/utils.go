package h264

// NAL unit types per ISO/IEC 14496-10 §7.4.1.
const (
	NaluCodedIDR = 5 // IDR (Instantaneous Decoding Refresh) slice
	NaluSPS      = 7 // Sequence Parameter Set
	NaluPPS      = 8 // Picture Parameter Set
)

const byteSize = 8

// Common magic numbers used in the package
const (
	// Bit masks
	maskLengthSizeMinusOne    = 0x03
	maskSPSCount              = 0x1f
	maskLengthSizeMinusOneInv = 0xfc
	maskSPSCountInv           = 0xe0

	// Scaling values
	defaultScaleValue = 8
	maxScaleValue     = 256

	// Bit sizes
	bits3  = 3
	bits8  = 8
	bits16 = 16
	bits32 = 32

	// Chroma format values
	chromaFormat3 = 3

	// Scaling list sizes
	scalingListSizeSmall = 16
	scalingListSizeLarge = 64
	scalingListThreshold = 6

	// Aspect ratio values
	aspectRatioExtended = 255

	// Bitrate calculation constants
	bitrateScaleFactor = 1.71
	bitrateFrameRate   = 30
	bitrateMultiplier  = 1000

	// Macroblock size
	mbSize = 16

	// Frame rate divisor
	frameRateDivisor = 2.0

	// Crop multiplier
	cropMultiplier = 2

	// Frame height calculation constant
	frameHeightBase = 2

	// Length field size in AVCDecoderConfRecord
	lengthFieldSize = 2
)

// SPSInfo holds the fields parsed from an H.264 Sequence Parameter Set
// (ISO/IEC 14496-10 §7.3.2.1). Width/Height/FPS are derived values, not
// raw fields — they fold in cropping and frame-rate clock info.
type SPSInfo struct {
	ID                uint
	ProfileIDC        uint
	LevelIDC          uint
	ConstraintSetFlag uint

	MbWidth  uint
	MbHeight uint

	CropLeft   uint
	CropRight  uint
	CropTop    uint
	CropBottom uint

	Width  uint
	Height uint
	FPS    uint
}
