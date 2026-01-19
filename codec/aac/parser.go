//nolint:mnd // .
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
	// Check for minimum ADTS header length
	if len(frame) < 7 { //nolint:mnd // 7 is the minimum length of an ADTS header
		err = fmt.Errorf("aacparser: insufficient data for ADTS header, need at least 7 bytes, got %d", len(frame))
		return
	}

	// Check ADTS sync word
	if frame[0] != 0xff || frame[1]&0xf6 != 0xf0 {
		err = fmt.Errorf("aacparser: invalid ADTS sync word: %02x %02x", frame[0], frame[1])
		return
	}

	// Extract AAC object type, sample rate index, and channel configuration
	config.ObjectType = uint(frame[2]>>6) + 1          //nolint:mnd // 6 is the number of bits for the object type
	config.SampleRateIndex = uint(frame[2] >> 2 & 0xf) //nolint:mnd // 2 is the number of bits for the sample rate index
	config.ChannelConfig = uint(frame[2]<<2&0x4 | frame[3]>>6&0x3)

	// Validate sample rate index
	if config.SampleRateIndex >= uint(len(sampleRateTable)) {
		err = fmt.Errorf("aacparser: invalid sample rate index: %d", config.SampleRateIndex)
		return
	}

	// Validate channel configuration
	if config.ChannelConfig == uint(0) || config.ChannelConfig >= uint(len(chanConfigTable)) {
		err = fmt.Errorf("aacparser: invalid channel configuration: %d", config.ChannelConfig)
		return
	}

	// Complete the configuration
	(&config).Complete()

	// Extract frame length
	framelen = int(frame[3]&0x3)<<11 | int(frame[4])<<3 | int(frame[5]>>5) //nolint:mnd,lll // 3 is the number of bits for the frame length

	// Extract number of samples
	samples = (int(frame[6]&0x3) + 1) * 1024 //nolint:mnd // 3 is the number of bits for the number of samples

	// Determine header length based on protection bit
	hdrlen = 7
	if frame[1]&0x1 == 0 {
		hdrlen = 9
		// Ensure we have enough data for the longer header
		if len(frame) < 9 { //nolint:mnd // 9 is the length of the protected ADTS header
			err = fmt.Errorf("aacparser: insufficient data for protected ADTS header, need 9 bytes, got %d", len(frame))
			return
		}
	}

	// Validate frame length
	if framelen < hdrlen {
		err = fmt.Errorf("aacparser: invalid ADTS frame length: %d (must be >= %d)", framelen, hdrlen)
		return
	}

	return
}

const ADTSHeaderLength = 7

func FillADTSHeader(header []byte, config MPEG4AudioConfig, samples int, payloadLength int) error {
	// Ensure header array is large enough
	if len(header) < ADTSHeaderLength {
		return fmt.Errorf("aacparser: header buffer too small, needs at least %d bytes, got %d",
			ADTSHeaderLength, len(header))
	}

	// Validate parameters
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

	// Calculate frame length - cannot exceed 13-bit field size
	if payloadLength >= (1 << 13) {
		return fmt.Errorf("aacparser: payload length too large: %d (max %d)", payloadLength, (1<<13)-1)
	}

	// AAAAAAAA AAAABCCD EEFFFFGH HHIJKLMM MMMMMMMM MMMOOOOO OOOOOOPP (QQQQQQQQ QQQQQQQQ)
	header[0] = 0xff
	header[1] = 0xf1
	header[2] = 0x50
	header[3] = 0x80
	header[4] = 0x43
	header[5] = 0xff
	header[6] = 0xcd

	header[2] = (byte(config.ObjectType-1)&0x3)<<6 |
		(byte(config.SampleRateIndex)&0xf)<<2 | byte(config.ChannelConfig>>2)&0x1
	header[3] = header[3]&0x3f | byte(config.ChannelConfig&0x3)<<6
	header[3] = header[3]&0xfc | byte(payloadLength>>11)&0x3
	header[4] = byte(payloadLength >> 3)
	header[5] = header[5]&0x1f | (byte(payloadLength)&0x7)<<5
	header[6] = header[6]&0xfc | byte(samples/1024-1)

	return nil
}

