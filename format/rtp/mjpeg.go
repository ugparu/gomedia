package rtp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sort"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/mjpeg"
	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/sdp"
)

// MJPEG RTP header constants
const (
	mjpegHeaderSize   = 8     // Main JPEG header size
	restartHeaderSize = 4     // Restart marker header size
	qtableHeaderSize  = 4     // Quantization table header size
	jpegClockRate     = 90000 // 90kHz RTP clock for video
)

// mjpegFragment represents an MJPEG fragment with its byte offset in the frame
type mjpegFragment struct {
	fragOffset uint32
	data       []byte
}

// cachedQTable stores quantization table data for reuse with Q values 128-254
// (static mapping per RFC 2435)
type cachedQTable struct {
	precision uint8
	data      []byte
}

// mjpegDemuxer handles MJPEG RTP demuxing according to RFC 2435
type mjpegDemuxer struct {
	*baseDemuxer
	codec           *mjpeg.CodecParameters
	packets         []*mjpeg.Packet
	fragments       []mjpegFragment
	timestamp       uint32
	width           uint8
	height          uint8
	frameType       uint8
	quality         uint8
	markerBit       bool
	lastTimestamp   uint32
	frameHeaders    []byte             // Reconstructed JPEG headers for current frame
	restartInterval uint16             // DRI restart interval
	qtablePrecision uint8              // Precision field from Q-table header
	cachedQTables   map[uint8]cachedQTable // Cached tables for Q 128-254
}

// NewMJPEGDemuxer creates a new MJPEG RTP demuxer
func NewMJPEGDemuxer(rdr io.Reader, sdp sdp.Media, index uint8, options ...DemuxerOption) gomedia.Demuxer {
	return &mjpegDemuxer{
		baseDemuxer:   newBaseDemuxer(rdr, sdp, index, options...),
		packets:       []*mjpeg.Packet{},
		fragments:     []mjpegFragment{},
		cachedQTables: make(map[uint8]cachedQTable),
	}
}

func (d *mjpegDemuxer) Close() {
	for _, pkt := range d.packets {
		if pkt.Slot != nil {
			pkt.Slot.Release()
		}
	}
	d.packets = nil
}

// Demux returns the codec parameters for MJPEG
func (d *mjpegDemuxer) Demux() (codecs gomedia.CodecParametersPair, err error) {
	// Get framerate from SDP if available, otherwise default to 30
	fps := uint(30) //nolint:mnd // default framerate
	if d.sdp.FPS > 0 {
		fps = uint(d.sdp.FPS)
	}

	// Create initial codec parameters with default dimensions;
	// updated when we receive the first packet with actual dimensions
	d.codec = mjpeg.NewCodecParameters(320, 240, fps) //nolint:mnd // default dimensions
	d.codec.SetStreamIndex(d.index)

	codecs.VideoCodecParameters = d.codec
	return
}

// ReadPacket reads and processes RTP/JPEG packets
func (d *mjpegDemuxer) ReadPacket() (pkt gomedia.Packet, err error) {
	// Return any buffered packets first
	if len(d.packets) > 0 {
		pkt = d.packets[0]
		d.packets = d.packets[1:]
		return
	}

	// Read RTP packet
	if _, err = d.baseDemuxer.ReadPacket(); err != nil {
		return
	}

	// Extract RTP marker bit (bit 7 of second RTP header byte)
	d.markerBit = (d.baseDemuxer.payload.Data()[5] & 0x80) != 0 //nolint:mnd

	// Parse MJPEG RTP payload
	if err = d.parseMJPEGPacket(); err != nil {
		return
	}

	// Return packet if we have one ready
	if len(d.packets) > 0 {
		pkt = d.packets[0]
		d.packets = d.packets[1:]
	}

	return
}

