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
	examplelogger "github.com/ugparu/gomedia/examples/logger"
	"github.com/ugparu/gomedia/format/rtsp"
	"github.com/ugparu/gomedia/reader"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/writer/hls"
	"github.com/ugparu/gomedia/writer/segmenter"
	"github.com/ugparu/gomedia/writer/webrtc"
)

var rtspURLs = strings.Split(os.Getenv("RTSP_URLS"), ",")

const segSize = 4 * time.Second

// Writers
var (
	hlsWr    = hls.New(1, 3, segSize, 100, 5.)
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
	Err string `json:"err"`
}

var log logger.Logger

func main() {
	log = examplelogger.New(logrus.InfoLevel)

	// Initialize HLS writer
	hlsWr.Write()
	log.Infof(log, "HLS writer initialized with: segments per playlist=1, fragment count=3, segment size=%v", segSize)

	// Initialize WebRTC
	err := webrtc.Init(2000, 2100, []string{"10.0.112.161"}, []pion.ICEServer{
		{},
	})
	if err != nil {
		log.Errorf(log, "Failed to initialize WebRTC: %v", err)
		os.Exit(1)
	}
	webrtcWr = webrtc.New(100, time.Second*12)
	webrtcWr.Write()
	log.Infof(log, "WebRTC writer initialized")

	os.RemoveAll("./recordings/")

	// Initialize Segmenter for MP4 recording
	seg = segmenter.New("./recordings/", time.Second*10, gomedia.Always, 100)
	seg.Write()
	log.Infof(log, "Segmenter initialized for MP4 recording")

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the HTTP server
	go func() {
		log.Infof(log, "Starting server on port 8080")
		GetServer().Start()
	}()

	// Initialize RTSP reader
	log.Infof(log, "Connecting to RTSP streams: %v", rtspURLs)
	rdr := reader.NewRTSP(100, reader.WithLogger(examplelogger.New(logrus.InfoLevel)), reader.WithRTSPParams(rtsp.WithRingBuffer(1024)))
	rdr.Read()
	for _, rtspURL := range rtspURLs {
		webrtcWr.AddSource() <- rtspURL
		seg.AddSource() <- rtspURL
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
			log.Infof(log, "Recorded segment: %s (size: %d bytes, duration: %v)",
				fileInfo.Name, fileInfo.Size, fileInfo.Stop.Sub(fileInfo.Start))
		}
	}()

	// Process packets
	go func() {
		log.Infof(log, "Starting packet processing")
		packetCount := 0
		lastLog := time.Now()

		for {
			select {
			case smpl := <-audioDecoder.Samples():
				processDecodedAudioPacket(smpl, aacEnc, alawEnc)
				smpl.Release()
			case pkt := <-aacEnc.Packets():
				processEncodedAudioPacket(pkt, hlsWr)
				processEncodedAudioPacket(pkt, seg)
				pkt.Release()
			case pkt := <-alawEnc.Packets():
				processEncodedAudioPacket(pkt, webrtcWr)
				pkt.Release()
			case pkt := <-rdr.Packets():
				if audioPkt, ok := pkt.(gomedia.AudioPacket); ok {
					processInputAudioPacket(audioPkt, audioDecoder, hlsWr, webrtcWr, seg)
				} else if videoPkt, ok := pkt.(gomedia.VideoPacket); ok {
					processVideoPacket(videoPkt, hlsWr, webrtcWr, seg)
				}
				packetCount++

				// Log packet statistics periodically
				if time.Since(lastLog) > 5*time.Second {
					log.Infof(log, "Processed %d packets in the last 5 seconds", packetCount)
					packetCount = 0
					lastLog = time.Now()
				}
				pkt.Release()
			}
		}
	}()

	// Wait for termination signal
	<-sigChan
	log.Infof(log, "Shutdown signal received")

	// Graceful shutdown
	rdr.Close()
	hlsWr.Close()
	webrtcWr.Close()
	seg.Close()
	GetServer().Close()
	log.Infof(log, "Server shutdown complete")
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

		log.Infof(log, "Starting listening")
		if err = s.server.ListenAndServe(); err != nil {
			log.Warningf(log, err.Error())
			err = nil
		}
	})
	if err != nil {
		log.Errorf(log, err.Error())
	}
}