func readObjectType(r *bits.Reader) (objectType uint, err error) {
	if objectType, err = r.ReadBits(5); err != nil {
		return
	}
	// Escape value is 31 (0x1f), not AotEscape constant (which is object type 43)
	const escapeValue = 31
	if objectType == escapeValue {
		var i uint
		if i, err = r.ReadBits(6); err != nil {
			return
		}
		// Extended object type: escape (31) + 6-bit value
		// Object type = 32 + 6-bit value
		objectType = 32 + i
	}
	return
}

func writeObjectType(w *bits.Writer, objectType uint) (err error) {
	if objectType >= 32 {
		// Extended object type: write escape (31) + 6-bit value (objectType - 32)
		// Note: escape value is 31 (0x1f), not AotEscape constant (which is object type 43)
		const escapeValue = 31
		if err = w.WriteBits(escapeValue, 5); err != nil {
			return
		}
		if err = w.WriteBits(objectType-32, 6); err != nil {
			return
		}
	} else {
		// Standard object type: write directly as 5-bit value
		if err = w.WriteBits(objectType, 5); err != nil {
			return
		}
	}
	return
}

func readSampleRateIndex(r *bits.Reader) (index uint, err error) {
	if index, err = r.ReadBits(4); err != nil {
		return
	}
	if index == 0xf {
		if index, err = r.ReadBits(24); err != nil {
			return
		}
	}
	return
}

func writeSampleRateIndex(w *bits.Writer, index uint) (err error) {
	if index >= 0xf {
		if err = w.WriteBits(0xf, 4); err != nil {
			return
		}
		if err = w.WriteBits(index, 24); err != nil {
			return
		}
	} else {
		if err = w.WriteBits(index, 4); err != nil {
			return
		}
	}
	return
}

func ParseMPEG4AudioConfigBytes(data []byte) (config MPEG4AudioConfig, err error) {
	// Copied from libavcodec/mpeg4audio.c avpriv_mpeg4audio_get_config()
	// Validate minimum data length before parsing
	// Minimum: 5 bits (object type) + 4 bits (sample rate) + 4 bits (channel config) = 13 bits = 2 bytes
	// But if object type is escape (31), we need 5 + 6 = 11 more bits = 3 bytes total
	// If sample rate is extended (0xf), we need 4 + 24 = 28 more bits = 4 bytes total
	// So worst case: 5 + 6 + 4 + 24 + 4 = 43 bits = 6 bytes minimum
	if len(data) == 0 {
		return config, fmt.Errorf("aacparser: empty MPEG4 audio config data")
	}

	r := bytes.NewReader(data)
	br := &bits.Reader{R: r}

	// Read object type (5 bits, or 5+6 if extended)
	if config.ObjectType, err = readObjectType(br); err != nil {
		if err == io.EOF {
			return config, fmt.Errorf("aacparser: insufficient data for object type: %w", err)
		}
		return
	}

	// Read sample rate index (4 bits, or 4+24 if extended)
	if config.SampleRateIndex, err = readSampleRateIndex(br); err != nil {
		if err == io.EOF {
			return config, fmt.Errorf("aacparser: insufficient data for sample rate index: %w", err)
		}
		return
	}

	// Read channel config (4 bits)
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
	if err = writeSampleRateIndex(bw, config.SampleRateIndex); err != nil {
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

	// Write fixed suffix bytes
	if _, err = w.Write([]byte{0x06, 0x80, 0x80, 0x80, 0x01, 0x02, 0x06, 0x80, 0x80, 0x80, 0x01}); err != nil {
		return fmt.Errorf("aacparser: failed to write suffix bytes: %w", err)
	}

	return
}
