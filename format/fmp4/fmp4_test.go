//nolint:mnd // Test file contains many magic numbers for expected values
package fmp4

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/format/mp4/mp4io"
	"github.com/ugparu/gomedia/utils/bits/pio"
	"github.com/ugparu/gomedia/utils/logger"
)

const testDataDir = "../../tests/data/h264_aac/"

// ---------------------------------------------------------------------------
// Test data helpers
// ---------------------------------------------------------------------------

type parametersJSON struct {
	URL   string          `json:"url"`
	Video *videoParamJSON `json:"video,omitempty"`
	Audio *audioParamJSON `json:"audio,omitempty"`
}

type videoParamJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	Record      string `json:"record,omitempty"`
}

type audioParamJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	Config      string `json:"config,omitempty"`
}

type packetJSON struct {
	Codec       string `json:"codec"`
	StreamIndex uint8  `json:"stream_index"`
	TimestampNs int64  `json:"timestamp_ns"`
	DurationNs  int64  `json:"duration_ns"`
	IsKeyframe  bool   `json:"is_keyframe,omitempty"`
	Size        int    `json:"size"`
	Data        string `json:"data"`
}

type packetsJSON struct {
	Packets []packetJSON `json:"packets"`
}

func loadTestCodecPair(t *testing.T) (gomedia.CodecParametersPair, *h264.CodecParameters, *aac.CodecParameters) {
	t.Helper()
	raw, err := os.ReadFile(testDataDir + "parameters.json")
	require.NoError(t, err)

	var params parametersJSON
	require.NoError(t, json.Unmarshal(raw, &params))
	require.NotNil(t, params.Video)
	require.NotNil(t, params.Audio)

	recordBytes, err := base64.StdEncoding.DecodeString(params.Video.Record)
	require.NoError(t, err)
	videoCp, err := h264.NewCodecDataFromAVCDecoderConfRecord(recordBytes)
	require.NoError(t, err)
	videoCp.SetStreamIndex(params.Video.StreamIndex)

	configBytes, err := base64.StdEncoding.DecodeString(params.Audio.Config)
	require.NoError(t, err)
	audioCp, err := aac.NewCodecDataFromMPEG4AudioConfigBytes(configBytes)
	require.NoError(t, err)
	audioCp.SetStreamIndex(params.Audio.StreamIndex)

	pair := gomedia.CodecParametersPair{
		SourceID:             "test",
		VideoCodecParameters: &videoCp,
		AudioCodecParameters: &audioCp,
	}
	return pair, &videoCp, &audioCp
}

func loadTestPackets(t *testing.T, videoCp *h264.CodecParameters, audioCp *aac.CodecParameters, limit int) []gomedia.Packet {
	t.Helper()
	raw, err := os.ReadFile(testDataDir + "packets.json")
	require.NoError(t, err)

	var pkts packetsJSON
	require.NoError(t, json.Unmarshal(raw, &pkts))

	absBase := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	var result []gomedia.Packet
	for i, entry := range pkts.Packets {
		if i >= limit {
			break
		}
		data, decErr := base64.StdEncoding.DecodeString(entry.Data)
		require.NoError(t, decErr)
		ts := time.Duration(entry.TimestampNs)
		dur := time.Duration(entry.DurationNs)
		switch entry.Codec {
		case "H264":
			pkt := h264.NewPacket(entry.IsKeyframe, ts, absBase, data, "test", videoCp)
			pkt.SetDuration(dur)
			result = append(result, pkt)
		case "AAC":
			result = append(result, aac.NewPacket(data, ts, "test", absBase, audioCp, dur))
		default:
			t.Fatalf("unexpected codec: %s", entry.Codec)
		}
	}
	return result
}

// readTag extracts an MP4 box tag at offset.
func readTag(b []byte, offset int) mp4io.Tag {
	return mp4io.Tag(pio.U32BE(b[offset+4:]))
}

// readBoxSize extracts an MP4 box size at offset.
func readBoxSize(b []byte, offset int) uint32 {
	return pio.U32BE(b[offset:])
}