func (s *Server) Close() {
	s.closeOnce.Do(func() {
		log.Warningf(log, "Stopping and closing")
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
		router.GET("/streams/:uuid/:id/index.m3u8", GetManifest)
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
		log.Debugf(log, "Initialized and set up")
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
	log.Debugf(log, "Manifest request")
	logrus.Debug("Low-Latency master playlist requested")

	index, err := hlsWr.GetMasterPlaylist()
	if err != nil {
		log.Errorf(log, "Failed to get master playlist: %v", err)
		c.Status(http.StatusNotFound)
		return
	}

	log.Debugf(log, "Serving master playlist")
	_, err = c.Writer.Write([]byte(index))
	if err != nil {
		log.Errorf(log, "Failed to write master playlist: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}
}

func GetManifest(c *gin.Context) {
	uid := c.Param("id")
	msn := StringToInt(c.DefaultQuery("_HLS_msn", "-1"))
	part := StringToInt(c.DefaultQuery("_HLS_part", "-1"))

	log.Debugf(log, "Low-Latency manifest requested: uid=%s, msn=%d, part=%d", uid, msn, part)

	index, err := hlsWr.GetIndexM3u8(c, uid, int64(msn), int8(part))
	if err != nil {
		log.Errorf(log, "Failed to get manifest: %v", err)
		c.String(http.StatusNotFound, err.Error())
		return
	}

	log.Debugf(log, "Serving manifest")
	_, err = c.Writer.Write([]byte(index))
	if err != nil {
		log.Errorf(log, "Failed to write manifest: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}
}

func GetInit(c *gin.Context) {
	c.Header("Content-Type", "video/mp4")
	uid := c.Param("id")
	version := StringToInt(c.DefaultQuery("v", "-1"))

	log.Debugf(log, "Init segment requested: uid=%s, version=%d", uid, version)

	var buf []byte
	var err error
	if version >= 0 {
		buf, err = hlsWr.GetInitByVersion(uid, version)
	} else {
		buf, err = hlsWr.GetInit(uid)
	}
	if err != nil {
		log.Errorf(log, "Failed to get init segment: %v", err)
		c.Status(http.StatusNotFound)
		return
	}

	log.Debugf(log, "Serving init segment: size=%d bytes", len(buf))
	_, err = c.Writer.Write(buf)
	if err != nil {
		log.Errorf(log, "Failed to write init segment: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}
}

func GetSegment(c *gin.Context) {
	c.Header("Content-Type", "video/mp4")
	uid := c.Param("id")
	segment := StringToInt(c.Param("segment"))

	log.Debugf(log, "Segment requested: uid=%s, segment=%d", uid, segment)

	buf, err := hlsWr.GetSegment(c, uid, uint64(segment))
	if err != nil {
		log.Errorf(log, "Failed to get segment: %v", err)
		c.Status(http.StatusNotFound)
		return
	}

	log.Debugf(log, "Serving segment: uid=%s, segment=%d, size=%d bytes", uid, segment, len(buf))
	_, err = c.Writer.Write(buf)
	if err != nil {
		log.Errorf(log, "Failed to write segment: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}
}

func GetFragment(c *gin.Context) {
	c.Header("Content-Type", "video/mp4")
	uid := c.Param("id")
	segment := StringToInt(c.Param("segment"))
	fragment := StringToInt(c.Param("fragment"))

	log.Debugf(log, "Fragment requested: uid=%s, segment=%d, fragment=%d", uid, segment, fragment)

	buf, err := hlsWr.GetFragment(c, uid, uint64(segment), uint8(fragment))
	if err != nil {
		log.Errorf(log, "Failed to get fragment: %v", err)
		c.Status(http.StatusNotFound)
		return
	}

	log.Debugf(log, "Serving fragment: uid=%s, segment=%d, fragment=%d, size=%d bytes", uid, segment, fragment, len(buf))
	_, err = c.Writer.Write(buf)
	if err != nil {
		log.Errorf(log, "Failed to write fragment: %v", err)
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
			Err: err.Error(),
		})
		return
	}

	// By default, bind peer to the first RTSP URL
	targetURL := ""
	if len(rtspURLs) > 0 && rtspURLs[0] != "" {
		targetURL = rtspURLs[0]
	}

	peer := &gomedia.WebRTCPeer{
		SDP:       req.SDP,
		TargetURL: targetURL,
		Delay:     0,
		Err:       nil,
		Done:      make(chan struct{}),
	}

	webrtcWr.Peers() <- peer

	select {
	case <-peer.Done:
		if peer.Err != nil {
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
	case <-time.After(time.Second * 10):
		log.Errorf(log, "Timeout adding peer")
		c.JSON(http.StatusInternalServerError, SDPResponse{
			SDP: "",
			Err: "Timeout adding peer",
		})
		return
	}
}

func GetCodecInfo(c *gin.Context) {
	c.JSON(http.StatusOK, webrtcWr.SortedResolutions())
}

// Static pages
func GetIndexHTML(c *gin.Context) {
	log.Debugf(log, "Index HTML page requested")
	c.File("index.html")
}

// Audio packet processing functions following the example pattern

func processInputAudioPacket(packet gomedia.AudioPacket, audioDecoder gomedia.AudioDecoder, hlsWr gomedia.HLSStreamer, webrtcWr gomedia.WebRTCStreamer, seg gomedia.Segmenter) {
	// Only process audio from the first stream if there are multiple URLs
	if len(rtspURLs) > 1 && packet.SourceID() != rtspURLs[0] {
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
			clonePkt.SetSourceID(url)
			hlsWr.Packets() <- clonePkt
		}
		// Send to segmenter for recording
		aPacket := packet.Clone(false)
		aPacket.SetSourceID(rtspURLs[0]) // Use first URL for recording
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
}

func processEncodedAudioPacket(packet gomedia.Packet, wr gomedia.Writer) {
	// Distribute encoded packets to all URLs
	for _, url := range rtspURLs {
		pkt := packet.Clone(false)
		pkt.SetSourceID(url)
		wr.Packets() <- pkt
	}
}

func processVideoPacket(packet gomedia.VideoPacket, hlsWr gomedia.HLSStreamer, webrtcWr gomedia.WebRTCStreamer, seg gomedia.Segmenter) {
	// Send video to HLS
	hlsWr.Packets() <- packet.Clone(false)
	// Send video to WebRTC
	webrtcWr.Packets() <- packet.Clone(false)
	// Send video to segmenter for recording
	seg.Packets() <- packet.Clone(false)
}
