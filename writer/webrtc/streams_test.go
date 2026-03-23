//nolint:mnd // Test file contains many magic numbers for expected values
package webrtc

import (
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/utils/logger"
)

// ---------------------------------------------------------------------------
// sortedStreams helpers
// ---------------------------------------------------------------------------

func newTestSortedStreams() *sortedStreams {
	return &sortedStreams{
		log:            logger.Default,
		sortedURLs:     []string{},
		streams:        map[string]*stream{},
		pendingPeers:   map[*peerTrack]bool{},
		targetDuration: 5 * time.Second,
		signaling:      &DefaultSignalingHandler{},
	}
}

func newTestPeerTrack(targetURL string) *peerTrack {
	return &peerTrack{
		PeerConnection: nil, // We don't need a real connection for stream tests
		log:            logger.Default,
		targetURL:      targetURL,
		vChan:          make(chan gomedia.VideoPacket, 500),
		aChan:          make(chan gomedia.AudioPacket, 500),
		vflush:         make(chan struct{}, 1),
		aflush:         make(chan struct{}, 1),
		done:           make(chan struct{}),
		DataChannel:    &webrtc.DataChannel{},
	}
}

// ---------------------------------------------------------------------------
// Exists
// ---------------------------------------------------------------------------

func TestSortedStreams_Exists_ReturnsFalseForMissing(t *testing.T) {
	ss := newTestSortedStreams()
	assert.False(t, ss.Exists("rtsp://nonexistent"))
}

func TestSortedStreams_Exists_ReturnsTrueAfterAdd(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")
	ss.Add("rtsp://cam1", videoCp)
	assert.True(t, ss.Exists("rtsp://cam1"))
}

// ---------------------------------------------------------------------------
// Add
// ---------------------------------------------------------------------------

func TestSortedStreams_Add_CreatesStream(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")

	moved := ss.Add("rtsp://cam1", videoCp)
	assert.Nil(t, moved)
	assert.Len(t, ss.sortedURLs, 1)
	assert.Equal(t, "rtsp://cam1", ss.sortedURLs[0])
	assert.NotNil(t, ss.streams["rtsp://cam1"])
	assert.NotNil(t, ss.streams["rtsp://cam1"].buffer)
}

func TestSortedStreams_Add_RejectsAudioOnlyCodec(t *testing.T) {
	ss := newTestSortedStreams()
	_, _, audioCp := loadTestCodecPair(t, "rtsp://cam1")

	moved := ss.Add("rtsp://cam1", audioCp)
	assert.Nil(t, moved)
	assert.False(t, ss.Exists("rtsp://cam1"))
}

func TestSortedStreams_Add_IgnoresDuplicate(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")

	ss.Add("rtsp://cam1", videoCp)
	moved := ss.Add("rtsp://cam1", videoCp)
	assert.Nil(t, moved)
	assert.Len(t, ss.sortedURLs, 1) // still 1
}

func TestSortedStreams_Add_MovesPendingPeers(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")

	// Create pending peer
	pt := newTestPeerTrack("rtsp://cam1")
	ss.pendingPeers[pt] = true

	moved := ss.Add("rtsp://cam1", videoCp)
	assert.Len(t, moved, 1)
	assert.Equal(t, pt, moved[0])
	assert.Len(t, ss.pendingPeers, 0) // removed from pending
	assert.True(t, ss.streams["rtsp://cam1"].tracks[pt])
}

// ---------------------------------------------------------------------------
// Remove
// ---------------------------------------------------------------------------

func TestSortedStreams_Remove_NonexistentIsNoOp(t *testing.T) {
	ss := newTestSortedStreams()
	ss.Remove("rtsp://nonexistent") // should not panic
}

func TestSortedStreams_Remove_DeletesStream(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")
	ss.Add("rtsp://cam1", videoCp)

	ss.Remove("rtsp://cam1")
	assert.False(t, ss.Exists("rtsp://cam1"))
	assert.Len(t, ss.sortedURLs, 0)
}

func TestSortedStreams_Remove_MovesPeersToAlternateStream(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")

	ss.Add("rtsp://cam1", videoCp)
	ss.Add("rtsp://cam2", videoCp)

	pt := newTestPeerTrack("rtsp://cam1")
	ss.streams["rtsp://cam1"].tracks[pt] = true

	ss.Remove("rtsp://cam1")

	// Peer should be in cam2's toAdd (via Move)
	assert.NotNil(t, ss.streams["rtsp://cam2"].toAdd[pt])
}

