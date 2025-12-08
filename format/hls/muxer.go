package hls

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/format/fmp4"
	"github.com/ugparu/gomedia/utils"
	"github.com/ugparu/gomedia/utils/lifecycle"
	"github.com/ugparu/gomedia/utils/logger"
)

// Constants defining fragment and part parameters.
const (
	fragmentDuration = time.Millisecond * 495
	partTarget       = .5
	maxTS            = time.Hour
)

type segments struct {
	segments map[uint64]*segment
	sync.RWMutex
	segIDs []uint64
}

func (s *segments) addSegment(seg *segment) {
	s.Lock()
	defer s.Unlock()
	s.segments[seg.id] = seg
	s.segIDs = append(s.segIDs, seg.id)
}

func (s *segments) removeSegment(id uint64) {
	s.Lock()
	defer s.Unlock()

	seg, ok := s.segments[id]
	if ok {
		for _, fragment := range seg.fragments {
			for _, packet := range fragment.packets {
				packet.Close()
			}
		}
	}

	delete(s.segments, id)
	for i := 1; i < len(s.segIDs); i++ {
		s.segIDs[i-1] = s.segIDs[i]
	}
	s.segIDs = s.segIDs[:len(s.segIDs)-1]
}

func (s *segments) getSegment(id uint64) (*segment, bool) {
	s.RLock()
	defer s.RUnlock()
	seg, ok := s.segments[id]
	return seg, ok
}

func (s *segments) getCurSegment() *segment {
	s.RLock()
	defer s.RUnlock()
	return s.segments[s.segIDs[len(s.segIDs)-1]]
}

// muxer is an implementation of the HLS interface.
type muxer struct {
	lifecycle.Manager[*muxer] // Embedding lifecycle.Manager to manage lifecycle functions.
	*segments
	segmentCount     uint8                       // Number of segments to keep in the playlist.
	mediaSequence    int64                       // Media sequence number.
	segmentDuration  time.Duration               // Target duration for each segment.
	fragmentDuration time.Duration               // Target duration for each fragment.
	manifest         string                      // Atomic value to store the HLS manifest.
	indexChan        chan struct{}               // Channel for signaling index changes.
	header           string                      // Initial part of the HLS playlist.
	codecPars        gomedia.CodecParametersPair // Codec parameters for video and audio.
}

// NewHLSMuxer creates a new HLS muxer with the specified segment duration and segment count.
func NewHLSMuxer(segmentDuration time.Duration, segmentCount uint8) gomedia.HLSMuxer {
	newHLS := &muxer{
		Manager: nil,
		segments: &segments{
			segments: make(map[uint64]*segment),
			segIDs:   []uint64{},
			RWMutex:  sync.RWMutex{},
		},
		segmentCount:     segmentCount,
		mediaSequence:    0,
		segmentDuration:  segmentDuration,
		fragmentDuration: fragmentDuration,
		manifest:         "",
		indexChan:        make(chan struct{}),
		header: fmt.Sprintf(`#EXTM3U
#EXT-X-VERSION:7
#EXT-X-TARGETDURATION:%d
#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES,PART-HOLD-BACK=%.5f	
#EXT-X-MAP:URI="init.mp4"
#EXT-X-PART-INF:PART-TARGET=%.5f
`, int(segmentDuration.Seconds()), segmentDuration.Seconds(), partTarget),
		codecPars: gomedia.CodecParametersPair{AudioCodecParameters: nil, VideoCodecParameters: nil, URL: ""},
	}
	newHLS.Manager = lifecycle.NewDefaultManager(newHLS)
	return newHLS
}

// Mux initializes the HLS muxer with codec parameters.
func (mxr *muxer) Mux(codecPars gomedia.CodecParametersPair) (err error) {
	startFunc := func(mxr *muxer) error {
		// Check if video or audio codec parameters are provided.
		if codecPars.VideoCodecParameters == nil && codecPars.AudioCodecParameters == nil {
			return &utils.NoCodecDataError{}
		}

		mux := fmp4.NewMuxer()
		if err = mux.Mux(codecPars); err != nil {
			return err
		}

		mxr.codecPars = codecPars
		// Create a new segment and set it as the current segment.
		newSeg := newSegment(0, mxr.fragmentDuration, mxr.segmentDuration, codecPars)
		mxr.addSegment(newSeg)

		return nil
	}
	return mxr.Manager.Start(startFunc)
}

// WritePacket writes a multimedia packet to the current fragment of the current segment.
func (mxr *muxer) WritePacket(inpPkt gomedia.Packet) (err error) {
	if inpPkt == nil {
		return &utils.NilPacketError{}
	}

	inpPkt.SetTimestamp(inpPkt.Timestamp() % maxTS)

	if err = mxr.getCurSegment().writePacket(inpPkt); err != nil {
		return err
	}

	select {
	case <-mxr.getCurSegment().finished:
		// The current segment has finished, create a new one and update the segment list.
		newSegID := mxr.getCurSegment().id + 1
		newSeg := newSegment(newSegID, mxr.fragmentDuration, mxr.segmentDuration, mxr.codecPars)
		mxr.addSegment(newSeg)
		// Manage the number of segments to keep.
		segCount := len(mxr.segIDs)
		// Use safe conversion to avoid integer overflow
		if segCount > int(mxr.segmentCount) {
			mxr.removeSegment(mxr.segIDs[0])
			mxr.mediaSequence++
		}
	default:
	}

	// Update the HLS manifest and signal the change.
	mxr.manifest = mxr.updateIndexM3u8()
	for {
		select {
		case mxr.indexChan <- struct{}{}:
		default:
			return
		}
	}
}

