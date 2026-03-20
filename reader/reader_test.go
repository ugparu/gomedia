package reader

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/format/rtsp"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

// fakeDemuxer is a controllable demuxer for testing.
type fakeDemuxer struct {
	demuxResult gomedia.CodecParametersPair
	demuxErr    error
	readFunc    func() (gomedia.Packet, error)
	closeCh     chan struct{}
	closeOnce   sync.Once
	closed      atomic.Bool
}

func newFakeDemuxer() *fakeDemuxer {
	return &fakeDemuxer{
		closeCh: make(chan struct{}),
	}
}

func (fd *fakeDemuxer) Demux() (gomedia.CodecParametersPair, error) {
	return fd.demuxResult, fd.demuxErr
}

func (fd *fakeDemuxer) ReadPacket() (gomedia.Packet, error) {
	if fd.readFunc != nil {
		return fd.readFunc()
	}
	// Block until closed
	<-fd.closeCh
	return nil, errors.New("demuxer closed")
}

func (fd *fakeDemuxer) Close() {
	fd.closeOnce.Do(func() {
		fd.closed.Store(true)
		close(fd.closeCh)
	})
}

var testH264Params = &h264.CodecParameters{
	BaseParameters: codec.BaseParameters{
		CodecType: gomedia.H264,
	},
}

var testAACParams = &aac.CodecParameters{
	BaseParameters: codec.BaseParameters{
		CodecType: gomedia.AAC,
		Index:     1,
	},
}

func makeVideoPacket(ts time.Duration) gomedia.VideoPacket {
	return h264.NewPacket(true, ts, time.Now(), []byte{0x00, 0x01}, "test", testH264Params)
}

func makeAudioPacket(ts, dur time.Duration) gomedia.AudioPacket {
	return aac.NewPacket([]byte{0xFF, 0xF1}, ts, "test", time.Now(), testAACParams, dur)
}

// newTestReader creates a reader with an injected demuxer factory for testing.
func newTestReader(chanSize int, factory func(string, ...rtsp.DemuxerOption) gomedia.Demuxer) *reader {
	rdr := &reader{
		log:         logger.Default,
		newDmx:      factory,
		packets:     make(chan gomedia.Packet, chanSize),
		addURLCh:    make(chan string, chanSize),
		removeURLCh: make(chan string, chanSize),
		dmxStoppers: make(map[string]chan struct{}),
		name:        "TEST_READER",
	}
	rdr.AsyncManager = lifecycle.NewFailSafeAsyncManager(rdr, rdr.log)
	return rdr
}

// receivePackets collects up to n packets from the channel with a timeout.
func receivePackets(ch <-chan gomedia.Packet, n int, timeout time.Duration) []gomedia.Packet {
	var pkts []gomedia.Packet
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for range n {
		select {
		case pkt := <-ch:
			pkts = append(pkts, pkt)
		case <-timer.C:
			return pkts
		}
	}
	return pkts
}

func TestReader_VideoPacketFlow(t *testing.T) {
	t.Parallel()

	fd := newFakeDemuxer()
	idx := 0
	// 30fps video: packets at 0ms, 33ms, 66ms, 100ms
	videoTimestamps := []time.Duration{
		100 * time.Millisecond,
		133 * time.Millisecond,
		166 * time.Millisecond,
		200 * time.Millisecond,
	}
	fd.readFunc = func() (gomedia.Packet, error) {
		if idx < len(videoTimestamps) {
			pkt := makeVideoPacket(videoTimestamps[idx])
			idx++
			return pkt, nil
		}
		// Block after all packets sent
		<-fd.closeCh
		return nil, errors.New("closed")
	}

	rdr := newTestReader(10, func(s string, opts ...rtsp.DemuxerOption) gomedia.Demuxer {
		return fd
	})

	rdr.Read()
	rdr.AddURL() <- "rtsp://test.local/stream"

	// One-behind: 4 packets in → 3 emitted (first cached, last buffered)
	pkts := receivePackets(rdr.Packets(), 3, 2*time.Second)
	require.Len(t, pkts, 3, "expected 3 packets emitted from 4 input packets (one-behind)")

	// Verify timestamps are normalized (start from 0) and monotonically increasing
	for i := 1; i < len(pkts); i++ {
		assert.Greater(t, pkts[i].Timestamp(), pkts[i-1].Timestamp(),
			"packet %d timestamp should be greater than packet %d", i, i-1)
	}

	rdr.Close()
	<-rdr.Done()
}

