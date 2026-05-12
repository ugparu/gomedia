// Package aac implements an MPEG-4 Audio (ADTS / AudioSpecificConfig) parser
// and a CodecParameters value built on top of it.
//
// Magic numbers throughout this file come from ISO/IEC 14496-3 (MPEG-4 Audio)
// and the libavcodec/mpeg4audio.h header from FFmpeg; they are tagged
// individually rather than file-wide.
package aac

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/utils/bits"
)

// Copied from libavcodec/mpeg4audio.h.
const (
	AotAacMain       = 1 + iota  // Y                       Main
	AotAacLc                     // Y                       Low Complexity
	AotAacSsr                    // N (code in SoC repo)    Scalable Sample Rate
	AotAacLtp                    // Y                       Long Term Prediction
	AotSbr                       // Y                       Spectral Band Replication
	AotAacScalable               // N                       Scalable
	AotTwinvq                    // N                       Twin Vector Quantizer
	AotCelp                      // N                       Code Excited Linear Prediction
	AotHvxc                      // N                       Harmonic Vector eXcitation Coding
	AotTtsi          = 12 + iota // N                       Text-To-Speech Interface
	AotMainsynth                 // N                       Main Synthesis
	AotWavesynth                 // N                       Wavetable Synthesis
	AotMidi                      // N                       General MIDI
	AotSafx                      // N                       Algorithmic Synthesis and Audio Effects
	AotErAacLc                   // N                       Error Resilient Low Complexity
	AotErAacLtp      = 19 + iota // N                       Error Resilient Long Term Prediction
	AotErAacScalable             // N                       Error Resilient Scalable
	AotErTwinvq                  // N                       Error Resilient Twin Vector Quantizer
	AotErBsac                    // N                       Error Resilient Bit-Sliced Arithmetic Coding
	AotErAacLd                   // N                       Error Resilient Low Delay
	AotErCelp                    // N                       Error Resilient Code Excited Linear Prediction
	AotErHvxc                    // N                       Error Resilient Harmonic Vector eXcitation Coding
	AotErHiln                    // N                       Error Resilient Harmonic and Individual Lines plus Noise
	AotErParam                   // N                       Error Resilient Parametric
	AotSsc                       // N                       SinuSoidal Coding
	AotPs                        // N                       Parametric Stereo
	AotSurround                  // N                       MPEG Surround
	AotEscape                    // Y                       Escape Value
	AotL1                        // Y                       Layer 1
	AotL2                        // Y                       Layer 2
	AotL3                        // Y                       Layer 3
	AotDst                       // N                       Direct Stream Transfer
	AotAls                       // Y                       Audio LosslesS
	AotSls                       // N                       Scalable LosslesS
	AotSlsNonCore                // N                       Scalable LosslesS (non core)
	AotErAacEld                  // N                       Error Resilient Enhanced Low Delay
	AotSmrSimple                 // N                       Symbolic Music Representation Simple
	AotSmrMain                   // N                       Symbolic Music Representation Main
	AotUsacNosbr                 // N                       Unified Speech and Audio Coding (no SBR)
	AotSaoc                      // N                       Spatial Audio Object Coding
	AotLdSurround                // N                       Low Delay MPEG Surround
	AotUsac                      // N                       Unified Speech and Audio Coding
)

type MPEG4AudioConfig struct {
	SampleRate      int
	ChannelLayout   gomedia.ChannelLayout
	ObjectType      uint
	SampleRateIndex uint
	ChannelConfig   uint
}

func (config *MPEG4AudioConfig) IsValid() bool {
	return config.ObjectType > 0
}

func (config *MPEG4AudioConfig) Complete() {
	if config.SampleRateIndex < uint(len(sampleRateTable)) {
		config.SampleRate = sampleRateTable[config.SampleRateIndex]
	}
	if config.ChannelConfig < uint(len(chanConfigTable)) {
		config.ChannelLayout = chanConfigTable[config.ChannelConfig]
	}
}

var sampleRateTable = []int{
	96000, 88200, 64000, 48000, 44100, 32000,
	24000, 22050, 16000, 12000, 11025, 8000, 7350,
}

