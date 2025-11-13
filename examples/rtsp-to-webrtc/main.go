package main

import (
	"log"
	"net/http"
	"os"
	"runtime"
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

func main() {
	go func() {
		for {
			runtime.GC()
			logger.Infof(nil, "GC called")
			time.Sleep(time.Second * 10)
		}
	}()

	rdr := reader.NewRTSP(0)
	rdr.Read()
	defer rdr.Close()
	rdr.AddURL() <- os.Getenv("RTSP_URL")

	webrtc.Init(2000, 2100, []string{"192.168.1.145"}, []pion.ICEServer{
		{
			URLs: []string{"stun:stun.l.google.com:19302"},
		},
	})

	webrtc := webrtc.New(0)
	webrtc.Write()
	defer webrtc.Close()

	go func() {
		for pkt := range rdr.Packets() {
			webrtc.Packets() <- pkt
		}
	}()

	r := gin.Default()

	// Serve the index.html file at root path
	r.StaticFile("/", "./index.html")
	r.StaticFile("/index.html", "./index.html")

	r.POST("/sdp", func(c *gin.Context) {
		var req SDPRequest

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, SDPResponse{
				SDP: "",
				Err: err,
			})
			return
		}

		webrtc.Peers() <- gomedia.WebRTCPeer{
			SDP:   req.SDP,
			Delay: 0,
			Err:   nil,
		}

		resp := <-webrtc.Peers()
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
