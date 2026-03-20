//nolint:mnd // Test file contains many magic numbers for expected values
package mp4

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/format/mp4/mp4io"
	"github.com/ugparu/gomedia/utils/bits/pio"
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

// createTempMP4 creates a temp file, muxes packets into it, and returns the path.
func createTempMP4(t *testing.T, pair gomedia.CodecParametersPair, packets []gomedia.Packet) string {
	t.Helper()
	f, err := os.CreateTemp("", "gomedia_test_*.mp4")
	require.NoError(t, err)
	path := f.Name()

	mux := NewMuxer(f)
	require.NoError(t, mux.Mux(pair))

	for _, pkt := range packets {
		require.NoError(t, mux.WritePacket(pkt))
	}
	require.NoError(t, mux.WriteTrailer())
	require.NoError(t, f.Close())

	t.Cleanup(func() { os.Remove(path) })
	return path
}

// ---------------------------------------------------------------------------
// Muxer — initialization and basic API
// ---------------------------------------------------------------------------

func TestMuxer_Mux_VideoAndAudio(t *testing.T) {
	t.Parallel()
	pair, _, _ := loadTestCodecPair(t)

	f, err := os.CreateTemp("", "gomedia_test_*.mp4")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	mux := NewMuxer(f)
	require.NoError(t, mux.Mux(pair))

	// Should have created two streams
	require.Len(t, mux.streams, 2)
	assert.Equal(t, gomedia.H264, mux.streams[0].Type())
	assert.Equal(t, gomedia.AAC, mux.streams[1].Type())
}

func TestMuxer_Mux_VideoOnly(t *testing.T) {
	t.Parallel()
	pair, _, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	f, err := os.CreateTemp("", "gomedia_test_*.mp4")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	mux := NewMuxer(f)
	require.NoError(t, mux.Mux(pair))
	require.Len(t, mux.streams, 1)
	assert.Equal(t, gomedia.H264, mux.streams[0].Type())
}

func TestMuxer_Mux_AudioOnly(t *testing.T) {
	t.Parallel()
	pair, _, _ := loadTestCodecPair(t)
	pair.VideoCodecParameters = nil

	f, err := os.CreateTemp("", "gomedia_test_*.mp4")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	mux := NewMuxer(f)
	require.NoError(t, mux.Mux(pair))
	require.Len(t, mux.streams, 1)
	assert.Equal(t, gomedia.AAC, mux.streams[0].Type())
}

func TestMuxer_GetPreLastPacket_NoPackets(t *testing.T) {
	t.Parallel()
	pair, _, _ := loadTestCodecPair(t)

	f, err := os.CreateTemp("", "gomedia_test_*.mp4")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	mux := NewMuxer(f)
	require.NoError(t, mux.Mux(pair))

	// No packets written yet
	assert.Nil(t, mux.GetPreLastPacket(0))
	assert.Nil(t, mux.GetPreLastPacket(1))
	// Out of range
	assert.Nil(t, mux.GetPreLastPacket(99))
}