// parseInitSegment parses ftyp + moov from init segment bytes.
func parseInitSegment(t *testing.T, data []byte) (*mp4io.FileType, *mp4io.Movie) {
	t.Helper()
	require.True(t, len(data) > 16, "init segment too short")

	ftypSize := int(readBoxSize(data, 0))
	require.Equal(t, mp4io.FTYP, readTag(data, 0), "first box must be ftyp")

	ftyp := &mp4io.FileType{}
	_, err := ftyp.Unmarshal(data[:ftypSize], 0)
	require.NoError(t, err)

	moovStart := ftypSize
	require.Equal(t, mp4io.MOOV, readTag(data, moovStart), "second box must be moov")

	moov := &mp4io.Movie{}
	_, err = moov.Unmarshal(data[moovStart:], moovStart)
	require.NoError(t, err)

	return ftyp, moov
}

// parseFragment walks styp → sidx(es) → moof → mdat and returns the moof and mdat payload.
func parseFragment(t *testing.T, data []byte) (*mp4io.MovieFrag, []byte) {
	t.Helper()
	n := 0

	// styp
	require.Equal(t, mp4io.STYP, readTag(data, n), "first box must be styp")
	n += int(readBoxSize(data, n))

	// sidx boxes
	for n+8 <= len(data) && readTag(data, n) == mp4io.SIDX {
		n += int(readBoxSize(data, n))
	}

	// moof
	require.Equal(t, mp4io.MOOF, readTag(data, n), "expected moof after sidx")
	moofSize := int(readBoxSize(data, n))
	moof := &mp4io.MovieFrag{}
	_, err := moof.Unmarshal(data[n:n+moofSize], n)
	require.NoError(t, err)
	n += moofSize

	// mdat
	require.Equal(t, mp4io.MDAT, readTag(data, n), "expected mdat after moof")
	mdatSize := int(readBoxSize(data, n))
	mdatPayload := data[n+8 : n+mdatSize] // skip size+tag
	return moof, mdatPayload
}

// makeVideoPacket is a helper for building test H.264 packets.
func makeVideoPacket(videoCp *h264.CodecParameters, keyframe bool, ts, dur time.Duration, payload []byte) *h264.Packet {
	pkt := h264.NewPacket(keyframe, ts, time.Time{}, payload, "test", videoCp)
	pkt.SetDuration(dur)
	return pkt
}

// ---------------------------------------------------------------------------
// Init segment tests — parse output back and validate against ISO 14496-12
// ---------------------------------------------------------------------------

func TestGetInit_ParsedFtyp(t *testing.T) {
	t.Parallel()
	pair, _, _ := loadTestCodecPair(t)
	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	buf := m.GetInit()

	ftyp, _ := parseInitSegment(t, buf.Data())

	// ISO 14496-12 §4.3: major_brand identifies the best-use specification
	require.Equal(t, pio.U32BE([]byte("isom")), ftyp.MajorBrand)

	// "dash" must be a compatible brand for DASH streaming
	found := false
	for _, brand := range ftyp.CompatibleBrands {
		if brand == pio.U32BE([]byte("dash")) {
			found = true
			break
		}
	}
	require.True(t, found, "compatible_brands must include 'dash'")
}