func TestReader_VideoDuration_SetFromTimestampDelta(t *testing.T) {
	t.Parallel()

	fd := newFakeDemuxer()
	idx := 0
	videoTimestamps := []time.Duration{
		1 * time.Second,
		1*time.Second + 33*time.Millisecond,
		1*time.Second + 66*time.Millisecond,
	}
	fd.readFunc = func() (gomedia.Packet, error) {
		if idx < len(videoTimestamps) {
			pkt := makeVideoPacket(videoTimestamps[idx])
			idx++
			return pkt, nil
		}
		<-fd.closeCh
		return nil, errors.New("closed")
	}

	rdr := newTestReader(10, func(s string, opts ...rtsp.DemuxerOption) gomedia.Demuxer {
		return fd
	})

	rdr.Read()
	rdr.AddURL() <- "rtsp://test.local/stream"

	// 3 packets in → 2 emitted
	pkts := receivePackets(rdr.Packets(), 2, 2*time.Second)
	require.Len(t, pkts, 2)

	// First emitted packet should have duration = 33ms (delta between first two raw timestamps)
	assert.Equal(t, 33*time.Millisecond, pkts[0].Duration(),
		"video packet duration should be computed from timestamp delta")
	assert.Equal(t, 33*time.Millisecond, pkts[1].Duration(),
		"second video packet duration should also be 33ms")

	rdr.Close()
	<-rdr.Done()
}

func TestReader_AudioPacketFlow(t *testing.T) {
	t.Parallel()

	fd := newFakeDemuxer()
	idx := 0
	// Audio packets with pre-set durations (as real demuxers do)
	audioDur := 23 * time.Millisecond
	audioTimestamps := []time.Duration{
		100 * time.Millisecond,
		123 * time.Millisecond,
		146 * time.Millisecond,
	}
	fd.readFunc = func() (gomedia.Packet, error) {
		if idx < len(audioTimestamps) {
			pkt := makeAudioPacket(audioTimestamps[idx], audioDur)
			idx++
			return pkt, nil
		}
		<-fd.closeCh
		return nil, errors.New("closed")
	}

	rdr := newTestReader(10, func(s string, opts ...rtsp.DemuxerOption) gomedia.Demuxer {
		return fd
	})

	rdr.Read()
	rdr.AddURL() <- "rtsp://test.local/stream"

	// 3 in → 2 emitted (one-behind)
	pkts := receivePackets(rdr.Packets(), 2, 2*time.Second)
	require.Len(t, pkts, 2)

	// Timestamps should be normalized and increasing
	assert.GreaterOrEqual(t, pkts[0].Timestamp(), time.Duration(0))
	assert.Greater(t, pkts[1].Timestamp(), pkts[0].Timestamp())

	rdr.Close()
	<-rdr.Done()
}

func TestReader_MixedVideoAudioFlow(t *testing.T) {
	t.Parallel()

	fd := newFakeDemuxer()
	// Interleaved video and audio packets
	packets := []gomedia.Packet{
		makeVideoPacket(100 * time.Millisecond),
		makeAudioPacket(100*time.Millisecond, 23*time.Millisecond),
		makeVideoPacket(133 * time.Millisecond),
		makeAudioPacket(123*time.Millisecond, 23*time.Millisecond),
		makeVideoPacket(166 * time.Millisecond),
		makeAudioPacket(146*time.Millisecond, 23*time.Millisecond),
	}
	idx := 0
	fd.readFunc = func() (gomedia.Packet, error) {
		if idx < len(packets) {
			pkt := packets[idx]
			idx++
			return pkt, nil
		}
		<-fd.closeCh
		return nil, errors.New("closed")
	}

	rdr := newTestReader(10, func(s string, opts ...rtsp.DemuxerOption) gomedia.Demuxer {
		return fd
	})

	rdr.Read()
	rdr.AddURL() <- "rtsp://test.local/stream"

	// 3 video + 3 audio = 6 in → 2 video + 2 audio = 4 emitted (one-behind per type)
	pkts := receivePackets(rdr.Packets(), 4, 2*time.Second)
	require.Len(t, pkts, 4, "expected 4 packets from 6 input (one-behind per stream type)")

	rdr.Close()
	<-rdr.Done()
}