func TestMuxer_WritePacket_InvalidStreamIndex(t *testing.T) {
	t.Parallel()
	pair, videoCp, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil // video only → stream index 0

	f, err := os.CreateTemp("", "gomedia_test_*.mp4")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	mux := NewMuxer(f)
	require.NoError(t, mux.Mux(pair))

	// Create a packet with stream index 5 (out of range)
	pkt := h264.NewPacket(true, 0, time.Now(), []byte{0x01}, "test", videoCp)
	pkt.SetDuration(33 * time.Millisecond)
	pkt.Idx = 5

	err = mux.WritePacket(pkt)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

// ---------------------------------------------------------------------------
// Muxer — file structure validation (ISO 14496-12)
// ---------------------------------------------------------------------------

func TestMuxer_FileStructure_FtypFreeeMdat(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	// First box: ftyp
	require.Equal(t, mp4io.FTYP, readTag(data, 0), "first box must be ftyp")
	ftypSize := int(readBoxSize(data, 0))

	// Second box: free
	require.Equal(t, mp4io.FREE, readTag(data, ftypSize), "second box must be free")
	freeSize := int(readBoxSize(data, ftypSize))

	// Third box: mdat (extended size: size=1, then tag, then 8-byte extended size)
	mdatOffset := ftypSize + freeSize
	mdatSizeField := pio.U32BE(data[mdatOffset:])
	require.Equal(t, uint32(1), mdatSizeField, "mdat must use extended size (size=1)")
	require.Equal(t, mp4io.MDAT, readTag(data, mdatOffset), "third box must be mdat")

	// Extended size is at mdatOffset+8
	extendedSize := pio.U64BE(data[mdatOffset+8:])
	require.Greater(t, extendedSize, uint64(16), "mdat extended size must include header + data")

	// After mdat: moov
	moovOffset := mdatOffset + int(extendedSize)
	require.Equal(t, mp4io.MOOV, readTag(data, moovOffset), "last box must be moov")
}

func TestMuxer_Moov_TrackCount(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)
	require.Len(t, moov.Tracks, 2, "should have 2 tracks (video + audio)")
}

func TestMuxer_Moov_VideoTrack_Handler(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)
	videoTrack := moov.Tracks[0]

	// Handler per ISO 14496-12 §8.4.3
	require.NotNil(t, videoTrack.Media.Handler)
	assert.Equal(t, [4]byte{'v', 'i', 'd', 'e'}, videoTrack.Media.Handler.SubType)
	assert.Equal(t, [4]byte{}, videoTrack.Media.Handler.Type, "pre_defined must be 0")
}

func TestMuxer_Moov_AudioTrack_Handler(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)
	audioTrack := moov.Tracks[1]

	// Handler per ISO 14496-12 §8.4.3
	require.NotNil(t, audioTrack.Media.Handler)
	assert.Equal(t, [4]byte{'s', 'o', 'u', 'n'}, audioTrack.Media.Handler.SubType)
	assert.Equal(t, [4]byte{}, audioTrack.Media.Handler.Type, "pre_defined must be 0")
}

func TestMuxer_Moov_MediaHeaderFlags(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)

	// Per ISO 14496-12 §8.4.2, mdhd flags must be 0
	for i, track := range moov.Tracks {
		assert.Equal(t, uint32(0), track.Media.Header.Flags, "track %d: mdhd flags must be 0", i)
	}
}

func TestMuxer_Moov_VideoTrack_Dimensions(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)
	videoTrack := moov.Tracks[0]

	// Track header should have correct dimensions
	assert.Equal(t, float64(videoCp.Width()), videoTrack.Header.TrackWidth)
	assert.Equal(t, float64(videoCp.Height()), videoTrack.Header.TrackHeight)
}

func TestMuxer_Moov_VideoTrack_HasVmhdNotSmhd(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)
	videoTrack := moov.Tracks[0]

	// Per ISO 14496-12 §8.4.5 — video track must have vmhd, not smhd
	assert.NotNil(t, videoTrack.Media.Info.Video, "video track must have vmhd")
	assert.Nil(t, videoTrack.Media.Info.Sound, "video track must not have smhd")
}

func TestMuxer_Moov_AudioTrack_HasSmhdNotVmhd(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)
	audioTrack := moov.Tracks[1]

	// Per ISO 14496-12 §8.4.5 — audio track must have smhd, not vmhd
	assert.NotNil(t, audioTrack.Media.Info.Sound, "audio track must have smhd")
	assert.Nil(t, audioTrack.Media.Info.Video, "audio track must not have vmhd")
}

func TestMuxer_Moov_VideoTrack_HasAVC1Desc(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)
	videoTrack := moov.Tracks[0]

	stbl := videoTrack.Media.Info.Sample
	require.NotNil(t, stbl.SampleDesc)
	require.NotNil(t, stbl.SampleDesc.AVC1Desc, "video track must have AVC1 sample description")
	assert.NotNil(t, stbl.SampleDesc.AVC1Desc.Conf, "AVC1 must have avcC config")
	assert.Greater(t, len(stbl.SampleDesc.AVC1Desc.Conf.Data), 0, "avcC data must not be empty")
}

