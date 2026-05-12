package gomedia

import "fmt"

// ChannelLayout is a bitmask of speaker positions; channel count is the number
// of set bits (see Count). Combine with bitwise OR to build custom layouts.
type ChannelLayout uint16

func (ch ChannelLayout) String() string {
	return fmt.Sprintf("%dch", ch.Count())
}

const (
	ChFrontCenter = ChannelLayout(1 << iota)
	ChFrontLeft
	ChFrontRight
	ChBackCenter
	ChBackLeft
	ChBackRight
	ChSideLeft
	ChSightRight
	ChLowFreq
	ChNr

	ChMono     = (ChFrontCenter)
	ChStereo   = (ChFrontLeft | ChFrontRight)
	Ch21       = (ChStereo | ChBackCenter)
	Ch2P1      = (ChStereo | ChLowFreq)
	ChSurround = (ChStereo | ChFrontCenter)
	Ch3P1      = (ChSurround | ChLowFreq)
)

// Count reports the number of set channel bits.
func (ch ChannelLayout) Count() (n int) {
	for ch != 0 {
		n++
		ch = (ch - 1) & ch
	}
	return
}
