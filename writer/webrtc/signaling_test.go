//nolint:mnd // Test file contains many magic numbers for expected values
package webrtc

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugparu/gomedia"
)

// ---------------------------------------------------------------------------
// DefaultSignalingHandler tests
// ---------------------------------------------------------------------------

func TestDefaultSignaling_BuildAvailableStreams(t *testing.T) {
	h := &DefaultSignalingHandler{}

	resolutions := []gomedia.Resolution{
		{URL: "rtsp://cam1", Width: 640, Height: 480, Codec: "H264"},
		{URL: "rtsp://cam2", Width: 1920, Height: 1080, Codec: "H264"},
	}

	data, err := h.BuildAvailableStreams(resolutions)
	require.NoError(t, err)

	var msg codecReq
	require.NoError(t, json.Unmarshal(data, &msg))

	assert.Equal(t, "setAvailableStreams", msg.Command)
	assert.Equal(t, "video", msg.Message.Type)
	assert.Len(t, msg.Message.Resolutions, 2)
	assert.Equal(t, "rtsp://cam1", msg.Message.Resolutions[0].URL)
	assert.Equal(t, 640, msg.Message.Resolutions[0].Width)
	assert.Equal(t, 480, msg.Message.Resolutions[0].Height)
	assert.Equal(t, "H264", msg.Message.Resolutions[0].Codec)
	assert.Equal(t, 1920, msg.Message.Resolutions[1].Width)
}

func TestDefaultSignaling_BuildAvailableStreams_Empty(t *testing.T) {
	h := &DefaultSignalingHandler{}

	data, err := h.BuildAvailableStreams(nil)
	require.NoError(t, err)

	var msg codecReq
	require.NoError(t, json.Unmarshal(data, &msg))

	assert.Equal(t, "setAvailableStreams", msg.Command)
	assert.Len(t, msg.Message.Resolutions, 0)
}

func TestDefaultSignaling_BuildStreamStarted(t *testing.T) {
	h := &DefaultSignalingHandler{}

	data, err := h.BuildStreamStarted()
	require.NoError(t, err)

	var msg dataChanReq
	require.NoError(t, json.Unmarshal(data, &msg))

	assert.Equal(t, "startStream", msg.Command)
	assert.Empty(t, msg.Token)
}

func TestDefaultSignaling_BuildStreamMoved_WithToken(t *testing.T) {
	h := &DefaultSignalingHandler{}

	data, err := h.BuildStreamMoved("tok123", "rtsp://cam2")
	require.NoError(t, err)

	var msg resp
	require.NoError(t, json.Unmarshal(data, &msg))

	assert.Equal(t, "tok123", msg.Token)
	assert.Equal(t, http.StatusOK, msg.Status)
	assert.Equal(t, "Ok", msg.Message)
}

func TestDefaultSignaling_BuildStreamMoved_WithoutToken(t *testing.T) {
	h := &DefaultSignalingHandler{}

	data, err := h.BuildStreamMoved("", "rtsp://cam2")
	require.NoError(t, err)

	var msg dataChanReq
	require.NoError(t, json.Unmarshal(data, &msg))

	assert.Equal(t, "setStreamUrl", msg.Command)
	assert.Equal(t, "rtsp://cam2", msg.Message)
	assert.Empty(t, msg.Token)
}

func TestDefaultSignaling_BuildErrorResponse(t *testing.T) {
	h := &DefaultSignalingHandler{}

	data, err := h.BuildErrorResponse("tok456")
	require.NoError(t, err)

	var msg resp
	require.NoError(t, json.Unmarshal(data, &msg))

	assert.Equal(t, "tok456", msg.Token)
	assert.Equal(t, http.StatusNotFound, msg.Status)
	assert.Equal(t, "Not Found", msg.Message)
}

func TestDefaultSignaling_ParseMessage(t *testing.T) {
	h := &DefaultSignalingHandler{}

	input := `{"token":"tok789","command":"setStreamUrl","message":"rtsp://cam3"}`
	token, targetURL, err := h.ParseMessage([]byte(input))
	require.NoError(t, err)

	assert.Equal(t, "tok789", token)
	assert.Equal(t, "rtsp://cam3", targetURL)
}

func TestDefaultSignaling_ParseMessage_InvalidJSON(t *testing.T) {
	h := &DefaultSignalingHandler{}

	_, _, err := h.ParseMessage([]byte("not json"))
	assert.Error(t, err)
}

func TestDefaultSignaling_Roundtrip(t *testing.T) {
	h := &DefaultSignalingHandler{}

	// Build a stream moved command and parse it back
	data, err := h.BuildStreamMoved("", "rtsp://cam5")
	require.NoError(t, err)

	token, url, err := h.ParseMessage(data)
	require.NoError(t, err)
	assert.Empty(t, token)
	assert.Equal(t, "rtsp://cam5", url)
}