func TestMuxer_Moov_AudioTrack_HasMP4ADesc(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)
	audioTrack := moov.Tracks[1]

	stbl := audioTrack.Media.Info.Sample
	require.NotNil(t, stbl.SampleDesc)
	require.NotNil(t, stbl.SampleDesc.MP4ADesc, "audio track must have MP4A sample description")
	require.NotNil(t, stbl.SampleDesc.MP4ADesc.Conf, "MP4A must have esds config")
	assert.Greater(t, len(stbl.SampleDesc.MP4ADesc.Conf.DecConfig), 0, "esds DecConfig must not be empty")
}

func TestMuxer_Moov_AudioTrack_SampleSizeInBits(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)
	audioTrack := moov.Tracks[1]

	mp4a := audioTrack.Media.Info.Sample.SampleDesc.MP4ADesc
	// Per ISO 14496-12 §12.2.3 — SampleSize is bits per uncompressed sample
	expectedBits := int16(audioCp.SampleFormat().BytesPerSample()) * 8
	assert.Equal(t, expectedBits, mp4a.SampleSize, "SampleSize must be in bits (BytesPerSample * 8)")
}

func TestMuxer_Moov_SyncSampleTable(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 100)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)
	videoTrack := moov.Tracks[0]

	stss := videoTrack.Media.Info.Sample.SyncSample
	require.NotNil(t, stss, "video track must have stss (sync sample table)")
	require.Greater(t, len(stss.Entries), 0, "must have at least one sync sample")

	// First entry should be 1 (first sample is usually a keyframe)
	assert.Equal(t, uint32(1), stss.Entries[0], "first sync sample should be sample 1")
}

func TestMuxer_Moov_Duration(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 100)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)

	// Movie duration must be > 0
	assert.Greater(t, moov.Header.Duration, int64(0), "movie duration must be positive")

	// Each track duration must be > 0
	for i, track := range moov.Tracks {
		assert.Greater(t, track.Header.Duration, int64(0), "track %d duration must be positive", i)
		assert.Greater(t, track.Media.Header.Duration, int64(0), "track %d media duration must be positive", i)
	}
}

func TestMuxer_Moov_TimeScale(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)

	// Movie timescale should be 1000 (per mp4 convention)
	assert.Equal(t, int32(1000), moov.Header.TimeScale)

	// Video track timescale should be 90000
	assert.Equal(t, int32(90000), moov.Tracks[0].Media.Header.TimeScale)
}

func TestMuxer_Moov_NextTrackID(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)
	assert.Equal(t, int32(3), moov.Header.NextTrackID, "nextTrackID should be num_tracks + 1")
}

// ---------------------------------------------------------------------------
// Muxer — WriteTrailer writes last buffered packet
// ---------------------------------------------------------------------------

func TestMuxer_WriteTrailer_FlushesLastPacket(t *testing.T) {
	t.Parallel()
	pair, videoCp, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	f, err := os.CreateTemp("", "gomedia_test_*.mp4")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	mux := NewMuxer(f)
	require.NoError(t, mux.Mux(pair))

	// Write 3 packets. The muxer uses a one-packet delay, so after WritePacket(3),
	// only 2 have been written to disk. WriteTrailer should flush the 3rd.
	for i := 0; i < 3; i++ {
		pkt := h264.NewPacket(i == 0, time.Duration(i)*33*time.Millisecond, time.Now(), []byte{0x01, byte(i)}, "test", videoCp)
		pkt.SetDuration(33 * time.Millisecond)
		require.NoError(t, mux.WritePacket(pkt))
	}

	require.NoError(t, mux.WriteTrailer())
	require.NoError(t, f.Close())

	// Verify all 3 samples are in the file
	moov := demuxAndGetMoov(t, f.Name())
	videoTrack := moov.Tracks[0]

	totalSamples := 0
	for _, entry := range videoTrack.Media.Info.Sample.TimeToSample.Entries {
		totalSamples += int(entry.Count)
	}
	assert.Equal(t, 3, totalSamples, "all 3 packets should be written")
}