// parseMJPEGPacket parses an MJPEG RTP packet according to RFC 2435
func (d *mjpegDemuxer) parseMJPEGPacket() error {
	if d.end-d.offset < mjpegHeaderSize {
		return errors.New("incomplete MJPEG header")
	}

	// Parse main JPEG header (8 bytes, present in every fragment)
	headerData := d.payload.Data()[d.offset : d.offset+mjpegHeaderSize]

	// typeSpecific := headerData[0]
	fragOffset := binary.BigEndian.Uint32([]byte{0, headerData[1], headerData[2], headerData[3]}) //nolint:mnd // 24-bit fragment offset
	mjpegType := headerData[4]
	quality := headerData[5]
	width := headerData[6]
	height := headerData[7]

	d.offset += mjpegHeaderSize

	// Restart marker header (types 64-127, present in every fragment)
	if mjpegType >= 64 && mjpegType <= 127 { //nolint:mnd
		if d.end-d.offset < restartHeaderSize {
			return errors.New("incomplete restart marker header")
		}

		restartData := d.payload.Data()[d.offset : d.offset+restartHeaderSize]
		d.restartInterval = binary.BigEndian.Uint16(restartData[0:2])
		// Bytes 2-3 contain F, L, and Restart Count fields used for partial
		// decoding of restart intervals. We reassemble full frames using
		// fragment offsets, so these fields are not needed.

		d.offset += restartHeaderSize
	}

	// Quantization table header (Q 128-255, only in first packet per RFC 2435 Section 3.1.8)
	var qtableData []byte
	var qtablePrecision uint8
	if quality >= 128 && fragOffset == 0 { //nolint:mnd
		if d.end-d.offset < qtableHeaderSize {
			return errors.New("incomplete quantization table header")
		}

		qtableHeader := d.payload.Data()[d.offset : d.offset+qtableHeaderSize]
		// MBZ := qtableHeader[0] // Must be zero
		qtablePrecision = qtableHeader[1]
		qtableLength := binary.BigEndian.Uint16(qtableHeader[2:4])

		d.offset += qtableHeaderSize

		if qtableLength > 0 {
			if d.end-d.offset < int(qtableLength) {
				return errors.New("incomplete quantization table data")
			}
			qtableData = make([]byte, qtableLength)
			copy(qtableData, d.payload.Data()[d.offset:d.offset+int(qtableLength)])
			d.offset += int(qtableLength)

			// Cache tables for Q 128-254 (static mapping per RFC 2435)
			if quality < 255 { //nolint:mnd
				d.cachedQTables[quality] = cachedQTable{
					precision: qtablePrecision,
					data:      qtableData,
				}
			}
		} else if quality < 255 { //nolint:mnd
			// Length=0: use previously cached tables for this Q value
			if cached, ok := d.cachedQTables[quality]; ok {
				qtableData = cached.data
				qtablePrecision = cached.precision
			}
		}
		// Q=255 with Length=0 is invalid per RFC; silently fall through
		// to computed tables as a best-effort degradation.
	}

	// Handle frame fragmentation with proper ordering
	if fragOffset == 0 {
		// Start of new frame - clear any previous fragments
		d.fragments = d.fragments[:0]
		d.timestamp = d.baseDemuxer.timestamp
		d.lastTimestamp = d.baseDemuxer.timestamp
		d.width = width
		d.height = height
		d.frameType = mjpegType
		d.quality = quality
		d.qtablePrecision = qtablePrecision

		// Update codec parameters with actual frame dimensions
		if d.codec != nil {
			actualWidth := uint(width) * 8  //nolint:mnd // width is in 8-pixel blocks
			actualHeight := uint(height) * 8 //nolint:mnd // height is in 8-pixel blocks
			if actualWidth != d.codec.Width() || actualHeight != d.codec.Height() {
				d.codec = mjpeg.NewCodecParameters(actualWidth, actualHeight, d.codec.FPS())
				d.codec.SetStreamIndex(d.index)
			}
		}

		// Generate JPEG headers for this frame
		d.frameHeaders = reconstructJPEGHeaders(mjpegType, width, height, quality,
			d.restartInterval, qtablePrecision, qtableData)
	} else {
		// Continuation of existing frame
		if d.lastTimestamp != d.baseDemuxer.timestamp {
			// New frame with different timestamp but no start fragment - discard
			d.fragments = d.fragments[:0]
			return nil
		}
	}

	// Add this fragment to our collection
	payloadData := make([]byte, d.end-d.offset)
	copy(payloadData, d.payload.Data()[d.offset:d.end])

	d.fragments = append(d.fragments, mjpegFragment{
		fragOffset: fragOffset,
		data:       payloadData,
	})

	// If this is the last fragment (RTP marker bit set), assemble the complete frame
	if d.markerBit {
		if err := d.assembleFrame(); err != nil {
			return err
		}
	}

	return nil
}