func TestSortedStreams_Remove_SavesPeersToPendingWhenLastStream(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")
	ss.Add("rtsp://cam1", videoCp)

	pt := newTestPeerTrack("rtsp://cam1")
	ss.streams["rtsp://cam1"].tracks[pt] = true

	ss.Remove("rtsp://cam1")

	assert.True(t, ss.pendingPeers[pt])
}

func TestSortedStreams_Remove_HandlesToAddPeers(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")
	ss.Add("rtsp://cam1", videoCp)

	// A peer in toAdd (was requested to move here)
	pt := newTestPeerTrack("rtsp://cam1")
	ss.streams["rtsp://cam1"].toAdd[pt] = &peerURL{peerTrack: pt, URL: "rtsp://cam1"}

	ss.Remove("rtsp://cam1")

	// Should be saved to pending since this is the last stream
	assert.True(t, ss.pendingPeers[pt])
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func TestSortedStreams_Update_NonexistentReturnsFalse(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")
	_, changed := ss.Update("rtsp://cam1", videoCp)
	assert.False(t, changed)
}

func TestSortedStreams_Update_SameParamsReturnsFalse(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")
	ss.Add("rtsp://cam1", videoCp)

	_, changed := ss.Update("rtsp://cam1", videoCp)
	assert.False(t, changed)
}

func TestSortedStreams_Update_AudioParamsUpdate(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, audioCp := loadTestCodecPair(t, "rtsp://cam1")
	ss.Add("rtsp://cam1", videoCp)

	_, changed := ss.Update("rtsp://cam1", audioCp)
	assert.True(t, changed)
	assert.Equal(t, audioCp, ss.streams["rtsp://cam1"].codecPar.AudioCodecParameters)
}

// ---------------------------------------------------------------------------
// Insert
// ---------------------------------------------------------------------------

func TestSortedStreams_Insert_AddsToTargetStream(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")
	ss.Add("rtsp://cam1", videoCp)

	pt := newTestPeerTrack("rtsp://cam1")
	err := ss.Insert(pt)
	require.NoError(t, err)

	seeded, ok := ss.streams["rtsp://cam1"].tracks[pt]
	assert.True(t, ok)
	assert.False(t, seeded) // not seeded yet
}

func TestSortedStreams_Insert_UnknownURLReturnsError(t *testing.T) {
	ss := newTestSortedStreams()
	pt := newTestPeerTrack("rtsp://nonexistent")
	err := ss.Insert(pt)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rtsp://nonexistent")
}

// ---------------------------------------------------------------------------
// Move
// ---------------------------------------------------------------------------

func TestSortedStreams_Move_AddsToTargetToAdd(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")
	ss.Add("rtsp://cam1", videoCp)
	ss.Add("rtsp://cam2", videoCp)

	pt := newTestPeerTrack("rtsp://cam1")
	ss.streams["rtsp://cam1"].tracks[pt] = true

	pu := &peerURL{peerTrack: pt, Token: "tok123", URL: "rtsp://cam2"}
	err := ss.Move(pu)
	require.NoError(t, err)

	assert.NotNil(t, ss.streams["rtsp://cam2"].toAdd[pt])
}

func TestSortedStreams_Move_NonexistentTargetReturnsError(t *testing.T) {
	ss := newTestSortedStreams()
	pt := newTestPeerTrack("rtsp://cam1")
	pu := &peerURL{peerTrack: pt, URL: "rtsp://nonexistent"}
	err := ss.Move(pu)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// sortURLsByResolution
// ---------------------------------------------------------------------------

func TestSortedStreams_SortedByResolution(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")

	// All streams use the same test codec params (same resolution),
	// so they should maintain insertion order
	ss.Add("rtsp://cam1", videoCp)
	ss.Add("rtsp://cam2", videoCp)
	ss.Add("rtsp://cam3", videoCp)

	assert.Len(t, ss.sortedURLs, 3)
}

// ---------------------------------------------------------------------------
// validatePacket
// ---------------------------------------------------------------------------

func TestSortedStreams_ValidatePacket_NilPacket(t *testing.T) {
	ss := newTestSortedStreams()
	_, err := ss.validatePacket(nil)
	assert.Error(t, err)
}

func TestSortedStreams_ValidatePacket_TooSmall(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	ss.Add("rtsp://test", videoCp)

	// Create a very small packet (less than minPktSz=5)
	absTime := time.Now()
	data := []byte{0x01, 0x02}
	pkt := h264.NewPacket(false, 0, absTime, data, "rtsp://test", videoCp)
	pkt.SetDuration(33 * time.Millisecond)

	_, err := ss.validatePacket(pkt)
	assert.ErrorIs(t, err, ErrPacketTooSmall)
}

func TestSortedStreams_ValidatePacket_UnknownURL(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")

	absTime := time.Now()
	pkt := makeVideoPacket(t, videoCp, "rtsp://unknown", true, 0, 33*time.Millisecond, absTime)

	_, err := ss.validatePacket(pkt)
	assert.ErrorIs(t, err, ErrStreamNotFound)
}

func TestSortedStreams_ValidatePacket_Success(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	ss.Add("rtsp://test", videoCp)

	absTime := time.Now()
	pkt := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 33*time.Millisecond, absTime)

	str, err := ss.validatePacket(pkt)
	require.NoError(t, err)
	assert.NotNil(t, str)
}

// ---------------------------------------------------------------------------
// writePacket
// ---------------------------------------------------------------------------

func TestSortedStreams_WritePacket_DropsPreKeyframe(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	ss.Add("rtsp://test", videoCp)

	absTime := time.Now()
	pkt := makeVideoPacket(t, videoCp, "rtsp://test", false, 0, 33*time.Millisecond, absTime)

	err := ss.writePacket(pkt)
	assert.NoError(t, err)
	// Buffer should still be empty (no keyframe yet)
	assert.Len(t, ss.streams["rtsp://test"].buffer.gops, 0)
}

func TestSortedStreams_WritePacket_StoresKeyframe(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	ss.Add("rtsp://test", videoCp)

	absTime := time.Now()
	pkt := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 33*time.Millisecond, absTime)

	err := ss.writePacket(pkt)
	assert.NoError(t, err)
	assert.Len(t, ss.streams["rtsp://test"].buffer.gops, 1)
}