// ---------------------------------------------------------------------------
// Demuxer — basic functionality
// ---------------------------------------------------------------------------

func TestDemuxer_NonexistentFile(t *testing.T) {
	t.Parallel()
	dmx := NewDemuxer("/nonexistent/file.mp4")
	_, err := dmx.Demux()
	require.Error(t, err)
}

func TestDemuxer_CloseWithoutDemux(t *testing.T) {
	t.Parallel()
	dmx := NewDemuxer("/nonexistent/file.mp4")
	// Should not panic
	dmx.Close()
}

func TestDemuxer_InvalidFile(t *testing.T) {
	t.Parallel()
	f, err := os.CreateTemp("", "gomedia_test_*.mp4")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	// Write garbage data
	_, err = f.Write([]byte("this is not an mp4 file"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	dmx := NewDemuxer(f.Name())
	_, err = dmx.Demux()
	require.Error(t, err)
	dmx.Close()
}

// ---------------------------------------------------------------------------
// Round-trip: Mux → Demux → verify codec parameters
// ---------------------------------------------------------------------------

func TestRoundTrip_CodecParameters(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	dmx := NewDemuxer(path)
	defer dmx.Close()

	params, err := dmx.Demux()
	require.NoError(t, err)

	// Video parameters
	require.NotNil(t, params.VideoCodecParameters)
	assert.Equal(t, gomedia.H264, params.VideoCodecParameters.Type())

	h264Params, ok := params.VideoCodecParameters.(*h264.CodecParameters)
	require.True(t, ok)
	assert.Equal(t, videoCp.Width(), h264Params.Width())
	assert.Equal(t, videoCp.Height(), h264Params.Height())

	// Audio parameters
	require.NotNil(t, params.AudioCodecParameters)
	assert.Equal(t, gomedia.AAC, params.AudioCodecParameters.Type())

	aacParams, ok := params.AudioCodecParameters.(*aac.CodecParameters)
	require.True(t, ok)
	assert.Equal(t, audioCp.SampleRate(), aacParams.SampleRate())
	assert.Equal(t, audioCp.ChannelLayout(), aacParams.ChannelLayout())
}

func TestRoundTrip_VideoParameters_Accessors(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	dmx := NewDemuxer(path).(*Demuxer)
	defer dmx.Close()

	_, err := dmx.Demux()
	require.NoError(t, err)

	assert.NotNil(t, dmx.VideoParameters())
	assert.NotNil(t, dmx.AudioParameters())
	assert.Equal(t, gomedia.H264, dmx.VideoParameters().Type())
	assert.Equal(t, gomedia.AAC, dmx.AudioParameters().Type())
}

// ---------------------------------------------------------------------------
// Round-trip: Mux → Demux → verify packets
// ---------------------------------------------------------------------------

func TestRoundTrip_ReadAllPackets_EOF(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	dmx := NewDemuxer(path)
	defer dmx.Close()

	_, err := dmx.Demux()
	require.NoError(t, err)

	// Read all packets until EOF
	var readPackets []gomedia.Packet
	for {
		pkt, readErr := dmx.ReadPacket()
		if readErr == io.EOF {
			break
		}
		require.NoError(t, readErr)
		readPackets = append(readPackets, pkt)
	}

	require.Greater(t, len(readPackets), 0, "should read at least some packets")
}

func TestRoundTrip_PacketTimestampsAreNonNegative(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	dmx := NewDemuxer(path)
	defer dmx.Close()

	_, err := dmx.Demux()
	require.NoError(t, err)

	for {
		pkt, readErr := dmx.ReadPacket()
		if readErr == io.EOF {
			break
		}
		require.NoError(t, readErr)
		assert.GreaterOrEqual(t, pkt.Timestamp(), time.Duration(0), "packet timestamp must be non-negative")
	}
}

func TestRoundTrip_PacketTimestampsAreMonotonicallyIncreasingPerStream(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 100)
	path := createTempMP4(t, pair, packets)

	dmx := NewDemuxer(path)
	defer dmx.Close()

	_, err := dmx.Demux()
	require.NoError(t, err)

	lastTS := map[uint8]time.Duration{}
	for {
		pkt, readErr := dmx.ReadPacket()
		if readErr == io.EOF {
			break
		}
		require.NoError(t, readErr)

		idx := pkt.StreamIndex()
		if prev, ok := lastTS[idx]; ok {
			assert.GreaterOrEqual(t, pkt.Timestamp(), prev,
				"stream %d: timestamps must be monotonically non-decreasing", idx)
		}
		lastTS[idx] = pkt.Timestamp()
	}
}

func TestRoundTrip_PacketDataNonEmpty(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	dmx := NewDemuxer(path)
	defer dmx.Close()

	_, err := dmx.Demux()
	require.NoError(t, err)

	for {
		pkt, readErr := dmx.ReadPacket()
		if readErr == io.EOF {
			break
		}
		require.NoError(t, readErr)
		assert.Greater(t, pkt.Len(), 0, "packet data must not be empty")
	}
}

func TestRoundTrip_VideoPacketsHaveKeyframes(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 100)
	path := createTempMP4(t, pair, packets)

	dmx := NewDemuxer(path)
	defer dmx.Close()

	_, err := dmx.Demux()
	require.NoError(t, err)

	hasKeyframe := false
	for {
		pkt, readErr := dmx.ReadPacket()
		if readErr == io.EOF {
			break
		}
		require.NoError(t, readErr)
		if vPkt, ok := pkt.(gomedia.VideoPacket); ok {
			if vPkt.IsKeyFrame() {
				hasKeyframe = true
			}
		}
	}
	assert.True(t, hasKeyframe, "should have at least one keyframe")
}