// assembleFrame assembles all fragments into a complete JPEG frame
func (d *mjpegDemuxer) assembleFrame() error {
	if len(d.fragments) == 0 {
		return errors.New("no fragments to assemble")
	}

	// Sort fragments by offset to ensure correct order
	sort.Slice(d.fragments, func(i, j int) bool {
		return d.fragments[i].fragOffset < d.fragments[j].fragOffset
	})

	// Check that we have the first fragment (offset 0)
	if d.fragments[0].fragOffset != 0 {
		// Missing start of frame, discard
		d.fragments = d.fragments[:0]
		return nil
	}

	// Calculate total size for allocation
	totalSize := len(d.frameHeaders)
	for _, frag := range d.fragments {
		totalSize += len(frag.data)
	}
	totalSize += 2 //nolint:mnd // space for potential EOI marker

	// Allocate from ring buffer or fall back to heap
	var data []byte
	var handle *buffer.SlotHandle

	if d.ring != nil {
		data, handle = d.ring.Alloc(totalSize)
	}
	if data == nil {
		data = make([]byte, totalSize)
	}

	// Write JPEG headers
	off := copy(data, d.frameHeaders)

	// Write all fragment data in order, checking for gaps
	expectedOffset := uint32(0)
	for _, frag := range d.fragments {
		if frag.fragOffset != expectedOffset {
			// Gap in fragments, discard frame
			d.fragments = d.fragments[:0]
			handle.Release() // no-op if nil
			return nil
		}
		off += copy(data[off:], frag.data)
		expectedOffset += uint32(len(frag.data))
	}

	// Add EOI marker if not present
	if off < 2 || data[off-2] != 0xFF || data[off-1] != 0xD9 { //nolint:mnd // EOI = FF D9
		data[off] = 0xFF
		data[off+1] = 0xD9 //nolint:mnd // EOI marker
		off += 2            //nolint:mnd
	}
	data = data[:off]

	// Create MJPEG packet
	ts := (time.Duration(d.timestamp) * time.Second) / time.Duration(jpegClockRate)

	packet := mjpeg.NewPacket(
		true, // MJPEG frames are always keyframes
		ts,
		time.Now(),
		data,
		"",
		d.codec,
	)
	packet.Slot = handle // nil for heap-backed packets; Release() is a no-op

	d.packets = append(d.packets, packet)
	d.fragments = d.fragments[:0]
	d.restartInterval = 0

	return nil
}

// reconstructJPEGHeaders reconstructs JPEG headers according to RFC 2435 Appendix B
func reconstructJPEGHeaders(mjpegType, width, height, quality uint8,
	restartInterval uint16, precision uint8, qtableData []byte) []byte {
	var headers bytes.Buffer

	// SOI marker
	headers.Write([]byte{0xFF, 0xD8}) //nolint:mnd

	// APP0 JFIF segment for compatibility
	headers.Write(createAPP0Segment())

	// Quantization tables
	if quality >= 128 && len(qtableData) > 0 { //nolint:mnd
		// Wrap raw table data from RTP Q-table header in DQT segments
		headers.Write(createDQTFromRawTables(precision, qtableData))
	} else {
		// Generate standard quantization tables based on quality factor
		lqt, cqt := generateQuantizationTables(quality)
		headers.Write(createDQTSegment(lqt, 0))
		headers.Write(createDQTSegment(cqt, 1))
	}

	// Add DRI segment if restart interval is present
	if restartInterval > 0 {
		headers.Write(createDRISegment(restartInterval))
	}

	// SOF0 (Start of Frame - Baseline DCT)
	headers.Write(createSOFSegment(mjpegType, width, height))

	// Huffman tables - all 4 standard tables (ITU-T T.81 Annex K)
	headers.Write(createHuffmanTables())

	// SOS (Start of Scan)
	headers.Write(createSOSSegment())

	return headers.Bytes()
}

// createAPP0Segment creates a standard JFIF APP0 segment
func createAPP0Segment() []byte {
	segment := make([]byte, 18) //nolint:mnd
	segment[0] = 0xFF
	segment[1] = 0xE0                      // APP0
	segment[2] = 0x00                      // Length MSB
	segment[3] = 0x10                      // Length LSB (16 bytes)
	copy(segment[4:9], []byte("JFIF\x00")) // JFIF identifier
	segment[9] = 0x01                      // Version major
	segment[10] = 0x01                     // Version minor
	segment[11] = 0x01                     // Density units (dpi)
	segment[12] = 0x00                     // X density MSB
	segment[13] = 0x48                     // X density LSB (72 dpi)
	segment[14] = 0x00                     // Y density MSB
	segment[15] = 0x48                     // Y density LSB (72 dpi)
	segment[16] = 0x00                     // Thumbnail width
	segment[17] = 0x00                     // Thumbnail height
	return segment
}

