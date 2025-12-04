package main

import (
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	pion "github.com/pion/webrtc/v4"
	"github.com/sirupsen/logrus"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	aacDec "github.com/ugparu/gomedia/decoder/aac"
	"github.com/ugparu/gomedia/decoder/opus"
	"github.com/ugparu/gomedia/decoder/pcm"
	"github.com/ugparu/gomedia/encoder"
	"github.com/ugparu/gomedia/encoder/aac"
	pcmEnc "github.com/ugparu/gomedia/encoder/pcm"
	"github.com/ugparu/gomedia/reader"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/writer/hls"
	"github.com/ugparu/gomedia/writer/segmenter"
	"github.com/ugparu/gomedia/writer/webrtc"
)

var rtspURLs = strings.Split(os.Getenv("RTSP_URLS"), ",")

const segSize = 5 * time.Second

// Writers
var (
	hlsWr    = hls.New(1, 2, segSize, 100)
	webrtcWr gomedia.WebRTCStreamer
	seg      gomedia.Segmenter
)

// Audio processing state
var audioPktType gomedia.CodecType

// WebRTC SDP request/response
type SDPRequest struct {
	SDP string `json:"sdp" binding:"required"`
}

type SDPResponse struct {
	SDP string `json:"sdp"`
	Err error  `json:"err"`
}

func main() {
	logrus.Info("Starting unified RTSP streaming server...")
	logrus.SetLevel(logrus.InfoLevel)

	// Initialize HLS writer
	hlsWr.Write()
	logrus.Info("HLS writer initialized with: segments per playlist=1, fragment count=3, segment size=", segSize)

	// Initialize WebRTC
	webrtc.Init(2000, 2100, []string{"10.0.112.141"}, []pion.ICEServer{
		{
			URLs: []string{"stun:stun.l.google.com:19302"},
		},
	})
	webrtcWr = webrtc.New(100, time.Second*10)
	webrtcWr.Write()
	logrus.Info("WebRTC writer initialized")

	// Initialize Segmenter for MP4 recording
	seg = segmenter.New("./recordings/", time.Second*10, gomedia.Always, 100)
	seg.Write()
	logrus.Info("Segmenter initialized for MP4 recording")

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the HTTP server
	go func() {
		logrus.Info("Starting server on port 8080")
		GetServer().Start()
	}()

	// Initialize RTSP reader
	logrus.Info("Connecting to RTSP streams: ", rtspURLs)
	rdr := reader.NewRTSP(100)
	rdr.Read()
	for _, rtspURL := range rtspURLs {
		rdr.AddURL() <- rtspURL
	}

	// Audio transcoding setup
	aacEnc := encoder.NewAudioEncoder(100, aac.NewAacEncoder)
	aacEnc.Encode()

	alawEnc := encoder.NewAudioEncoder(100, pcmEnc.NewAlawEncoder)
	alawEnc.Encode()

	audioDecoder := decoder.NewAudioDecoder(100, map[gomedia.CodecType]func() decoder.InnerAudioDecoder{
		gomedia.PCMAlaw: pcm.NewALAWDecoder,
		gomedia.PCMUlaw: pcm.NewULAWDecoder,
		gomedia.OPUS:    opus.NewOpusDecoder,
		gomedia.AAC:     aacDec.NewAacDecoder,
	})
	audioDecoder.Decode()

	// Log recorded segments
	go func() {
		for fileInfo := range seg.Files() {
			logrus.Infof("Recorded segment: %s (size: %d bytes, duration: %v)",
				fileInfo.Name, fileInfo.Size, fileInfo.Stop.Sub(fileInfo.Start))
		}
	}()

	// Process packets
	go func() {
		logrus.Info("Starting packet processing")
		packetCount := 0
		lastLog := time.Now()

		for {
			select {
			case smpl := <-audioDecoder.Samples():
				processDecodedAudioPacket(smpl, aacEnc, alawEnc)
			case pkt := <-aacEnc.Packets():
				aPacket := pkt.Clone(false)
				aPacket.SetURL(rtspURLs[0]) // Use first URL for recording
				seg.Packets() <- aPacket
				processEncodedAudioPacket(pkt, hlsWr)
			case pkt := <-alawEnc.Packets():
				processEncodedAudioPacket(pkt, webrtcWr)
			case pkt := <-rdr.Packets():
				if audioPkt, ok := pkt.(gomedia.AudioPacket); ok {
					processInputAudioPacket(audioPkt, audioDecoder, hlsWr, webrtcWr, seg)
				} else if videoPkt, ok := pkt.(gomedia.VideoPacket); ok {
					processVideoPacket(videoPkt, hlsWr, webrtcWr, seg)
				}
				packetCount++

				// Log packet statistics periodically
				if time.Since(lastLog) > 5*time.Second {
					logrus.Infof("Processed %d packets in the last 5 seconds", packetCount)
					packetCount = 0
					lastLog = time.Now()
				}
				pkt.Close()
			}
		}
	}()

	// Wait for termination signal
	<-sigChan
	logrus.Info("Shutdown signal received")

	// Graceful shutdown
	rdr.Close()
	hlsWr.Close()
	webrtcWr.Close()
	seg.Close()
	GetServer().Close()
	logrus.Info("Server shutdown complete")
}