func TestGetInit_ParsedMoov_VideoTrack(t *testing.T) {
	t.Parallel()
	pair, _, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	buf := m.GetInit()
	_, moov := parseInitSegment(t, buf.Data())

	// ISO 14496-12 §8.2.2: moov must have a header
	require.NotNil(t, moov.Header)
	require.Equal(t, int32(2), moov.Header.NextTrackID, "NextTrackID = num_tracks + 1")

	// Must have exactly one track
	require.Len(t, moov.Tracks, 1)
	track := moov.Tracks[0]

	// ISO 14496-12 §8.3.2: tkhd
	require.NotNil(t, track.Header)
	require.Equal(t, int32(1), track.Header.TrackId)
	require.Equal(t, uint32(0x0003), track.Header.Flags, "track_enabled | track_in_movie")

	// ISO 14496-12 §8.4.2: mdhd — timescale must be 90000 for video
	require.NotNil(t, track.Media)
	require.NotNil(t, track.Media.Header)
	require.Equal(t, int32(90000), track.Media.Header.TimeScale)

	// ISO 14496-12 §8.4.3: hdlr — handler_type must be "vide"
	require.NotNil(t, track.Media.Handler)
	require.Equal(t, [4]byte{'v', 'i', 'd', 'e'}, track.Media.Handler.SubType)
	// pre_defined field (Type) must be 0 per spec
	require.Equal(t, [4]byte{}, track.Media.Handler.Type)

	// ISO 14496-12 §8.4.5.2: vmhd must be present for video tracks
	require.NotNil(t, track.Media.Info)
	require.NotNil(t, track.Media.Info.Video, "video track must have vmhd")

	// ISO 14496-14 §5.6: mvex is required for fragmented files
	require.NotNil(t, moov.MovieExtend)
	require.Len(t, moov.MovieExtend.Tracks, 1)
	require.Equal(t, uint32(1), moov.MovieExtend.Tracks[0].TrackId)
}

func TestGetInit_ParsedMoov_AudioTrack(t *testing.T) {
	t.Parallel()
	pair, _, _ := loadTestCodecPair(t)
	pair.VideoCodecParameters = nil

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	buf := m.GetInit()
	_, moov := parseInitSegment(t, buf.Data())

	require.Len(t, moov.Tracks, 1)
	track := moov.Tracks[0]

	// ISO 14496-12 §8.4.2: mdhd timescale must match AAC sample rate
	require.Equal(t, int32(16000), track.Media.Header.TimeScale, "audio timescale = sample rate")

	// ISO 14496-12 §8.4.3: handler_type must be "soun"
	require.Equal(t, [4]byte{'s', 'o', 'u', 'n'}, track.Media.Handler.SubType)
	require.Equal(t, [4]byte{}, track.Media.Handler.Type)

	// ISO 14496-12 §8.4.5.3: smhd must be present for sound tracks
	require.NotNil(t, track.Media.Info.Sound, "audio track must have smhd")

	// ISO 14496-12 §8.3.2: volume=1.0 for audio, alternate_group=1
	require.Equal(t, float64(1), track.Header.Volume)
	require.Equal(t, int16(1), track.Header.AlternateGroup)
}

func TestGetInit_ParsedMoov_VideoAndAudio(t *testing.T) {
	t.Parallel()
	pair, _, _ := loadTestCodecPair(t)

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	buf := m.GetInit()
	_, moov := parseInitSegment(t, buf.Data())

	require.Len(t, moov.Tracks, 2)
	require.Equal(t, int32(3), moov.Header.NextTrackID)

	// Track IDs must be 1, 2
	require.Equal(t, int32(1), moov.Tracks[0].Header.TrackId)
	require.Equal(t, int32(2), moov.Tracks[1].Header.TrackId)

	// First track = video, second = audio
	require.Equal(t, [4]byte{'v', 'i', 'd', 'e'}, moov.Tracks[0].Media.Handler.SubType)
	require.Equal(t, [4]byte{'s', 'o', 'u', 'n'}, moov.Tracks[1].Media.Handler.SubType)

	require.NotNil(t, moov.Tracks[0].Media.Info.Video, "video track must have vmhd")
	require.NotNil(t, moov.Tracks[1].Media.Info.Sound, "audio track must have smhd")

	// mvex must have one trex per track
	require.Len(t, moov.MovieExtend.Tracks, 2)
}

// ---------------------------------------------------------------------------
// Fragment tests — parse moof back and validate structural correctness
// ---------------------------------------------------------------------------

func TestGetMP4Fragment_ParsedMoof_SequenceNumber(t *testing.T) {
	t.Parallel()
	pair, videoCp, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 0, 33*time.Millisecond, []byte{0x01})))

	buf := m.GetMP4Fragment(42)

	moof, _ := parseFragment(t, buf.Data())
	require.NotNil(t, moof.Header)
	require.Equal(t, uint32(42), moof.Header.Seqnum, "mfhd sequence_number must match idx")
}