// generateQuantizationTables generates standard JPEG quantization tables
// using the algorithm from RFC 2435 Appendix A (MakeTables procedure)
func generateQuantizationTables(quality uint8) ([]byte, []byte) {
	// Standard JPEG quantization tables (JPEG Annex K, Tables K.1 and K.2)
	jpegLumaQuantizer := [64]int{ //nolint:mnd
		16, 11, 10, 16, 24, 40, 51, 61,
		12, 12, 14, 19, 26, 58, 60, 55,
		14, 13, 16, 24, 40, 57, 69, 56,
		14, 17, 22, 29, 51, 87, 80, 62,
		18, 22, 37, 56, 68, 109, 103, 77,
		24, 35, 55, 64, 81, 104, 113, 92,
		49, 64, 78, 87, 103, 121, 120, 101,
		72, 92, 95, 98, 112, 100, 103, 99,
	}

	jpegChromaQuantizer := [64]int{ //nolint:mnd
		17, 18, 24, 47, 99, 99, 99, 99,
		18, 21, 26, 66, 99, 99, 99, 99,
		24, 26, 56, 99, 99, 99, 99, 99,
		47, 66, 99, 99, 99, 99, 99, 99,
		99, 99, 99, 99, 99, 99, 99, 99,
		99, 99, 99, 99, 99, 99, 99, 99,
		99, 99, 99, 99, 99, 99, 99, 99,
		99, 99, 99, 99, 99, 99, 99, 99,
	}

	// Calculate scale factor per RFC 2435
	var scaleFactor int
	if quality < 1 {
		scaleFactor = 5000 //nolint:mnd
	} else if quality > 99 { //nolint:mnd
		scaleFactor = 1
	} else if quality < 50 { //nolint:mnd
		scaleFactor = 5000 / int(quality) //nolint:mnd
	} else {
		scaleFactor = 200 - 2*int(quality) //nolint:mnd
	}

	// Scale and clamp quantization tables
	lqt := make([]byte, 64) //nolint:mnd
	cqt := make([]byte, 64) //nolint:mnd

	for i := 0; i < 64; i++ { //nolint:mnd
		lq := (jpegLumaQuantizer[i]*scaleFactor + 50) / 100 //nolint:mnd
		cq := (jpegChromaQuantizer[i]*scaleFactor + 50) / 100 //nolint:mnd

		if lq < 1 {
			lq = 1
		} else if lq > 255 { //nolint:mnd
			lq = 255 //nolint:mnd
		}

		if cq < 1 {
			cq = 1
		} else if cq > 255 { //nolint:mnd
			cq = 255 //nolint:mnd
		}

		lqt[i] = byte(lq)
		cqt[i] = byte(cq)
	}

	return lqt, cqt
}

// createDQTSegment creates a Define Quantization Table segment for 8-bit coefficients
func createDQTSegment(table []byte, tableID uint8) []byte {
	// 2(marker) + 67(Lq: 2 length + 1 PqTq + 64 table data)
	segment := make([]byte, 69) //nolint:mnd
	segment[0] = 0xFF
	segment[1] = 0xDB // DQT
	segment[2] = 0x00 // Length MSB
	segment[3] = 0x43 // Length LSB (67 = 2 + 1 + 64)
	segment[4] = tableID
	copy(segment[5:], table)
	return segment
}

// createDQTFromRawTables wraps raw quantization table coefficients from the RTP
// Q-table header into JPEG DQT marker segments. The precision field is a bitmask
// indicating coefficient size per table: 0 = 8-bit (64 bytes), 1 = 16-bit (128 bytes).
// MSB corresponds to the first table (RFC 2435 Section 3.1.8).
func createDQTFromRawTables(precision uint8, tableData []byte) []byte {
	var result bytes.Buffer
	offset := 0
	tableID := uint8(0)

	for offset < len(tableData) && tableID < 4 { //nolint:mnd
		is16bit := (precision & (1 << (7 - tableID))) != 0 //nolint:mnd

		coeffSize := 64 //nolint:mnd // 8-bit: 64 bytes per table
		if is16bit {
			coeffSize = 128 //nolint:mnd // 16-bit: 128 bytes per table
		}

		if offset+coeffSize > len(tableData) {
			break
		}

		// PqTq byte: upper nibble = precision (0 or 1), lower nibble = table destination ID
		pqTq := tableID
		if is16bit {
			pqTq |= 0x10 //nolint:mnd
		}

		lq := uint16(2 + 1 + coeffSize) //nolint:mnd // Lq includes the 2-byte length field itself
		segment := make([]byte, 2+int(lq))
		segment[0] = 0xFF
		segment[1] = 0xDB // DQT
		binary.BigEndian.PutUint16(segment[2:4], lq)
		segment[4] = pqTq
		copy(segment[5:], tableData[offset:offset+coeffSize])

		result.Write(segment)
		offset += coeffSize
		tableID++
	}

	return result.Bytes()
}

