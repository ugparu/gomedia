package h264

// minAVCRecordSize is the minimum size of an AVC (Advanced Video Coding) record.
const minAVCRecordSize = 3

// NaluCodedIDR represents the Network Abstraction Layer Unit (NALU) type for
// Coded IDR (Instantaneous Decoding Refresh).
const NaluCodedIDR = 5

// NaluSPS represents the Network Abstraction Layer Unit (NALU) type for Sequence Parameter Set.
const NaluSPS = 7

// NaluPPS represents the Network Abstraction Layer Unit (NALU) type for Picture Parameter Set.
const NaluPPS = 8

// byteSize is the number of bits in a byte.
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

// SPSInfo represents information extracted from Sequence Parameter Sets (SPS) in a video stream.
type SPSInfo struct {
	ID                uint // Identifier for the SPS.
	ProfileIDC        uint // Profile identifier for the SPS.
	LevelIDC          uint // Level identifier for the SPS.
	ConstraintSetFlag uint // Constraint set flag for the SPS.

	MbWidth  uint // Width of macroblocks in the SPS.
	MbHeight uint // Height of macroblocks in the SPS.

	CropLeft   uint // Left cropping value for the SPS.
	CropRight  uint // Right cropping value for the SPS.
	CropTop    uint // Top cropping value for the SPS.
	CropBottom uint // Bottom cropping value for the SPS.

	Width  uint // Width of the video frame.
	Height uint // Height of the video frame.
	FPS    uint // Frames per second (FPS) for the video stream.
}
