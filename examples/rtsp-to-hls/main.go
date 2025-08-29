package main

import (
	"errors"
	"fmt"
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
	"github.com/sirupsen/logrus"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/decoder"
	aacDec "github.com/ugparu/gomedia/decoder/aac"
	"github.com/ugparu/gomedia/decoder/opus"
	"github.com/ugparu/gomedia/decoder/pcm"
	"github.com/ugparu/gomedia/encoder"
	"github.com/ugparu/gomedia/encoder/aac"
	"github.com/ugparu/gomedia/reader"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/writer/hls"
)

var rtspURLs = strings.Split(os.Getenv("RTSP_URLS"), ",")

const segSize = 6 * time.Second

// Debug HLS writer
var hlsWr = hls.New(1, 3, segSize, 100)

func main() {
	fmt.Println("Starting HLS debug server...")

	// Initialize the HLS writer
	hlsWr.Write()
	logrus.Info("HLS writer initialized with: segments per playlist=1, fragment count=3, segment size=", segSize)

	// Set log level to debug for more detailed output
	logrus.SetLevel(logrus.InfoLevel)
	logrus.Info("Log level set to DEBUG")

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the server in a goroutine
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

	aacEnc := encoder.NewAudioEncoder(100, aac.NewAacEncoder)
	aacEnc.Encode()

	audioDecoder := decoder.NewAudioDecoder(100, map[gomedia.CodecType]func() decoder.InnerAudioDecoder{
		gomedia.PCMAlaw: pcm.NewALAWDecoder,
		gomedia.PCMUlaw: pcm.NewULAWDecoder,
		gomedia.OPUS:    opus.NewOpusDecoder,
		gomedia.AAC:     aacDec.NewAacDecoder,
	})
	audioDecoder.Decode()

	logrus.Info("HLS writer initialized with stream parameters")

	// Process packets
	go func() {
		logrus.Info("Starting packet processing")
		packetCount := 0
		lastLog := time.Now()

		for {
			select {
			case smpl := <-audioDecoder.Samples():
				aacEnc.Samples() <- smpl
			case pkt := <-aacEnc.Packets():
				for _, url := range rtspURLs {
					clonePkt := pkt.Clone(true)
					clonePkt.SetURL(url)
					hlsWr.Packets() <- clonePkt
				}
			case pkt := <-rdr.Packets():
				_, ok := pkt.(gomedia.AudioPacket)
				println(pkt.StreamIndex(), pkt.URL(), ok)
				if inPkt, ok := pkt.(gomedia.AudioPacket); ok {
					if inPkt.URL() != rtspURLs[0] {
						continue
					}
					audioDecoder.Packets() <- inPkt
				} else if inPkt, ok := pkt.(gomedia.VideoPacket); ok {
					hlsWr.Packets() <- inPkt
				}
				packetCount++

				// Log packet statistics periodically
				if time.Since(lastLog) > 5*time.Second {
					logrus.Infof("Processed %d packets in the last 5 seconds", packetCount)
					packetCount = 0
					lastLog = time.Now()
				}
			}
		}
	}()

	// Wait for termination signal
	<-sigChan
	logrus.Info("Shutdown signal received")

	// Graceful shutdown
	GetServer().Close()
	logrus.Info("Server shutdown complete")
}

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

		router.GET("/streams/stream.m3u8", GetMaster)
		router.GET("/streams/:uuid/:id/cubic.m3u8", GetManifest)
		router.GET("/streams/:uuid/:id/init.mp4", GetInit)
		router.GET("/streams/:uuid/:id/segment/:segment/:any", GetSegment)
		router.GET("/streams/:uuid/:id/fragment/:segment/:fragment/:any", GetFragment)
		router.GET("/", GetIndexHTML)

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

	f, err := os.Create("init.mp4")
	if err != nil {
		logrus.Errorf("Failed to create segment file: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}
	defer f.Close()

	_, err = f.Write(buf)
	if err != nil {
		logrus.Errorf("Failed to write segment to file: %v", err)
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

// GetIndexHTML serves the main HTML page with video player
func GetIndexHTML(c *gin.Context) {
	logrus.Debug("Index HTML page requested")
	c.File("index.html")
}