func TestGetMP4Fragment_ParsedMoof_TrackID(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 0, 33*time.Millisecond, []byte{0x01})))
	require.NoError(t, m.strs[1].writePacket(aac.NewPacket([]byte{0x02}, 0, "test", time.Time{}, audioCp, 64*time.Millisecond)))

	buf := m.GetMP4Fragment(1)

	moof, _ := parseFragment(t, buf.Data())
	require.Len(t, moof.Tracks, 2)

	// ISO 14496-12 §8.8.7: track_ID in tfhd must match the track in moov
	require.Equal(t, uint32(1), moof.Tracks[0].Header.TrackID)
	require.Equal(t, uint32(2), moof.Tracks[1].Header.TrackID)
}

func TestGetMP4Fragment_ParsedMoof_TfhdFlags(t *testing.T) {
	t.Parallel()
	pair, videoCp, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 0, 33*time.Millisecond, []byte{0x01})))

	buf := m.GetMP4Fragment(1)

	moof, _ := parseFragment(t, buf.Data())
	flags := moof.Tracks[0].Header.Flags

	// ISO 14496-12 §8.8.7: default-base-is-moof must be set for fMP4
	require.NotZero(t, flags&mp4io.TFHDDefaultBaseIsMOOF, "default-base-is-moof must be set")
	require.NotZero(t, flags&mp4io.TFHDDefaultDuration, "default-sample-duration-present must be set")
	require.NotZero(t, flags&mp4io.TFHDDefaultSize, "default-sample-size-present must be set")
	require.NotZero(t, flags&mp4io.TFHDDefaultFlags, "default-sample-flags-present must be set")
}

func TestGetMP4Fragment_ParsedMoof_DecodeTime(t *testing.T) {
	t.Parallel()
	pair, videoCp, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	ts := 500 * time.Millisecond
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, ts, 33*time.Millisecond, []byte{0x01})))

	buf := m.GetMP4Fragment(1)

	moof, _ := parseFragment(t, buf.Data())
	require.NotNil(t, moof.Tracks[0].DecodeTime)

	// tfdt.baseMediaDecodeTime must be the first packet's timestamp in timescale units
	expectedTime := uint64(m.strs[0].timeToTS(ts))
	require.Equal(t, expectedTime, moof.Tracks[0].DecodeTime.Time)
}

func TestGetMP4Fragment_DataOffset_PointsIntoMdat(t *testing.T) {
	t.Parallel()
	pair, videoCp, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 0, 33*time.Millisecond, payload)))

	buf := m.GetMP4Fragment(1)
	data := buf.Data()

	// Find moof start offset
	n := 0
	n += int(readBoxSize(data, n)) // skip styp
	for readTag(data, n) == mp4io.SIDX {
		n += int(readBoxSize(data, n))
	}
	moofStart := n
	moofSize := int(readBoxSize(data, n))

	moof, _ := parseFragment(t, data)
	dataOffset := moof.Tracks[0].Run.DataOffset

	// ISO 14496-12 §8.8.8: data-offset is relative to the base data offset.
	// With default-base-is-moof, base is the start of the moof box.
	// So moofStart + dataOffset should land inside mdat, pointing at our payload.
	absOffset := moofStart + int(dataOffset)
	require.True(t, absOffset > moofStart+moofSize, "data offset must point past moof into mdat")
	require.True(t, absOffset+len(payload) <= len(data), "data offset + payload must be within buffer")
	require.Equal(t, payload, data[absOffset:absOffset+len(payload)])
}