func TestSortedStreams_WritePacket_DistributesToSeededPeers(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	ss.Add("rtsp://test", videoCp)

	pt := newTestPeerTrack("rtsp://test")
	ss.streams["rtsp://test"].tracks[pt] = true // marked as seeded

	absTime := time.Now()
	pkt := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 33*time.Millisecond, absTime)

	err := ss.writePacket(pkt)
	assert.NoError(t, err)

	// Peer should have received a cloned packet
	select {
	case vPkt := <-pt.vChan:
		assert.NotNil(t, vPkt)
		vPkt.Release()
	case <-time.After(time.Second):
		t.Fatal("expected video packet on peer channel")
	}
}

func TestSortedStreams_WritePacket_DropsForFullChannel(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	ss.Add("rtsp://test", videoCp)

	// Create peer with tiny channel to force drops
	pt := &peerTrack{
		log:       logger.Default,
		targetURL: "rtsp://test",
		vChan:     make(chan gomedia.VideoPacket, 1), // size 1
		aChan:     make(chan gomedia.AudioPacket, 1),
		vflush:    make(chan struct{}, 1),
		aflush:    make(chan struct{}, 1),
		done:      make(chan struct{}),
	}
	ss.streams["rtsp://test"].tracks[pt] = true // seeded

	absTime := time.Now()

	// Fill the channel
	kf := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 33*time.Millisecond, absTime)
	err := ss.writePacket(kf)
	require.NoError(t, err)

	// This one should be dropped (channel full), not block
	p2 := makeVideoPacket(t, videoCp, "rtsp://test", false, 33*time.Millisecond, 33*time.Millisecond, absTime.Add(33*time.Millisecond))
	err = ss.writePacket(p2)
	assert.NoError(t, err) // should not error, just drop

	// Drain
	<-pt.vChan
}

func TestSortedStreams_WritePacket_SkipsClosedPeer(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://test")
	ss.Add("rtsp://test", videoCp)

	pt := newTestPeerTrack("rtsp://test")
	ss.streams["rtsp://test"].tracks[pt] = true // seeded
	close(pt.done)                               // mark as closed

	absTime := time.Now()
	pkt := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 33*time.Millisecond, absTime)

	err := ss.writePacket(pkt)
	assert.NoError(t, err)
	// Should not panic or block, just skip the closed peer
	assert.Len(t, pt.vChan, 0)
}