func TestRoundTrip_VideoOnly(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	allPackets := loadTestPackets(t, videoCp, audioCp, 100)
	// Filter to video-only
	var videoPackets []gomedia.Packet
	for _, pkt := range allPackets {
		if pkt.StreamIndex() == 0 {
			videoPackets = append(videoPackets, pkt)
		}
	}
	require.Greater(t, len(videoPackets), 0)

	path := createTempMP4(t, pair, videoPackets)

	dmx := NewDemuxer(path)
	defer dmx.Close()

	params, err := dmx.Demux()
	require.NoError(t, err)
	assert.NotNil(t, params.VideoCodecParameters)
	assert.Nil(t, params.AudioCodecParameters)

	count := 0
	for {
		_, readErr := dmx.ReadPacket()
		if readErr == io.EOF {
			break
		}
		require.NoError(t, readErr)
		count++
	}
	assert.Greater(t, count, 0)
}

func TestRoundTrip_AudioOnly(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	pair.VideoCodecParameters = nil
	audioCp.SetStreamIndex(0) // Must be 0 when it's the only stream
	pair.AudioCodecParameters = audioCp

	allPackets := loadTestPackets(t, videoCp, audioCp, 100)
	// Filter to audio-only and fix stream index
	var audioPackets []gomedia.Packet
	for _, pkt := range allPackets {
		if _, ok := pkt.(*aac.Packet); ok {
			// Create new packet with stream index 0
			ap := pkt.(*aac.Packet)
			newPkt := aac.NewPacket(ap.Data(), ap.Timestamp(), "test", time.Now(), audioCp, ap.Duration())
			audioPackets = append(audioPackets, newPkt)
		}
	}
	require.Greater(t, len(audioPackets), 0)

	path := createTempMP4(t, pair, audioPackets)

	dmx := NewDemuxer(path)
	defer dmx.Close()

	params, err := dmx.Demux()
	require.NoError(t, err)
	assert.Nil(t, params.VideoCodecParameters)
	assert.NotNil(t, params.AudioCodecParameters)

	count := 0
	for {
		_, readErr := dmx.ReadPacket()
		if readErr == io.EOF {
			break
		}
		require.NoError(t, readErr)
		count++
	}
	assert.Greater(t, count, 0)
}