// HTTP Server
type Server struct {
	server    *http.Server
	router    *gin.Engine
	startOnce *sync.Once
	closeOnce *sync.Once
	deadChan  chan any
}

func (s *Server) Start() {
	err := errors.New("HTTP server has been started already")
	s.startOnce.Do(func() {
		defer close(s.deadChan)

		logger.Info(s, "Starting listening")
		if err = s.server.ListenAndServe(); err != nil {
			logger.Warning(s, err.Error())
			err = nil
		}
	})
	if err != nil {
		logger.Error(s, err.Error())
	}
}

func (s *Server) Close() {
	s.closeOnce.Do(func() {
		logger.Warning(s, "Stopping and closing")
		s.server.Close()
	})
}

func (s *Server) Dead() <-chan any {
	return s.deadChan
}

var instance *Server
var serverOnce = &sync.Once{}

func GetServer() *Server {
	serverOnce.Do(func() {
		gin.SetMode(gin.ReleaseMode)
		router := gin.New()
		router.Use(
			func(c *gin.Context) {
				c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
				c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
				c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
				c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")
				if c.Request.Method == "OPTIONS" {
					c.AbortWithStatus(200)
					return
				}
				c.Next()
			})
		router.Use(gin.Recovery())
		pprof.Register(router)

		// Static pages
		router.GET("/", GetIndexHTML)

		// HLS endpoints
		router.GET("/streams/stream.m3u8", GetMaster)
		router.GET("/streams/:uuid/:id/cubic.m3u8", GetManifest)
		router.GET("/streams/:uuid/:id/init.mp4", GetInit)
		router.GET("/streams/:uuid/:id/segment/:segment/:any", GetSegment)
		router.GET("/streams/:uuid/:id/fragment/:segment/:fragment/:any", GetFragment)

		// WebRTC endpoints
		router.POST("/sdp", HandleSDP)
		router.GET("/webrtc/codec", GetCodecInfo)

		instance = &Server{
			server: &http.Server{
				Addr:    "0.0.0.0:8080",
				Handler: router,
			},
			router:    router,
			deadChan:  make(chan any),
			startOnce: &sync.Once{},
			closeOnce: &sync.Once{},
		}
		logger.Debug(instance, "Initialized and set up")
	})
	return instance
}

func StringToInt(val string) int {
	i, err := strconv.Atoi(val)
	if err != nil {
		return -1
	}
	return i
}