func TestSortedStreams_WritePacket_AudioDistribution(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, audioCp := loadTestCodecPair(t, "rtsp://test")
	ss.Add("rtsp://test", videoCp)

	pt := newTestPeerTrack("rtsp://test")
	ss.streams["rtsp://test"].tracks[pt] = true // seeded

	absTime := time.Now()

	// First send a keyframe to start the buffer
	kf := makeVideoPacket(t, videoCp, "rtsp://test", true, 0, 33*time.Millisecond, absTime)
	err := ss.writePacket(kf)
	require.NoError(t, err)
	<-pt.vChan // drain video

	// Now send audio
	aPkt := makeAudioPacket(t, audioCp, "rtsp://test", 0, 21*time.Millisecond, absTime)
	err = ss.writePacket(aPkt)
	assert.NoError(t, err)

	select {
	case a := <-pt.aChan:
		assert.NotNil(t, a)
		a.Release()
	case <-time.After(time.Second):
		t.Fatal("expected audio packet on peer channel")
	}
}

// ---------------------------------------------------------------------------
// writePacket with real test data
// ---------------------------------------------------------------------------

func TestSortedStreams_WritePacket_RealData(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, audioCp := loadTestCodecPair(t, "rtsp://test")
	ss.Add("rtsp://test", videoCp)
	ss.Update("rtsp://test", audioCp)

	pt := newTestPeerTrack("rtsp://test")
	ss.streams["rtsp://test"].tracks[pt] = true // seeded

	packets := loadTestPackets(t, "rtsp://test", videoCp, audioCp, 50)

	videoCount := 0
	audioCount := 0
	for _, pkt := range packets {
		err := ss.writePacket(pkt)
		require.NoError(t, err)
	}

	// Drain peer channels and count
	for {
		select {
		case pkt := <-pt.vChan:
			videoCount++
			pkt.Release()
		case pkt := <-pt.aChan:
			audioCount++
			pkt.Release()
		default:
			goto done
		}
	}
done:
	assert.Greater(t, videoCount, 0, "expected video packets delivered to peer")
	assert.Greater(t, audioCount, 0, "expected audio packets delivered to peer")
}

// ---------------------------------------------------------------------------
// processPendingTracks / moveTrackToStream
// ---------------------------------------------------------------------------

func TestSortedStreams_MoveTrackToStream_SendsBufferedPackets(t *testing.T) {
	ss := newTestSortedStreams()
	_, videoCp, _ := loadTestCodecPair(t, "rtsp://cam1")
	ss.Add("rtsp://cam1", videoCp)
	ss.Add("rtsp://cam2", videoCp)

	// Build up some buffer on cam2
	absTime := time.Now()
	kf := makeVideoPacket(t, videoCp, "rtsp://cam2", true, 0, 33*time.Millisecond, absTime)
	ss.writePacket(kf)
	p := makeVideoPacket(t, videoCp, "rtsp://cam2", false, 33*time.Millisecond, 33*time.Millisecond, absTime.Add(33*time.Millisecond))
	ss.writePacket(p)

	// Peer on cam1 requests move to cam2
	pt := newTestPeerTrack("rtsp://cam1")
	ss.streams["rtsp://cam1"].tracks[pt] = true

	pu := &peerURL{peerTrack: pt, Token: "tok", URL: "rtsp://cam2"}
	err := ss.Move(pu)
	require.NoError(t, err)

	// Send a keyframe on cam2 to trigger processPendingTracks
	kf2 := makeVideoPacket(t, videoCp, "rtsp://cam2", true, 66*time.Millisecond, 33*time.Millisecond, absTime.Add(66*time.Millisecond))
	err = ss.writePacket(kf2)
	require.NoError(t, err)

	// Peer should have received buffered + new packets
	receivedCount := 0
	for {
		select {
		case pkt := <-pt.vChan:
			receivedCount++
			pkt.Release()
		default:
			goto done
		}
	}
done:
	assert.Greater(t, receivedCount, 0, "peer should receive packets after move")

	// Peer should now be in cam2's tracks, not cam1's
	_, inCam1 := ss.streams["rtsp://cam1"].tracks[pt]
	assert.False(t, inCam1)
	seeded, inCam2 := ss.streams["rtsp://cam2"].tracks[pt]
	assert.True(t, inCam2)
	assert.True(t, seeded)
}