// ---------------------------------------------------------------------------
// Muxer — GetPreLastPacket after writing
// ---------------------------------------------------------------------------

func TestMuxer_GetPreLastPacket_AfterWrite(t *testing.T) {
	t.Parallel()
	pair, videoCp, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	f, err := os.CreateTemp("", "gomedia_test_*.mp4")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	mux := NewMuxer(f)
	require.NoError(t, mux.Mux(pair))

	pkt1 := h264.NewPacket(true, 0, time.Now(), []byte{0x01}, "test", videoCp)
	pkt1.SetDuration(33 * time.Millisecond)
	require.NoError(t, mux.WritePacket(pkt1))

	// After first write, lastPacket should be pkt1
	result := mux.GetPreLastPacket(0)
	require.NotNil(t, result)

	pkt2 := h264.NewPacket(false, 33*time.Millisecond, time.Now(), []byte{0x02}, "test", videoCp)
	pkt2.SetDuration(33 * time.Millisecond)
	require.NoError(t, mux.WritePacket(pkt2))

	// After second write, lastPacket should be pkt2
	result = mux.GetPreLastPacket(0)
	require.NotNil(t, result)
}

// ---------------------------------------------------------------------------
// Muxer — GetWritePosition
// ---------------------------------------------------------------------------

func TestMuxer_GetWritePosition(t *testing.T) {
	t.Parallel()
	pair, videoCp, _ := loadTestCodecPair(t)
	pair.AudioCodecParameters = nil

	f, err := os.CreateTemp("", "gomedia_test_*.mp4")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	mux := NewMuxer(f)
	require.NoError(t, mux.Mux(pair))

	// After Mux: ftyp(32) + free(8) + mdat_header(16) = 56
	assert.Equal(t, int64(56), mux.GetWritePosition())

	pkt := h264.NewPacket(true, 0, time.Now(), []byte{0x01, 0x02, 0x03}, "test", videoCp)
	pkt.SetDuration(33 * time.Millisecond)
	require.NoError(t, mux.WritePacket(pkt))

	// After first write, no data written yet (one-packet delay)
	assert.Equal(t, int64(56), mux.GetWritePosition())

	pkt2 := h264.NewPacket(false, 33*time.Millisecond, time.Now(), []byte{0x04, 0x05}, "test", videoCp)
	pkt2.SetDuration(33 * time.Millisecond)
	require.NoError(t, mux.WritePacket(pkt2))

	// After second write, first packet should be flushed
	assert.Greater(t, mux.GetWritePosition(), int64(56))
}

// ---------------------------------------------------------------------------
// Muxer — stts (TimeToSample) and stsz (SampleSize) validation
// ---------------------------------------------------------------------------

func TestMuxer_SampleTable_SttsAndStsz(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)

	for i, track := range moov.Tracks {
		stbl := track.Media.Info.Sample
		require.NotNil(t, stbl.TimeToSample, "track %d: must have stts", i)
		require.Greater(t, len(stbl.TimeToSample.Entries), 0, "track %d: stts must have entries", i)

		require.NotNil(t, stbl.SampleSize, "track %d: must have stsz", i)
		require.Greater(t, len(stbl.SampleSize.Entries), 0, "track %d: stsz must have entries", i)

		// Verify total sample count from stts matches stsz
		totalFromStts := 0
		for _, entry := range stbl.TimeToSample.Entries {
			totalFromStts += int(entry.Count)
		}
		assert.Equal(t, len(stbl.SampleSize.Entries), totalFromStts,
			"track %d: stts sample count must match stsz entry count", i)
	}
}

