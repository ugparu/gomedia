package webrtc

import (
	"encoding/json"
	"net/http"

	"github.com/ugparu/gomedia"
)

// SignalingHandler abstracts the data channel signaling protocol.
// Implement this interface to use a custom message format between
// the WebRTC server and client.
type SignalingHandler interface {
	// BuildAvailableStreams creates a message notifying the client about available streams.
	BuildAvailableStreams(resolutions []gomedia.Resolution) ([]byte, error)
	// BuildStreamStarted creates a message sent after peer seeding completes.
	BuildStreamStarted() ([]byte, error)
	// BuildStreamMoved creates a message sent when a peer's stream changes.
	// token is non-empty when this is a response to a client-initiated request.
	BuildStreamMoved(token, url string) ([]byte, error)
	// BuildErrorResponse creates an error response for an invalid stream request.
	BuildErrorResponse(token string) ([]byte, error)
	// ParseMessage parses an incoming data channel message.
	// Returns the token and the target stream URL the client wants to switch to.
	ParseMessage(data []byte) (token, targetURL string, err error)
}

// DefaultSignalingHandler implements the default signaling protocol.
type DefaultSignalingHandler struct{}

func (d *DefaultSignalingHandler) BuildAvailableStreams(resolutions []gomedia.Resolution) ([]byte, error) {
	reqMsg := &codecReq{
		Token:   "",
		Command: "setAvailableStreams",
		Message: codec{
			Type:        "video",
			Resolutions: make([]resolution, 0, len(resolutions)),
		},
	}
	for _, r := range resolutions {
		reqMsg.Message.Resolutions = append(reqMsg.Message.Resolutions, resolution{
			URL:    r.URL,
			Width:  r.Width,
			Height: r.Height,
			Codec:  r.Codec,
		})
	}
	return json.Marshal(reqMsg)
}

func (d *DefaultSignalingHandler) BuildStreamStarted() ([]byte, error) {
	return json.Marshal(&dataChanReq{
		Token:   "",
		Command: "startStream",
		Message: "",
	})
}

func (d *DefaultSignalingHandler) BuildStreamMoved(token, url string) ([]byte, error) {
	if token == "" {
		return json.Marshal(&dataChanReq{
			Token:   "",
			Command: "setStreamUrl",
			Message: url,
		})
	}
	return json.Marshal(&resp{
		Token:   token,
		Status:  http.StatusOK,
		Message: "Ok",
	})
}

func (d *DefaultSignalingHandler) BuildErrorResponse(token string) ([]byte, error) {
	return json.Marshal(&resp{
		Token:   token,
		Status:  http.StatusNotFound,
		Message: "Not Found",
	})
}

func (d *DefaultSignalingHandler) ParseMessage(data []byte) (token, targetURL string, err error) {
	req := &dataChanReq{}
	if err = json.Unmarshal(data, req); err != nil {
		return "", "", err
	}
	return req.Token, req.Message, nil
}