func TestGetMP4Fragment_MdatSize_MatchesPayload(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	videoPayload := []byte{0xAA, 0xBB, 0xCC}
	audioPayload := []byte{0xDD, 0xEE}
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 0, 33*time.Millisecond, videoPayload)))
	require.NoError(t, m.strs[1].writePacket(aac.NewPacket(audioPayload, 0, "test", time.Time{}, audioCp, 64*time.Millisecond)))

	buf := m.GetMP4Fragment(1)

	_, mdatPayload := parseFragment(t, buf.Data())

	// mdat payload should be exactly video data + audio data (in stream order)
	expectedLen := len(videoPayload) + len(audioPayload)
	require.Equal(t, expectedLen, len(mdatPayload), "mdat size must equal sum of all packet data")
	require.Equal(t, videoPayload, mdatPayload[:len(videoPayload)])
	require.Equal(t, audioPayload, mdatPayload[len(videoPayload):])
}

func TestGetMP4Fragment_MultiStream_DataOffsets(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	videoPayload := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	audioPayload := []byte{0xA1, 0xA2, 0xA3}
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 0, 33*time.Millisecond, videoPayload)))
	require.NoError(t, m.strs[1].writePacket(aac.NewPacket(audioPayload, 0, "test", time.Time{}, audioCp, 64*time.Millisecond)))

	buf := m.GetMP4Fragment(1)
	data := buf.Data()

	// Find moof start
	n := 0
	n += int(readBoxSize(data, n)) // styp
	for readTag(data, n) == mp4io.SIDX {
		n += int(readBoxSize(data, n))
	}
	moofStart := n

	moof, _ := parseFragment(t, data)
	require.Len(t, moof.Tracks, 2)

	// Each track's DataOffset must point to its own data within mdat
	videoOffset := moofStart + int(moof.Tracks[0].Run.DataOffset)
	audioOffset := moofStart + int(moof.Tracks[1].Run.DataOffset)

	require.Equal(t, videoPayload, data[videoOffset:videoOffset+len(videoPayload)])
	require.Equal(t, audioPayload, data[audioOffset:audioOffset+len(audioPayload)])
	require.Equal(t, videoOffset+len(videoPayload), audioOffset, "audio must follow video immediately")
}

// ---------------------------------------------------------------------------
// Sample flags tests — validate against ISO 14496-12 §8.8.3 sample_flags
// ---------------------------------------------------------------------------

func TestSampleFlags_VideoKeyframeThenNonKeyframe(t *testing.T) {
	t.Parallel()
	pair, videoCp, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 0, 33*time.Millisecond, []byte{0x01})))
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, false, 33*time.Millisecond, 33*time.Millisecond, []byte{0x02})))

	buf := m.GetMP4Fragment(1)
	moof, _ := parseFragment(t, buf.Data())

	trun := moof.Tracks[0].Run
	// DefaultFlags = non-keyframe (based on second sample)
	require.Equal(t, mp4io.SampleNonKeyframe, moof.Tracks[0].Header.DefaultFlags)
	// FirstSampleFlags must be set to override the first (keyframe) sample
	require.NotZero(t, trun.Flags&mp4io.TRUNFirstSampleFlags)
	require.Equal(t, mp4io.SampleNoDependencies, trun.FirstSampleFlags,
		"first sample (keyframe) must be SampleNoDependencies")
}

func TestSampleFlags_AllKeyframes(t *testing.T) {
	t.Parallel()
	pair, videoCp, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 0, 33*time.Millisecond, []byte{0x01})))
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 33*time.Millisecond, 33*time.Millisecond, []byte{0x02})))

	buf := m.GetMP4Fragment(1)
	moof, _ := parseFragment(t, buf.Data())

	// All keyframes: DefaultFlags = NoDependencies, no FirstSampleFlags needed
	require.Equal(t, mp4io.SampleNoDependencies, moof.Tracks[0].Header.DefaultFlags)
	require.Zero(t, moof.Tracks[0].Run.Flags&mp4io.TRUNFirstSampleFlags)
}

