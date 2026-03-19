// nolint: all
package mp4io

import (
	"github.com/ugparu/gomedia/utils/bits/pio"
)

type TimeToSampleEntry struct {
	Count    uint32
	Duration uint32
}

func GetTimeToSampleEntry(b []byte) (self TimeToSampleEntry) {
	self.Count = pio.U32BE(b[0:])
	self.Duration = pio.U32BE(b[4:])
	return
}
func PutTimeToSampleEntry(b []byte, self TimeToSampleEntry) {
	pio.PutU32BE(b[0:], self.Count)
	pio.PutU32BE(b[4:], self.Duration)
}

const LenTimeToSampleEntry = 8

type SampleToChunkEntry struct {
	FirstChunk      uint32
	SamplesPerChunk uint32
	SampleDescId    uint32
}

func GetSampleToChunkEntry(b []byte) (self SampleToChunkEntry) {
	self.FirstChunk = pio.U32BE(b[0:])
	self.SamplesPerChunk = pio.U32BE(b[4:])
	self.SampleDescId = pio.U32BE(b[8:])
	return
}
func PutSampleToChunkEntry(b []byte, self SampleToChunkEntry) {
	pio.PutU32BE(b[0:], self.FirstChunk)
	pio.PutU32BE(b[4:], self.SamplesPerChunk)
	pio.PutU32BE(b[8:], self.SampleDescId)
}

const LenSampleToChunkEntry = 12

type CompositionOffsetEntry struct {
	Count  uint32
	Offset uint32
}

func GetCompositionOffsetEntry(b []byte) (self CompositionOffsetEntry) {
	self.Count = pio.U32BE(b[0:])
	self.Offset = pio.U32BE(b[4:])
	return
}
func PutCompositionOffsetEntry(b []byte, self CompositionOffsetEntry) {
	pio.PutU32BE(b[0:], self.Count)
	pio.PutU32BE(b[4:], self.Offset)
}

const LenCompositionOffsetEntry = 8