// createDRISegment creates a Define Restart Interval segment
func createDRISegment(restartInterval uint16) []byte {
	segment := make([]byte, 6) //nolint:mnd
	segment[0] = 0xFF
	segment[1] = 0xDD // DRI marker
	segment[2] = 0x00 // Length MSB
	segment[3] = 0x04 // Length LSB (4 = 2 + 2)
	binary.BigEndian.PutUint16(segment[4:6], restartInterval)
	return segment
}

// createSOFSegment creates a Start of Frame (Baseline DCT) segment with
// sampling factors determined by the RFC 2435 Type field
func createSOFSegment(mjpegType, width, height uint8) []byte {
	segment := make([]byte, 19) //nolint:mnd
	segment[0] = 0xFF
	segment[1] = 0xC0 // SOF0
	segment[2] = 0x00 // Length MSB
	segment[3] = 0x11 // Length LSB (17 = 2 + 1 + 2 + 2 + 1 + 3*3)
	segment[4] = 0x08 // 8-bit sample precision

	// Height and width in pixels (RTP values are in 8-pixel blocks)
	actualHeight := uint16(height) * 8 //nolint:mnd
	actualWidth := uint16(width) * 8   //nolint:mnd
	binary.BigEndian.PutUint16(segment[5:7], actualHeight)
	binary.BigEndian.PutUint16(segment[7:9], actualWidth)

	segment[9] = 0x03 // Number of components (Y, Cb, Cr)

	// Component 1 (Y) - sampling factors per RFC 2435 Type mapping
	segment[10] = 0x01                    // Component ID
	mjpegTypeBase := mjpegType & 0x3F     //nolint:mnd // lower 6 bits determine the base type
	switch mjpegTypeBase {
	case 0: // 4:2:2 subsampling
		segment[11] = 0x21 // hsamp=2, vsamp=1
	case 1: // 4:2:0 subsampling
		segment[11] = 0x22 // hsamp=2, vsamp=2
	case 2: // 4:4:4 or grayscale //nolint:mnd
		segment[11] = 0x11 // hsamp=1, vsamp=1
	default: // default to 4:2:0
		segment[11] = 0x22
	}
	segment[12] = 0x00 // Quantization table 0

	// Component 2 (Cb)
	segment[13] = 0x02 // Component ID
	segment[14] = 0x11 // hsamp=1, vsamp=1
	segment[15] = 0x01 // Quantization table 1

	// Component 3 (Cr)
	segment[16] = 0x03 // Component ID
	segment[17] = 0x11 // hsamp=1, vsamp=1
	segment[18] = 0x01 // Quantization table 1

	return segment
}