// SampleRateIndex returns the MPEG-4 sample rate index for the given rate.
// If the rate is not in the standard table, it returns 0xf (explicit rate).
func SampleRateIndex(rate int) uint {
	for i, r := range sampleRateTable {
		if r == rate {
			return uint(i) //nolint:gosec // index fits in uint
		}
	}
	return 0xf //nolint:mnd // 0xf signals explicit sample rate in AudioSpecificConfig
}

/*
These are the channel configurations:
0: Defined in AOT Specifc Config
1: 1 channel: front-center
2: 2 channels: front-left, front-right
3: 3 channels: front-center, front-left, front-right
4: 4 channels: front-center, front-left, front-right, back-center
5: 5 channels: front-center, front-left, front-right, back-left, back-right
6: 6 channels: front-center, front-left, front-right, back-left, back-right, LFE-channel
7: 8 channels: front-center, front-left, front-right, side-left, side-right, back-left, back-right, LFE-channel
8-15: Reserved.
*/
// ChannelLayoutForConfig returns the channel layout bitmask for the given
// AAC channel configuration index (0-7). Returns 0 for out-of-range values.
func ChannelLayoutForConfig(config uint) gomedia.ChannelLayout {
	if config < uint(len(chanConfigTable)) {
		return chanConfigTable[config]
	}
	return 0
}

var chanConfigTable = []gomedia.ChannelLayout{
	0,
	gomedia.ChFrontCenter,
	gomedia.ChFrontLeft | gomedia.ChFrontRight,
	gomedia.ChFrontCenter | gomedia.ChFrontLeft | gomedia.ChFrontRight,
	gomedia.ChFrontCenter | gomedia.ChFrontLeft | gomedia.ChFrontRight | gomedia.ChBackCenter,
	gomedia.ChFrontCenter | gomedia.ChFrontLeft | gomedia.ChFrontRight | gomedia.ChBackLeft | gomedia.ChBackRight,
	gomedia.ChFrontCenter | gomedia.ChFrontLeft | gomedia.ChFrontRight | gomedia.ChBackLeft | gomedia.ChBackRight | gomedia.ChLowFreq,                                             //nolint: lll
	gomedia.ChFrontCenter | gomedia.ChFrontLeft | gomedia.ChFrontRight | gomedia.ChSideLeft | gomedia.ChSightRight | gomedia.ChBackLeft | gomedia.ChBackRight | gomedia.ChLowFreq, //nolint: lll
}

func ParseADTSHeader(frame []byte) (config MPEG4AudioConfig, hdrlen int, framelen int, samples int, err error) {
	if len(frame) < 7 { //nolint:mnd // unprotected ADTS header is 7 bytes
		err = fmt.Errorf("aacparser: insufficient data for ADTS header, need at least 7 bytes, got %d", len(frame))
		return
	}

	if frame[0] != 0xff || frame[1]&0xf6 != 0xf0 {
		err = fmt.Errorf("aacparser: invalid ADTS sync word: %02x %02x", frame[0], frame[1])
		return
	}

	config.ObjectType = uint(frame[2]>>6) + 1          //nolint:mnd // 2-bit profile in bits 7-6 of byte 2; AOT = profile + 1
	config.SampleRateIndex = uint(frame[2] >> 2 & 0xf) //nolint:mnd // 4-bit sampling_frequency_index in bits 5-2 of byte 2
	config.ChannelConfig = uint(frame[2]<<2&0x4 | frame[3]>>6&0x3)

	if config.SampleRateIndex >= uint(len(sampleRateTable)) {
		err = fmt.Errorf("aacparser: invalid sample rate index: %d", config.SampleRateIndex)
		return
	}

	// config == 0 is valid per ISO 14496-3 §1.6.3: the channel configuration
	// is defined in the bitstream (program_config_element).
	if config.ChannelConfig >= uint(len(chanConfigTable)) {
		err = fmt.Errorf("aacparser: invalid channel configuration: %d", config.ChannelConfig)
		return
	}

	(&config).Complete()

	framelen = int(frame[3]&0x3)<<11 | int(frame[4])<<3 | int(frame[5]>>5) //nolint:mnd,lll // bit fields per ISO/IEC 13818-7
	samples = (int(frame[6]&0x3) + 1) * 1024                               //nolint:mnd // 1024 samples per AAC frame

	hdrlen = 7
	if frame[1]&0x1 == 0 {
		hdrlen = 9
		if len(frame) < 9 { //nolint:mnd // CRC-protected ADTS header is 9 bytes
			err = fmt.Errorf("aacparser: insufficient data for protected ADTS header, need 9 bytes, got %d", len(frame))
			return
		}
	}

	if framelen < hdrlen {
		err = fmt.Errorf("aacparser: invalid ADTS frame length: %d (must be >= %d)", framelen, hdrlen)
		return
	}

	return
}

