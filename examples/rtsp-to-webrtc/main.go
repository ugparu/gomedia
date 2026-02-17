package main

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	pion "github.com/pion/webrtc/v4"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/reader"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/writer/webrtc"
)

type SDPRequest struct {
	SDP string `json:"sdp" binding:"required"`
}

type SDPResponse struct {
	SDP string `json:"sdp"`
	Err string `json:"err"`
}

type URLRequest struct {
	URLs []string `json:"urls" binding:"required,min=1"`
}

type URLResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

var (
	rdr         gomedia.Reader
	urlMutex    sync.Mutex
	currentURLs []string
	webrtcWrt   gomedia.WebRTCStreamer
)

func main() {
	// Initialize reader once at startup
	rdr = reader.NewRTSP(100)
	rdr.Read()
	defer rdr.Close()

	if err := webrtc.Init(2000, 2100, []string{"10.10.0.7"}, []pion.ICEServer{
		{},
	}); err != nil {
		log.Fatalf("Failed to initialize WebRTC: %v", err)
	}

	webrtcWrt = webrtc.New(100, time.Second*6)
	webrtcWrt.Write()
	defer webrtcWrt.Close()

	// Forward packets from reader to webrtc
	go func() {
		for pkt := range rdr.Packets() {
			webrtcWrt.Packets() <- pkt
		}
	}()

	r := gin.Default()

	// Serve the index.html file at root path
	r.StaticFile("/", "./index.html")
	r.StaticFile("/index.html", "./index.html")

	r.POST("/url", func(c *gin.Context) {
		var req URLRequest

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, URLResponse{
				Success: false,
				Message: err.Error(),
			})
			return
		}

		urlMutex.Lock()
		defer urlMutex.Unlock()

		// Create a map of new URLs for quick lookup
		newURLsMap := make(map[string]bool)
		for _, url := range req.URLs {
			if url != "" {
				newURLsMap[url] = true
			}
		}

		// Remove URLs that are no longer needed
		for _, url := range currentURLs {
			if url != "" && !newURLsMap[url] {
				logger.Infof(nil, "Removing URL: %s", url)
				rdr.RemoveURL() <- url
				webrtcWrt.RemoveSource() <- url
			}
		}

		// Create a map of current URLs for quick lookup
		currentURLsMap := make(map[string]bool)
		for _, url := range currentURLs {
			if url != "" {
				currentURLsMap[url] = true
			}
		}

		// Add only missing URLs
		for _, url := range req.URLs {
			if url != "" && !currentURLsMap[url] {
				webrtcWrt.AddSource() <- url
				rdr.AddURL() <- url
				logger.Infof(nil, "Added URL: %s", url)
			}
		}

		// Update currentURLs to match req.URLs
		currentURLs = nil
		for _, url := range req.URLs {
			if url != "" {
				currentURLs = append(currentURLs, url)
			}
		}

		if len(currentURLs) == 0 {
			c.JSON(http.StatusBadRequest, URLResponse{
				Success: false,
				Message: "No valid URLs provided",
			})
			return
		}

		c.JSON(http.StatusOK, URLResponse{
			Success: true,
			Message: "RTSP stream(s) initialized",
		})
	})

	r.POST("/sdp", func(c *gin.Context) {
		var req SDPRequest

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, SDPResponse{
				SDP: "",
				Err: err.Error(),
			})
			return
		}

		urlMutex.Lock()
		hasURLs := len(currentURLs) > 0
		urlMutex.Unlock()

		if !hasURLs {
			c.JSON(http.StatusBadRequest, SDPResponse{
				SDP: "",
				Err: "No URLs available",
			})
			return
		}

		peer := &gomedia.WebRTCPeer{
			SDP:   req.SDP,
			Delay: 0,
			Err:   nil,
			Done:  make(chan struct{}),
		}

		webrtcWrt.Peers() <- peer

		select {
		case <-peer.Done:
			if peer.Err != nil {
				logger.Errorf(nil, "Error adding peer: %v", peer.Err)
				c.JSON(http.StatusInternalServerError, SDPResponse{
					SDP: "",
					Err: peer.Err.Error(),
				})
				return
			}
			c.JSON(http.StatusOK, SDPResponse{
				SDP: peer.SDP,
				Err: "",
			})
		}
	})

	log.Println("Starting HTTP server on :8080")
	if err := r.Run(":8080"); err != nil {
		return
	}
}