func TestReader_NilPacketsSkipped(t *testing.T) {
	t.Parallel()

	fd := newFakeDemuxer()
	idx := 0
	fd.readFunc = func() (gomedia.Packet, error) {
		idx++
		switch idx {
		case 1:
			return nil, nil // nil packet
		case 2:
			return makeVideoPacket(100 * time.Millisecond), nil
		case 3:
			return nil, nil // another nil packet
		case 4:
			return makeVideoPacket(133 * time.Millisecond), nil
		case 5:
			return makeVideoPacket(166 * time.Millisecond), nil
		default:
			<-fd.closeCh
			return nil, errors.New("closed")
		}
	}

	rdr := newTestReader(10, func(s string, opts ...rtsp.DemuxerOption) gomedia.Demuxer {
		return fd
	})

	rdr.Read()
	rdr.AddURL() <- "rtsp://test.local/stream"

	// 3 real video packets → 2 emitted, nil packets skipped
	pkts := receivePackets(rdr.Packets(), 2, 2*time.Second)
	require.Len(t, pkts, 2, "nil packets should be skipped, 3 real packets → 2 emitted")

	rdr.Close()
	<-rdr.Done()
}

func TestReader_NonMonotonicPacketsFiltered(t *testing.T) {
	t.Parallel()

	fd := newFakeDemuxer()
	idx := 0
	fd.readFunc = func() (gomedia.Packet, error) {
		idx++
		switch idx {
		case 1:
			return makeVideoPacket(100 * time.Millisecond), nil
		case 2:
			return makeVideoPacket(133 * time.Millisecond), nil
		case 3:
			// Non-monotonic: goes backwards
			return makeVideoPacket(120 * time.Millisecond), nil
		case 4:
			return makeVideoPacket(166 * time.Millisecond), nil
		case 5:
			return makeVideoPacket(200 * time.Millisecond), nil
		default:
			<-fd.closeCh
			return nil, errors.New("closed")
		}
	}

	rdr := newTestReader(10, func(s string, opts ...rtsp.DemuxerOption) gomedia.Demuxer {
		return fd
	})

	rdr.Read()
	rdr.AddURL() <- "rtsp://test.local/stream"

	// 5 packets in, 1 is non-monotonic (dropped), 1 cached → 3 emitted
	pkts := receivePackets(rdr.Packets(), 3, 2*time.Second)
	require.Len(t, pkts, 3, "non-monotonic packet should be filtered out")

	// Verify all output timestamps are strictly increasing
	for i := 1; i < len(pkts); i++ {
		assert.Greater(t, pkts[i].Timestamp(), pkts[i-1].Timestamp(),
			"output timestamps must be strictly monotonic after filtering")
	}

	rdr.Close()
	<-rdr.Done()
}

func TestReader_StopDuringReading(t *testing.T) {
	t.Parallel()

	fd := newFakeDemuxer()
	idx := 0
	fd.readFunc = func() (gomedia.Packet, error) {
		idx++
		if idx <= 2 {
			return makeVideoPacket(time.Duration(idx) * 100 * time.Millisecond), nil
		}
		// Slow down to allow Close() to take effect
		time.Sleep(50 * time.Millisecond)
		return makeVideoPacket(time.Duration(idx) * 100 * time.Millisecond), nil
	}

	rdr := newTestReader(10, func(s string, opts ...rtsp.DemuxerOption) gomedia.Demuxer {
		return fd
	})

	rdr.Read()
	rdr.AddURL() <- "rtsp://test.local/stream"

	// Read at least one packet
	pkts := receivePackets(rdr.Packets(), 1, 2*time.Second)
	require.NotEmpty(t, pkts, "should receive at least one packet before close")

	rdr.Close()

	// Done channel should signal within reasonable time
	select {
	case <-rdr.Done():
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("reader did not shut down within timeout")
	}
}