func TestSampleFlags_AudioAllNoDependencies(t *testing.T) {
	t.Parallel()
	pair, _, audioCp := loadTestCodecPair(t)
	pair.VideoCodecParameters = nil

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	require.NoError(t, m.strs[0].writePacket(aac.NewPacket([]byte{0x01}, 0, "test", time.Time{}, audioCp, 64*time.Millisecond)))
	require.NoError(t, m.strs[0].writePacket(aac.NewPacket([]byte{0x02}, 64*time.Millisecond, "test", time.Time{}, audioCp, 64*time.Millisecond)))

	buf := m.GetMP4Fragment(1)
	moof, _ := parseFragment(t, buf.Data())

	// Audio samples are all independent (sync) — must be SampleNoDependencies
	require.Equal(t, mp4io.SampleNoDependencies, moof.Tracks[0].Header.DefaultFlags,
		"audio DefaultFlags must be SampleNoDependencies, not SampleNonKeyframe")
	require.Zero(t, moof.Tracks[0].Run.Flags&mp4io.TRUNFirstSampleFlags,
		"audio should not need FirstSampleFlags override")
}

func TestSampleFlags_MixedKeyframes_TRUNSampleFlagsSet(t *testing.T) {
	t.Parallel()
	pair, videoCp, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	// keyframe, non-keyframe, keyframe — the third differs from DefaultFlags
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 0, 33*time.Millisecond, []byte{0x01})))
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, false, 33*time.Millisecond, 33*time.Millisecond, []byte{0x02})))
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 66*time.Millisecond, 33*time.Millisecond, []byte{0x03})))

	buf := m.GetMP4Fragment(1)
	moof, _ := parseFragment(t, buf.Data())

	trun := moof.Tracks[0].Run
	// DefaultFlags based on packets[1] = non-keyframe
	require.Equal(t, mp4io.SampleNonKeyframe, moof.Tracks[0].Header.DefaultFlags)
	// Third sample (keyframe) differs from default, so TRUNSampleFlags must be set
	require.NotZero(t, trun.Flags&mp4io.TRUNSampleFlags,
		"per-sample flags must be set when non-first samples differ from default")
}

// ---------------------------------------------------------------------------
// TRUN entry correctness
// ---------------------------------------------------------------------------

func TestTrunEntries_SizeAndDuration(t *testing.T) {
	t.Parallel()
	pair, videoCp, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	dur := 33 * time.Millisecond
	data1 := []byte{0x01, 0x02, 0x03}
	data2 := []byte{0x04, 0x05}
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 0, dur, data1)))
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, false, dur, dur, data2)))

	buf := m.GetMP4Fragment(1)
	moof, _ := parseFragment(t, buf.Data())

	trun := moof.Tracks[0].Run
	require.Len(t, trun.Entries, 2)

	// Sizes differ, so TRUNSampleSize must be set and entries must have correct sizes
	require.NotZero(t, trun.Flags&mp4io.TRUNSampleSize)
	require.Equal(t, uint32(len(data1)), trun.Entries[0].Size)
	require.Equal(t, uint32(len(data2)), trun.Entries[1].Size)
}

// ---------------------------------------------------------------------------
// Fragment lifecycle: reset after GetMP4Fragment
// ---------------------------------------------------------------------------

func TestGetMP4Fragment_ResetsStreams(t *testing.T) {
	t.Parallel()
	pair, videoCp, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 0, 33*time.Millisecond, []byte{0x01})))

	m.GetMP4Fragment(1)

	// Streams should be recreated and empty
	require.Len(t, m.strs, 1)
	require.Empty(t, m.strs[0].packets)
}

func TestMultipleFragments_IndependentPayloads(t *testing.T) {
	t.Parallel()
	pair, videoCp, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	// Fragment 1
	payload1 := []byte{0xAA, 0xBB}
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 0, 33*time.Millisecond, payload1)))
	buf1 := m.GetMP4Fragment(1)

	// Fragment 2
	payload2 := []byte{0xCC, 0xDD, 0xEE}
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 33*time.Millisecond, 33*time.Millisecond, payload2)))
	buf2 := m.GetMP4Fragment(2)

	// Validate each fragment independently
	_, mdat1 := parseFragment(t, buf1.Data())
	_, mdat2 := parseFragment(t, buf2.Data())

	require.Equal(t, payload1, mdat1, "fragment 1 mdat must contain only payload1")
	require.Equal(t, payload2, mdat2, "fragment 2 mdat must contain only payload2")
}