// ---------------------------------------------------------------------------
// Demuxer — SourceID
// ---------------------------------------------------------------------------

func TestDemuxer_SourceID(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	dmx := NewDemuxer(path)
	defer dmx.Close()

	params, err := dmx.Demux()
	require.NoError(t, err)
	assert.Equal(t, path, params.SourceID, "SourceID should be the file path")
}

// ---------------------------------------------------------------------------
// ChunkOffset — stco vs co64
// ---------------------------------------------------------------------------

func TestMuxer_ChunkOffset_Stco(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)

	for i, track := range moov.Tracks {
		stbl := track.Media.Info.Sample
		require.NotNil(t, stbl.ChunkOffset, "track %d: must have chunk offset table", i)
		require.Greater(t, len(stbl.ChunkOffset.Entries), 0, "track %d: chunk offset must have entries", i)

		// For small files, all offsets should fit in 32 bits
		for _, offset := range stbl.ChunkOffset.Entries {
			assert.Less(t, offset, uint64(1<<32), "small file offsets should fit in 32 bits")
		}
	}
}

// ---------------------------------------------------------------------------
// Composition offset (ctts) — should be 0 for this muxer
// ---------------------------------------------------------------------------

func TestMuxer_CompositionOffset_Zero(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 50)
	path := createTempMP4(t, pair, packets)

	moov := demuxAndGetMoov(t, path)
	videoTrack := moov.Tracks[0]

	ctts := videoTrack.Media.Info.Sample.CompositionOffset
	if ctts != nil && len(ctts.Entries) > 0 {
		for _, entry := range ctts.Entries {
			assert.Equal(t, uint32(0), entry.Offset,
				"composition offset must be 0 (no B-frame reordering)")
		}
	}
}

// ---------------------------------------------------------------------------
// Multiple ReadPacket after EOF
// ---------------------------------------------------------------------------

func TestDemuxer_ReadPacket_MultipleEOF(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 10)
	path := createTempMP4(t, pair, packets)

	dmx := NewDemuxer(path)
	defer dmx.Close()

	_, err := dmx.Demux()
	require.NoError(t, err)

	// Drain all packets
	for {
		_, readErr := dmx.ReadPacket()
		if readErr == io.EOF {
			break
		}
		require.NoError(t, readErr)
	}

	// Subsequent reads should also return EOF
	_, err = dmx.ReadPacket()
	assert.Equal(t, io.EOF, err)
	_, err = dmx.ReadPacket()
	assert.Equal(t, io.EOF, err)
}

// ---------------------------------------------------------------------------
// Ftyp brands validation
// ---------------------------------------------------------------------------

func TestMuxer_Ftyp_Brands(t *testing.T) {
	t.Parallel()
	pair, videoCp, audioCp := loadTestCodecPair(t)
	packets := loadTestPackets(t, videoCp, audioCp, 10)
	path := createTempMP4(t, pair, packets)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	ftypSize := int(readBoxSize(data, 0))
	ftyp := &mp4io.FileType{}
	_, err = ftyp.Unmarshal(data[:ftypSize], 0)
	require.NoError(t, err)

	// Verify "isom" major brand
	assert.Equal(t, pio.U32BE([]byte("isom")), ftyp.MajorBrand)
	assert.Greater(t, len(ftyp.CompatibleBrands), 0, "must have compatible brands")
}

// ---------------------------------------------------------------------------
// Helper: parse moov from a muxed file
// ---------------------------------------------------------------------------

func demuxAndGetMoov(t *testing.T, path string) *mp4io.Movie {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	atoms, err := mp4io.ReadFileAtoms(f)
	require.NoError(t, err)

	for _, atom := range atoms {
		if atom.Tag() == mp4io.MOOV {
			moov, ok := atom.(*mp4io.Movie)
			require.True(t, ok)
			return moov
		}
	}
	t.Fatal("moov atom not found")
	return nil
}