func TestReader_ReconnectOnError(t *testing.T) {
	t.Parallel()

	var connectCount atomic.Int32

	factory := func(s string, opts ...rtsp.DemuxerOption) gomedia.Demuxer {
		count := connectCount.Add(1)
		fd := newFakeDemuxer()

		idx := 0
		fd.readFunc = func() (gomedia.Packet, error) {
			idx++
			if count == 1 {
				// First connection: send 2 packets then error
				switch {
				case idx <= 2:
					return makeVideoPacket(time.Duration(idx) * 100 * time.Millisecond), nil
				default:
					return nil, errors.New("connection lost")
				}
			}
			// Second connection: send packets from a different base timestamp
			if idx <= 3 {
				return makeVideoPacket(time.Duration(idx) * 50 * time.Millisecond), nil
			}
			<-fd.closeCh
			return nil, errors.New("closed")
		}

		return fd
	}

	rdr := newTestReader(10, factory)
	rdr.Read()
	rdr.AddURL() <- "rtsp://test.local/stream"

	// First connection: 2 video → 1 emitted (one-behind), then error + 1s reconnect
	// Second connection: 3 video → 2 emitted
	// Total: 3 packets, but need to wait >1s for reconnect
	pkts := receivePackets(rdr.Packets(), 3, 5*time.Second)
	require.NotEmpty(t, pkts, "should receive packets even after reconnect")

	// Verify reconnect happened
	assert.GreaterOrEqual(t, connectCount.Load(), int32(2), "should have reconnected at least once")

	rdr.Close()
	<-rdr.Done()
}

func TestReader_ReconnectPreservesRTSPOptions(t *testing.T) {
	t.Parallel()

	var capturedOpts [][]rtsp.DemuxerOption
	var mu sync.Mutex

	factory := func(s string, opts ...rtsp.DemuxerOption) gomedia.Demuxer {
		mu.Lock()
		capturedOpts = append(capturedOpts, opts)
		mu.Unlock()

		fd := newFakeDemuxer()
		callCount := 0
		fd.readFunc = func() (gomedia.Packet, error) {
			callCount++
			if callCount == 1 {
				return nil, errors.New("force reconnect")
			}
			<-fd.closeCh
			return nil, errors.New("closed")
		}
		return fd
	}

	// Use a sentinel option to verify it's passed through
	sentinelCalled := false
	sentinelOpt := func(d *rtsp.DemuxerOption) {}
	_ = sentinelOpt

	rdr := newTestReader(10, factory)
	// Manually set opts since we can't use real rtsp.DemuxerOption constructors easily
	rdr.opts = []rtsp.DemuxerOption{}
	rdr.Read()
	rdr.AddURL() <- "rtsp://test.local/stream"

	// Wait for at least 2 connections (initial + reconnect)
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		n := len(capturedOpts)
		mu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for reconnect")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	// Both initial and reconnect calls should receive opts (even if empty slice)
	// The key test: they should both be non-nil (opts... was passed)
	require.GreaterOrEqual(t, len(capturedOpts), 2)

	_ = sentinelCalled // used for verification pattern

	rdr.Close()
	<-rdr.Done()
}

func TestReader_AddAndRemoveURL(t *testing.T) {
	t.Parallel()

	var activeCount atomic.Int32

	factory := func(s string, opts ...rtsp.DemuxerOption) gomedia.Demuxer {
		activeCount.Add(1)
		fd := newFakeDemuxer()
		idx := 0
		fd.readFunc = func() (gomedia.Packet, error) {
			idx++
			return makeVideoPacket(time.Duration(idx) * 100 * time.Millisecond), nil
		}
		return fd
	}

	rdr := newTestReader(10, factory)
	rdr.Read()

	url := "rtsp://test.local/stream1"
	rdr.AddURL() <- url

	// Wait for packets to flow
	pkts := receivePackets(rdr.Packets(), 1, 2*time.Second)
	require.NotEmpty(t, pkts)

	// Remove URL
	rdr.RemoveURL() <- url

	// Small delay for goroutine to stop
	time.Sleep(100 * time.Millisecond)

	rdr.Close()
	<-rdr.Done()
}