// createHuffmanTables creates all 4 standard JPEG Huffman tables (ITU-T T.81 Annex K)
func createHuffmanTables() []byte {
	var tables bytes.Buffer

	// Luma DC table (Table K.3)
	lumDCCodeLens := []byte{0, 1, 5, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0}
	lumDCSymbols := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	tables.Write(createDHTSegment(lumDCCodeLens, lumDCSymbols, 0, 0))

	// Luma AC table (Table K.5)
	lumACCodeLens := []byte{0, 2, 1, 3, 3, 2, 4, 3, 5, 5, 4, 4, 0, 0, 1, 0x7d}
	lumACSymbols := []byte{
		0x01, 0x02, 0x03, 0x00, 0x04, 0x11, 0x05, 0x12,
		0x21, 0x31, 0x41, 0x06, 0x13, 0x51, 0x61, 0x07,
		0x22, 0x71, 0x14, 0x32, 0x81, 0x91, 0xa1, 0x08,
		0x23, 0x42, 0xb1, 0xc1, 0x15, 0x52, 0xd1, 0xf0,
		0x24, 0x33, 0x62, 0x72, 0x82, 0x09, 0x0a, 0x16,
		0x17, 0x18, 0x19, 0x1a, 0x25, 0x26, 0x27, 0x28,
		0x29, 0x2a, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39,
		0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49,
		0x4a, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59,
		0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69,
		0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79,
		0x7a, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89,
		0x8a, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98,
		0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7,
		0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6,
		0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3, 0xc4, 0xc5,
		0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3, 0xd4,
		0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe1, 0xe2,
		0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9, 0xea,
		0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8,
		0xf9, 0xfa,
	}
	tables.Write(createDHTSegment(lumACCodeLens, lumACSymbols, 0, 1))

	// Chroma DC table (Table K.4)
	chrDCCodeLens := []byte{0, 3, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0}
	chrDCSymbols := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	tables.Write(createDHTSegment(chrDCCodeLens, chrDCSymbols, 1, 0))

	// Chroma AC table (Table K.6)
	chrACCodeLens := []byte{0, 2, 1, 2, 4, 4, 3, 4, 7, 5, 4, 4, 0, 1, 2, 0x77}
	chrACSymbols := []byte{
		0x00, 0x01, 0x02, 0x03, 0x11, 0x04, 0x05, 0x21,
		0x31, 0x06, 0x12, 0x41, 0x51, 0x07, 0x61, 0x71,
		0x13, 0x22, 0x32, 0x81, 0x08, 0x14, 0x42, 0x91,
		0xa1, 0xb1, 0xc1, 0x09, 0x23, 0x33, 0x52, 0xf0,
		0x15, 0x62, 0x72, 0xd1, 0x0a, 0x16, 0x24, 0x34,
		0xe1, 0x25, 0xf1, 0x17, 0x18, 0x19, 0x1a, 0x26,
		0x27, 0x28, 0x29, 0x2a, 0x35, 0x36, 0x37, 0x38,
		0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48,
		0x49, 0x4a, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58,
		0x59, 0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68,
		0x69, 0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78,
		0x79, 0x7a, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87,
		0x88, 0x89, 0x8a, 0x92, 0x93, 0x94, 0x95, 0x96,
		0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5,
		0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4,
		0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3,
		0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2,
		0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda,
		0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9,
		0xea, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8,
		0xf9, 0xfa,
	}
	tables.Write(createDHTSegment(chrACCodeLens, chrACSymbols, 1, 1))

	return tables.Bytes()
}

// createDHTSegment creates a Define Huffman Table segment.
// Lh includes the 2-byte length field itself per JPEG spec.
func createDHTSegment(codeLens, symbols []byte, tableID, tableClass uint8) []byte {
	lh := 2 + 1 + len(codeLens) + len(symbols) // Lh includes itself (2) + TcTh (1) + data
	segment := make([]byte, 2+lh)               // 2(marker) + Lh
	segment[0] = 0xFF
	segment[1] = 0xC4 // DHT
	binary.BigEndian.PutUint16(segment[2:4], uint16(lh))
	segment[4] = (tableClass << 4) | tableID //nolint:mnd
	copy(segment[5:], codeLens)
	copy(segment[5+len(codeLens):], symbols)
	return segment
}

// createSOSSegment creates a Start of Scan segment with correct Huffman table references
func createSOSSegment() []byte {
	segment := make([]byte, 14) //nolint:mnd
	segment[0] = 0xFF
	segment[1] = 0xDA // SOS
	segment[2] = 0x00 // Length MSB
	segment[3] = 0x0C // Length LSB (12 = 2 + 1 + 3*2 + 3)
	segment[4] = 0x03 // Number of components

	// Component 1 (Y) - use Luma DC/AC tables (0/0)
	segment[5] = 0x01 // Component ID
	segment[6] = 0x00 // DC table 0, AC table 0

	// Component 2 (Cb) - use Chroma DC/AC tables (1/1)
	segment[7] = 0x02 // Component ID
	segment[8] = 0x11 // DC table 1, AC table 1

	// Component 3 (Cr) - use Chroma DC/AC tables (1/1)
	segment[9] = 0x03  // Component ID
	segment[10] = 0x11 // DC table 1, AC table 1

	segment[11] = 0x00 // Start of spectral selection (Ss)
	segment[12] = 0x3F // End of spectral selection (Se = 63)
	segment[13] = 0x00 // Successive approximation (Ah=0, Al=0)

	return segment
}