// ---------------------------------------------------------------------------
// timeToTS — validated mathematically, not fitted to output
// ---------------------------------------------------------------------------

func TestTimeToTS_ExactConversions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		timeScale int64
		duration  time.Duration
		expected  int64
	}{
		{"1s@90kHz", 90000, time.Second, 90000},
		{"1s@44100Hz", 44100, time.Second, 44100},
		{"0.5s@90kHz", 90000, 500 * time.Millisecond, 45000},
		{"zero", 90000, 0, 0},
		{"10s@48kHz", 48000, 10 * time.Second, 480000},
		{"24h@90kHz", 90000, 24 * time.Hour, 24 * 3600 * 90000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &Stream{timeScale: tc.timeScale}
			require.Equal(t, tc.expected, s.timeToTS(tc.duration))
		})
	}
}

// ---------------------------------------------------------------------------
// safeInt32Conversion — boundary tests
// ---------------------------------------------------------------------------

func TestSafeInt32Conversion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    int64
		expected int32
	}{
		{"zero", 0, 0},
		{"positive", 42, 42},
		{"negative", -100, -100},
		{"max_int32", maxInt32Value, int32(maxInt32Value)},
		{"min_int32", minInt32Value, int32(minInt32Value)},
		{"overflow_positive", maxInt32Value + 1, int32(maxInt32Value)},
		{"overflow_negative", minInt32Value - 1, int32(minInt32Value)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.expected, safeInt32Conversion(logger.Default, nil, tc.input, "test"))
		})
	}
}

// ---------------------------------------------------------------------------
// Integration: real test data roundtrip
// ---------------------------------------------------------------------------

func TestFullRoundtrip_RealData(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))

	// Init segment
	initBuf := m.GetInit()
	_, moov := parseInitSegment(t, initBuf.Data())
	require.Len(t, moov.Tracks, 2)

	// Write real packets
	pkts := loadTestPackets(t, videoCp, audioCp, 30)
	require.NotEmpty(t, pkts)
	for _, pkt := range pkts {
		require.NoError(t, m.WritePacket(pkt))
	}

	// Fragment
	fragBuf := m.GetMP4Fragment(1)

	moof, mdatPayload := parseFragment(t, fragBuf.Data())

	// Must have 2 tracks (video + audio)
	require.Len(t, moof.Tracks, 2)

	// TRUN entry count must match packet count per stream
	var totalEntries int
	for _, track := range moof.Tracks {
		require.NotEmpty(t, track.Run.Entries, "each track must have at least one trun entry")
		totalEntries += len(track.Run.Entries)
	}
	require.Equal(t, len(pkts), totalEntries, "total trun entries must match total packets written")

	// mdat must contain actual data
	var expectedMdatSize int
	for _, pkt := range pkts {
		expectedMdatSize += pkt.Len()
	}
	require.Equal(t, expectedMdatSize, len(mdatPayload), "mdat payload must equal total packet data size")
}

func TestFullRoundtrip_BufferExactSize(t *testing.T) {
	t.Parallel()
	pair, videoCp, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	m := NewMuxer(logger.Default)
	require.NoError(t, m.Mux(pair))
	require.NoError(t, m.WritePacket(makeVideoPacket(videoCp, true, 0, 33*time.Millisecond, []byte{0x01, 0x02})))

	buf := m.GetMP4Fragment(1)

	data := buf.Data()
	// Walk all boxes and verify they exactly fill the buffer — no trailing garbage
	n := 0
	for n < len(data) {
		size := int(readBoxSize(data, n))
		require.True(t, size >= 8, "box size must be at least 8 (size+tag)")
		require.True(t, n+size <= len(data), "box extends past buffer end")
		n += size
	}
	require.Equal(t, len(data), n, "boxes must exactly fill the buffer with no leftover bytes")
}