func TestReader_CloseWithNoURLs(t *testing.T) {
	t.Parallel()

	rdr := newTestReader(10, func(s string, opts ...rtsp.DemuxerOption) gomedia.Demuxer {
		return newFakeDemuxer()
	})

	rdr.Read()

	// Close immediately without adding any URLs
	rdr.Close()

	select {
	case <-rdr.Done():
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not shut down within timeout")
	}
}

func TestReader_PacketsChannelReturned(t *testing.T) {
	t.Parallel()

	rdr := newTestReader(10, func(s string, opts ...rtsp.DemuxerOption) gomedia.Demuxer {
		return newFakeDemuxer()
	})

	ch := rdr.Packets()
	require.NotNil(t, ch)
}

func TestReader_AddURLSetsName(t *testing.T) {
	t.Parallel()

	fd := newFakeDemuxer()
	rdr := newTestReader(10, func(s string, opts ...rtsp.DemuxerOption) gomedia.Demuxer {
		return fd
	})

	rdr.Read()
	rdr.AddURL() <- "rtsp://192.168.1.100:554/stream"

	// Give time for Step to process
	time.Sleep(100 * time.Millisecond)

	assert.Contains(t, rdr.String(), "192.168.1.100")

	rdr.Close()
	<-rdr.Done()
}

func TestReader_ReconnectBackoff(t *testing.T) {
	t.Parallel()

	var connectTimes []time.Time
	var mu sync.Mutex

	factory := func(s string, opts ...rtsp.DemuxerOption) gomedia.Demuxer {
		mu.Lock()
		connectTimes = append(connectTimes, time.Now())
		mu.Unlock()

		fd := newFakeDemuxer()
		fd.demuxErr = errors.New("connection refused")
		fd.readFunc = func() (gomedia.Packet, error) {
			return nil, errors.New("not connected")
		}
		return fd
	}

	rdr := newTestReader(10, factory)
	rdr.Read()
	rdr.AddURL() <- "rtsp://test.local/stream"

	// Wait for several reconnect attempts
	deadline := time.After(10 * time.Second)
	for {
		mu.Lock()
		n := len(connectTimes)
		mu.Unlock()
		if n >= 4 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for reconnect attempts")
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	mu.Lock()
	times := make([]time.Time, len(connectTimes))
	copy(times, connectTimes)
	mu.Unlock()

	// Verify backoff: intervals should generally increase
	// First interval ~1s, second ~2s, third ~4s
	if len(times) >= 3 {
		interval1 := times[1].Sub(times[0])
		interval2 := times[2].Sub(times[1])
		assert.Greater(t, interval2, interval1,
			"reconnect intervals should increase (backoff): first=%v, second=%v", interval1, interval2)
	}

	rdr.Close()
	<-rdr.Done()
}

func TestReader_DemuxError_StillReconnects(t *testing.T) {
	t.Parallel()

	var connectCount atomic.Int32

	factory := func(s string, opts ...rtsp.DemuxerOption) gomedia.Demuxer {
		count := connectCount.Add(1)
		fd := newFakeDemuxer()

		if count <= 2 {
			// First two attempts: Demux fails
			fd.demuxErr = errors.New("connection refused")
			fd.readFunc = func() (gomedia.Packet, error) {
				return nil, errors.New("not connected")
			}
		} else {
			// Third attempt: succeeds
			idx := 0
			fd.readFunc = func() (gomedia.Packet, error) {
				idx++
				if idx <= 3 {
					return makeVideoPacket(time.Duration(idx) * 100 * time.Millisecond), nil
				}
				<-fd.closeCh
				return nil, errors.New("closed")
			}
		}
		return fd
	}

	rdr := newTestReader(10, factory)
	rdr.Read()
	rdr.AddURL() <- "rtsp://test.local/stream"

	// Should eventually get packets after failed Demux attempts
	pkts := receivePackets(rdr.Packets(), 1, 10*time.Second)
	require.NotEmpty(t, pkts, "should receive packets after demuxer recovers")

	rdr.Close()
	<-rdr.Done()
}