const ADTSHeaderLength = 7

func FillADTSHeader(header []byte, config MPEG4AudioConfig, samples int, payloadLength int) error {
	if len(header) < ADTSHeaderLength {
		return fmt.Errorf("aacparser: header buffer too small, needs at least %d bytes, got %d",
			ADTSHeaderLength, len(header))
	}

	if !config.IsValid() {
		return errors.New("aacparser: invalid MPEG4 audio configuration")
	}

	if config.SampleRateIndex >= uint(len(sampleRateTable)) {
		return fmt.Errorf("aacparser: invalid sample rate index: %d", config.SampleRateIndex)
	}

	if config.ChannelConfig >= uint(len(chanConfigTable)) {
		return fmt.Errorf("aacparser: invalid channel configuration: %d", config.ChannelConfig)
	}

	if samples <= 0 || samples%1024 != 0 {
		return fmt.Errorf("aacparser: invalid samples count: %d (must be multiple of 1024)", samples)
	}

	if payloadLength < 0 {
		return fmt.Errorf("aacparser: invalid payload length: %d", payloadLength)
	}

	payloadLength += ADTSHeaderLength

	// ADTS aac_frame_length is a 13-bit field.
	if payloadLength >= (1 << 13) {
		return fmt.Errorf("aacparser: payload length too large: %d (max %d)", payloadLength, (1<<13)-1)
	}

	// AAAAAAAA AAAABCCD EEFFFFGH HHIJKLMM MMMMMMMM MMMOOOOO OOOOOOPP (QQQQQQQQ QQQQQQQQ)
	// header[5] bits 4-0: buffer_fullness[10:6] — set to 0x1f (all ones = VBR)
	// header[6] bits 7-2: buffer_fullness[5:0]  — set to 0xfc (all ones = VBR)
	header[0] = 0xff
	header[1] = 0xf1
	header[5] = 0xff
	header[6] = 0xff

	header[2] = (byte(config.ObjectType-1)&0x3)<<6 |
		(byte(config.SampleRateIndex)&0xf)<<2 | byte(config.ChannelConfig>>2)&0x1
	header[3] = header[3]&0x3f | byte(config.ChannelConfig&0x3)<<6
	header[3] = header[3]&0xfc | byte(payloadLength>>11)&0x3
	header[4] = byte(payloadLength >> 3)
	header[5] = header[5]&0x1f | (byte(payloadLength)&0x7)<<5
	header[6] = header[6]&0xfc | byte(samples/1024-1)

	return nil
}

// objectType is a 5-bit field in AudioSpecificConfig; the escape value 31
// signals an extended type encoded as 31 + 6-bit (so AOTs 32-95 are reachable).
// Don't confuse this with AotEscape (= 39), which is a real object type.
func readObjectType(r *bits.Reader) (objectType uint, err error) {
	if objectType, err = r.ReadBits(5); err != nil {
		return
	}
	const escapeValue = 31
	if objectType == escapeValue {
		var i uint
		if i, err = r.ReadBits(6); err != nil {
			return
		}
		objectType = 32 + i
	}
	return
}

func writeObjectType(w *bits.Writer, objectType uint) (err error) {
	const escapeValue = 31
	if objectType >= 32 {
		if err = w.WriteBits(escapeValue, 5); err != nil {
			return
		}
		if err = w.WriteBits(objectType-32, 6); err != nil {
			return
		}
	} else {
		if err = w.WriteBits(objectType, 5); err != nil {
			return
		}
	}
	return
}