// updateIndexM3u8 generates and returns the updated HLS manifest.
func (mxr *muxer) updateIndexM3u8() string {
	var index strings.Builder
	index.WriteString(mxr.header)
	index.WriteString(fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", mxr.mediaSequence))
	for _, id := range mxr.segIDs {
		segment, ok := mxr.getSegment(id)
		if !ok {
			logger.Errorf(mxr, "Segment %d not found in map", id)
			continue
		}

		if segment.manifestEntry == "" {
			logger.Errorf(mxr, "Manifest entry for segment %d is nil", id)
			continue
		}

		index.WriteString(segment.manifestEntry)
	}

	return index.String()
}

// GetIndexM3u8 returns the HLS manifest based on the requested segment and part.
func (mxr *muxer) GetIndexM3u8(ctx context.Context, needSeg int64, needPart int8) (string, error) {
	if needSeg < 0 {
		return mxr.manifest, nil
	}

	// Wait for segment or part
	waitErr := mxr.waitForSegmentOrPart(ctx, needSeg, needPart)
	if waitErr != nil {
		return "", waitErr
	}

	const timeout = time.Second * 3
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-mxr.indexChan:
	}

	return mxr.manifest, nil
}

// waitForSegmentOrPart is a helper function to wait for a segment or part
// to be available. This reduces complexity in the GetIndexM3u8 function.
func (mxr *muxer) waitForSegmentOrPart(ctx context.Context, needSeg int64, needPart int8) error {
	// Check for potential overflow before conversion
	if needSeg < 0 {
		logger.Errorf(mxr, "Segment index %d is negative", needSeg)
		return errors.New("segment index cannot be negative")
	}

	// Safe conversion after check
	needSegUint64 := uint64(needSeg)

	// Determine if we need to wait for a specific part
	shouldWaitForPart := needPart >= 0

	for {
		// Check if context is done before proceeding
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Continue with the logic
		}

		curSeg := mxr.getCurSegment()

		// First check if we've already passed the needed segment
		if curSeg.id > needSegUint64 {
			return nil
		}

		// If we're at the right segment but don't need a specific part
		if curSeg.id == needSegUint64 && !shouldWaitForPart {
			return nil
		}

		// If we're at the right segment and need a specific part
		if curSeg.id == needSegUint64 && shouldWaitForPart {
			// Wait for the specific part
			// We wrap this call in a select to handle context cancellation
			doneCh := make(chan struct{})
			go func() {
				defer close(doneCh)

				// Convert needPart to uint8 safely for the waitFragment call
				// Ensure we don't convert negative values to prevent integer overflow
				var partIndex uint8
				if needPart < 0 {
					partIndex = 0
				} else {
					partIndex = uint8(needPart)
				}
				curSeg.waitFragment(ctx, partIndex)
			}()

			// Wait for either context cancellation or part to be available
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-doneCh:
				return nil
			}
		}

		// Wait for the current segment to finish before continuing the loop
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-curSeg.finished:
			// Continue waiting in the loop for the next segment
		}
	}
}

// GetInit returns the MP4 initialization segment.
func (mxr *muxer) GetInit() ([]byte, error) {
	mux := fmp4.NewMuxer()
	if err := mux.Mux(mxr.codecPars); err != nil {
		return nil, err
	}
	buf := mux.GetInit()
	defer buf.Release()
	if len(buf.Data()) == 0 {
		return nil, errors.New("empty init buffer")
	}
	return buf.Data(), nil
}

// GetSegment returns the MP4 content of a specific segment.
func (mxr *muxer) GetSegment(ctx context.Context, index uint64) ([]byte, error) {
	seg, ok := mxr.getSegment(index)
	if !ok {
		return nil, errors.New("segment not found")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-seg.finished:
	}
	buf := seg.getMp4Buffer()
	defer buf.Release()
	return buf.Data(), nil
}

// GetFragment returns the MP4 content of a specific fragment within a segment.
func (mxr *muxer) GetFragment(ctx context.Context, segindex uint64, index uint8) ([]byte, error) {
	seg, ok := mxr.getSegment(segindex)
	if !ok {
		return nil, errors.New("segment not found")
	}

	// waitFragment doesn't return an error, so don't try to handle one
	seg.waitFragment(ctx, index)

	buf := seg.getFragment(ctx, index)
	defer buf.Release()
	bytes := buf.Data()

	if len(bytes) == 0 {
		return nil, errors.New("fragment not found")
	}
	return bytes, nil
}

// Close_ closes the HLS muxer.
func (mxr *muxer) Close_() { //nolint:revive // Method name required by interface
}

// GetMasterEntry returns the master entry for the HLS playlist.
func (mxr *muxer) GetMasterEntry() (string, error) {
	if mxr.codecPars.VideoCodecParameters == nil {
		return "", errors.New("no video codec")
	}
	w, h := mxr.codecPars.VideoCodecParameters.Width(), mxr.codecPars.VideoCodecParameters.Height()
	codecsStr := mxr.codecPars.VideoCodecParameters.Tag()
	if mxr.codecPars.AudioCodecParameters != nil {
		codecsStr += fmt.Sprintf(",%s", mxr.codecPars.AudioCodecParameters.Tag())
	}

	// Format the master playlist entry with proper line breaks to keep the line length manageable
	masterEntry := fmt.Sprintf(
		"#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"%s\"",
		mxr.codecPars.VideoCodecParameters.Bitrate(),
		w,
		h,
		codecsStr,
	)
	return masterEntry, nil
}

// String returns a string representation of the HLS muxer.
func (mxr *muxer) String() string {
	return "HLS_MUXER"
}
