package gomedia

// SampleFormat represents different audio sample formats.
type SampleFormat uint8

// Constants representing various audio sample formats.
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

// BytesPerSample returns the number of bytes per audio sample for the given sample format.
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

// String returns a human-readable string representation of the sample format.
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

// IsPlanar checks if the sample format is in planar layout.
func (sf SampleFormat) IsPlanar() bool {
	switch sf { //nolint: exhaustive // other formats are not planar
	case S16P, S32P, FLTP, DBLP:
		return true
	default:
		return false
	}
}