// readSampleRateIndex reads a 4-bit sample rate index. When the escape value
// 0xf is found, it reads an additional 24 bits which encode the actual sample
// rate in Hz (ISO 14496-3 §1.6.5.1). In that case extHz holds the Hz value
// and index is set to 0xf as a sentinel.
func readSampleRateIndex(r *bits.Reader) (index uint, extHz int, err error) {
	if index, err = r.ReadBits(4); err != nil {
		return
	}
	if index == 0xf {
		var hz uint
		if hz, err = r.ReadBits(24); err != nil {
			return
		}
		extHz = int(hz) //nolint:gosec // 24-bit value always fits in int
	}
	return
}

// writeSampleRateIndex writes a 4-bit sample rate index. When index >= 0xf the
// extended form is written: escape (0xf, 4 bits) followed by sampleRate in Hz
// as a 24-bit value (ISO 14496-3 §1.6.5.1).
func writeSampleRateIndex(w *bits.Writer, index uint, sampleRate int) (err error) {
	if index >= 0xf {
		if err = w.WriteBits(0xf, 4); err != nil {
			return
		}
		if err = w.WriteBits(uint(sampleRate), 24); err != nil { //nolint:gosec // sample rate fits in 24 bits
			return
		}
	} else {
		if err = w.WriteBits(index, 4); err != nil {
			return
		}
	}
	return
}

// ParseMPEG4AudioConfigBytes decodes an AudioSpecificConfig (ISO 14496-3 §1.6.2.1).
// Layout: 5b objectType (+6b if escape=31), 4b sampleRateIndex (+24b Hz if escape=0xf),
// 4b channelConfig.
//
// Adapted from libavcodec/mpeg4audio.c:avpriv_mpeg4audio_get_config().
func ParseMPEG4AudioConfigBytes(data []byte) (config MPEG4AudioConfig, err error) {
	if len(data) == 0 {
		return config, fmt.Errorf("aacparser: empty MPEG4 audio config data")
	}

	br := &bits.Reader{R: bytes.NewReader(data)}

	if config.ObjectType, err = readObjectType(br); err != nil {
		if err == io.EOF {
			return config, fmt.Errorf("aacparser: insufficient data for object type: %w", err)
		}
		return
	}

	var extHz int
	if config.SampleRateIndex, extHz, err = readSampleRateIndex(br); err != nil {
		if err == io.EOF {
			return config, fmt.Errorf("aacparser: insufficient data for sample rate index: %w", err)
		}
		return
	}
	if extHz != 0 {
		// Extended form: skip the table lookup in Complete().
		config.SampleRate = extHz
	}

	if config.ChannelConfig, err = br.ReadBits(4); err != nil {
		if err == io.EOF {
			return config, fmt.Errorf("aacparser: insufficient data for channel config: %w", err)
		}
		return
	}

	(&config).Complete()
	return
}

func WriteMPEG4AudioConfig(w io.Writer, config MPEG4AudioConfig) (err error) {
	if w == nil {
		return errors.New("aacparser: writer is nil")
	}

	bw := &bits.Writer{W: w}
	if err = writeObjectType(bw, config.ObjectType); err != nil {
		return
	}

	if config.SampleRateIndex == 0 {
		for i, rate := range sampleRateTable {
			if rate == config.SampleRate {
				config.SampleRateIndex = uint(i) //nolint:gosec // integer overflow for sample rate is not possible
			}
		}
	}
	if err = writeSampleRateIndex(bw, config.SampleRateIndex, config.SampleRate); err != nil {
		return
	}

	if config.ChannelConfig == 0 {
		for i, layout := range chanConfigTable {
			if layout == config.ChannelLayout {
				config.ChannelConfig = uint(i) //nolint:gosec // integer overflow for channel is not possible
			}
		}
	}
	if err = bw.WriteBits(config.ChannelConfig, 4); err != nil {
		return
	}

	if err = bw.FlushBits(); err != nil {
		return
	}

	// SL config + GASpecificConfig terminator suffix (ISO 14496-1 expandable tags).
	if _, err = w.Write([]byte{0x06, 0x80, 0x80, 0x80, 0x01, 0x02, 0x06, 0x80, 0x80, 0x80, 0x01}); err != nil {
		return fmt.Errorf("aacparser: failed to write suffix bytes: %w", err)
	}

	return
}
