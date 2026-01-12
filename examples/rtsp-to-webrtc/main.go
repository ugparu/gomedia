package main

import (
	"log"
	"net/http"
	"runtime"
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
	Err error  `json:"err"`
}

type URLRequest struct {
	URL string `json:"url" binding:"required"`
}

type URLResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

var (
	rdr        gomedia.Reader
	urlMutex   sync.Mutex
	currentURL string
	webrtcWrt  gomedia.WebRTCStreamer
)

func main() {
	go func() {
		for {
			runtime.GC()
			logger.Infof(nil, "GC called")
			time.Sleep(time.Second * 10)
		}
	}()

	// Initialize reader once at startup
	rdr = reader.NewRTSP(100)
	rdr.Read()
	defer rdr.Close()

	webrtc.Init(2000, 2100, []string{"192.168.1.156"}, []pion.ICEServer{
		{
			URLs: []string{"stun:stun.l.google.com:19302"},
		},
	})

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

		// Remove existing URL if any
		if currentURL != "" {
			logger.Infof(nil, "Removing existing URL: %s", currentURL)
			rdr.RemoveURL() <- currentURL
			webrtcWrt.RemoveSource() <- currentURL
		}

		// Add new URL
		currentURL = req.URL
		rdr.AddURL() <- currentURL

		c.JSON(http.StatusOK, URLResponse{
			Success: true,
			Message: "RTSP stream initialized",
		})
	})

	r.POST("/sdp", func(c *gin.Context) {
		var req SDPRequest

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, SDPResponse{
				SDP: "",
				Err: err,
			})
			return
		}

		urlMutex.Lock()
		hasURL := currentURL != ""
		urlMutex.Unlock()

		if !hasURL {
			c.JSON(http.StatusBadRequest, SDPResponse{
				SDP: "",
				Err: nil,
			})
			return
		}

		webrtcWrt.Peers() <- gomedia.WebRTCPeer{
			SDP:   req.SDP,
			Delay: 0,
			Err:   nil,
		}

		resp := <-webrtcWrt.Peers()
		if resp.Err != nil {
			c.JSON(http.StatusInternalServerError, SDPResponse{
				SDP: "",
				Err: resp.Err,
			})
			return
		}

		c.JSON(http.StatusOK, SDPResponse{
			SDP: resp.SDP,
			Err: resp.Err,
		})
	})

	log.Println("Starting HTTP server on :8080")
	if err := r.Run(":8080"); err != nil {
		return
	}
}
