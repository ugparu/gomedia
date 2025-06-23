package gomedia

import "fmt"

// ChannelLayout represents the audio channel layout.
type ChannelLayout uint16

// String returns the human-readable string representation of a ChannelLayout.
func (ch ChannelLayout) String() string {
	return fmt.Sprintf("%dch", ch.Count())
}

// Constants representing specific audio channel layouts.
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

// Count returns the number of channels in the ChannelLayout.
func (ch ChannelLayout) Count() (n int) {
	for ch != 0 {
		n++
		ch = (ch - 1) & ch
	}
	return
}
