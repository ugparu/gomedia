package gomedia

// SampleFormat identifies an audio sample's numeric type and layout. Planar
// variants (suffix P) store each channel in a separate buffer; all others are
// interleaved.
type SampleFormat uint8

const (
	U8   = SampleFormat(iota + 1) // 8-bit unsigned integer
	S16                           // signed 16-bit integer
	S32                           // signed 32-bit integer
	FLT                           // 32-bit float
	DBL                           // 64-bit float
	U8P                           // 8-bit unsigned integer in planar
	S16P                          // signed 16-bit integer in planar
	S32P                          // signed 32-bit integer in planar
	FLTP                          // 32-bit float in planar
	DBLP                          // 64-bit float in planar
	U32                           // unsigned 32-bit integer
)

// BytesPerSample is the byte width of one sample in this format (per channel
// for planar formats, across all channels combined for interleaved formats).
func (sf SampleFormat) BytesPerSample() int {
	switch sf {
	case U8, U8P:
		return 1
	case S16, S16P:
		return 2 //nolint:mnd
	case FLT, FLTP, S32, S32P, U32:
		return 4 //nolint:mnd
	case DBL, DBLP:
		return 8 //nolint:mnd
	default:
		return 0
	}
}

func (sf SampleFormat) String() string {
	switch sf {
	case U8:
		return "U8"
	case S16:
		return "S16"
	case S32:
		return "S32"
	case FLT:
		return "FLT"
	case DBL:
		return "DBL"
	case U8P:
		return "U8P"
	case S16P:
		return "S16P"
	case FLTP:
		return "FLTP"
	case DBLP:
		return "DBLP"
	case U32:
		return "U32"
	case S32P:
		return "S32P"
	default:
		return "?"
	}
}

// IsPlanar reports whether each channel is stored in a separate buffer
// (as opposed to samples interleaved across channels in one buffer).
func (sf SampleFormat) IsPlanar() bool {
	switch sf { //nolint: exhaustive // other formats are not planar
	case S16P, S32P, FLTP, DBLP:
		return true
	default:
		return false
	}
}