// HLS Handlers
func GetMaster(c *gin.Context) {
	logger.Debug(GetServer(), "Manifest request")
	logrus.Debug("Low-Latency master playlist requested")

	index, err := hlsWr.GetMasterPlaylist()
	if err != nil {
		logrus.Errorf("Failed to get master playlist: %v", err)
		c.Status(http.StatusNotFound)
		return
	}

	logrus.Debug("Serving master playlist")
	_, err = c.Writer.Write([]byte(index))
	if err != nil {
		logrus.Errorf("Failed to write master playlist: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}
}

func GetManifest(c *gin.Context) {
	id := StringToInt(c.Param("id"))
	msn := StringToInt(c.DefaultQuery("_HLS_msn", "-1"))
	part := StringToInt(c.DefaultQuery("_HLS_part", "-1"))

	logrus.Debugf("Low-Latency manifest requested: id=%d, msn=%d, part=%d", id, msn, part)

	index, err := hlsWr.GetIndexM3u8(c, uint8(id), int64(msn), int8(part))
	if err != nil {
		logrus.Errorf("Failed to get manifest: %v", err)
		c.String(http.StatusNotFound, err.Error())
		return
	}

	logrus.Debug("Serving manifest")
	_, err = c.Writer.Write([]byte(index))
	if err != nil {
		logrus.Errorf("Failed to write manifest: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}
}

func GetInit(c *gin.Context) {
	c.Header("Content-Type", "video/mp4")
	id := StringToInt(c.Param("id"))

	logrus.Debugf("Init segment requested: id=%d", id)

	buf, err := hlsWr.GetInit(uint8(id))
	if err != nil {
		logrus.Errorf("Failed to get init segment: %v", err)
		c.Status(http.StatusNotFound)
		return
	}

	logrus.Debugf("Serving init segment: size=%d bytes", len(buf))
	_, err = c.Writer.Write(buf)
	if err != nil {
		logrus.Errorf("Failed to write init segment: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}
}

func GetSegment(c *gin.Context) {
	c.Header("Content-Type", "video/mp4")
	id := StringToInt(c.Param("id"))
	segment := StringToInt(c.Param("segment"))

	logrus.Debugf("Segment requested: id=%d, segment=%d", id, segment)

	buf, err := hlsWr.GetSegment(c, uint8(id), uint64(segment))
	if err != nil {
		logrus.Errorf("Failed to get segment: %v", err)
		c.Status(http.StatusNotFound)
		return
	}

	logrus.Debugf("Serving segment: id=%d, segment=%d, size=%d bytes", id, segment, len(buf))
	_, err = c.Writer.Write(buf)
	if err != nil {
		logrus.Errorf("Failed to write segment: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}
}

func GetFragment(c *gin.Context) {
	c.Header("Content-Type", "video/mp4")
	id := StringToInt(c.Param("id"))
	segment := StringToInt(c.Param("segment"))
	fragment := StringToInt(c.Param("fragment"))

	logrus.Debugf("Fragment requested: id=%d, segment=%d, fragment=%d", id, segment, fragment)

	buf, err := hlsWr.GetFragment(c, uint8(id), uint64(segment), uint8(fragment))
	if err != nil {
		logrus.Errorf("Failed to get fragment: %v", err)
		c.Status(http.StatusNotFound)
		return
	}

	logrus.Debugf("Serving fragment: id=%d, segment=%d, fragment=%d, size=%d bytes", id, segment, fragment, len(buf))
	_, err = c.Writer.Write(buf)
	if err != nil {
		logrus.Errorf("Failed to write fragment: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}
}

// WebRTC Handlers
func HandleSDP(c *gin.Context) {
	var req SDPRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, SDPResponse{
			SDP: "",
			Err: err,
		})
		return
	}

	webrtcWr.Peers() <- gomedia.WebRTCPeer{
		SDP:   req.SDP,
		Delay: 0,
		Err:   nil,
	}

	resp := <-webrtcWr.Peers()
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
}

func GetCodecInfo(c *gin.Context) {
	c.JSON(http.StatusOK, webrtcWr.SortedResolutions())
}

// Static pages
func GetIndexHTML(c *gin.Context) {
	logrus.Debug("Index HTML page requested")
	c.File("index.html")
}

// Audio packet processing functions following the example pattern

func processInputAudioPacket(packet gomedia.AudioPacket, audioDecoder gomedia.AudioDecoder, hlsWr gomedia.HLSStreamer, webrtcWr gomedia.WebRTCStreamer, seg gomedia.Segmenter) {
	// Only process audio from the first stream if there are multiple URLs
	if len(rtspURLs) > 1 && packet.URL() != rtspURLs[0] {
		return
	}

	// Always send to decoder
	audioDecoder.Packets() <- packet.Clone(false).(gomedia.AudioPacket)

	// Track the audio packet type
	audioPktType = packet.CodecParameters().Type()

	switch audioPktType {
	case gomedia.AAC:
		// Send AAC directly to HLS for all URLs
		for _, url := range rtspURLs {
			clonePkt := packet.Clone(false)
			clonePkt.SetURL(url)
			hlsWr.Packets() <- clonePkt
		}
		// Send to segmenter for recording
		aPacket := packet.Clone(false)
		aPacket.SetURL(rtspURLs[0]) // Use first URL for recording
		seg.Packets() <- aPacket
	case gomedia.PCMAlaw:
		// Send PCMAlaw directly to WebRTC
		webrtcWr.Packets() <- packet.Clone(false)
	}
}

func processDecodedAudioPacket(packet gomedia.AudioPacket, aacEnc gomedia.AudioEncoder, alawEnc gomedia.AudioEncoder) {
	// If audio type is NOT AAC, send to AAC encoder for HLS
	if audioPktType != gomedia.AAC {
		aacEnc.Samples() <- packet.Clone(false).(gomedia.AudioPacket)
	}
	// If audio type is NOT PCMAlaw, send to ALAW encoder for WebRTC
	if audioPktType != gomedia.PCMAlaw {
		alawEnc.Samples() <- packet.Clone(false).(gomedia.AudioPacket)
	}
	packet.Close()
}

func processEncodedAudioPacket(packet gomedia.Packet, wr gomedia.Writer) {
	// Distribute encoded packets to all URLs
	for _, url := range rtspURLs {
		pkt := packet.Clone(false)
		pkt.SetURL(url)
		wr.Packets() <- pkt
	}
	packet.Close()
}

func processVideoPacket(packet gomedia.VideoPacket, hlsWr gomedia.HLSStreamer, webrtcWr gomedia.WebRTCStreamer, seg gomedia.Segmenter) {
	// Send video to HLS
	hlsWr.Packets() <- packet.Clone(false)
	// Send video to WebRTC
	webrtcWr.Packets() <- packet.Clone(false)
	// Send video to segmenter for recording
	seg.Packets() <- packet.Clone(false)
}
