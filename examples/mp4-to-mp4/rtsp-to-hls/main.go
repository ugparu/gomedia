package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/reader"
	"github.com/ugparu/gomedia/writer/hls"
)

var hlsWriter gomedia.HLSStreamer

// StringToInt converts string to int, returns -1 on error
func StringToInt(val string) int {
	i, err := strconv.Atoi(val)
	if err != nil {
		return -1
	}
	return i
}

// GetMaster handles master playlist requests
func GetMaster(c *gin.Context) {
	c.Header("Content-Type", "application/vnd.apple.mpegurl")
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")

	playlist, err := hlsWriter.GetMasterPlaylist()
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"Error on getting master playlist": err.Error()})
		return
	}

	_, err = c.Writer.Write([]byte(playlist))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"Error on writing playlist's data to response": err.Error()})
		return
	}
}

// GetManifest handles index playlist requests
func GetManifest(c *gin.Context) {
	c.Header("Content-Type", "application/vnd.apple.mpegurl")
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")

	index, err := hlsWriter.GetIndexM3u8(c,
		uint8(StringToInt(c.Param("id"))),
		int64(StringToInt(c.DefaultQuery("_HLS_msn", "-1"))),
		int8(StringToInt(c.DefaultQuery("_HLS_part", "-1"))))
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"Error on getting index M3u8": err.Error()})
		return
	}

	_, err = c.Writer.Write([]byte(index))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"Error on writing index's data to response": err.Error()})
		return
	}
}

// GetInit handles initialization segment requests
func GetInit(c *gin.Context) {
	c.Header("Content-Type", "video/mp4")
	c.Header("Cache-Control", "public, max-age=31536000")

	buf, err := hlsWriter.GetInit(uint8(StringToInt(c.Param("id"))))
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"Error on getting init for current \"id\"-parameter": err.Error()})
		return
	}

	_, err = c.Writer.Write(buf)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"Error on writing camera's init data to response": err.Error()})
		return
	}
}

// GetSegment handles media segment requests
func GetSegment(c *gin.Context) {
	c.Header("Content-Type", "video/mp4")
	c.Header("Cache-Control", "public, max-age=31536000")

	buf, err := hlsWriter.GetSegment(c,
		uint8(StringToInt(c.Param("id"))),
		uint64(StringToInt(c.Param("segment"))))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"Error on getting segment": err.Error()})
		return
	}

	_, err = c.Writer.Write(buf)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"Error on writing camera's segment data to response": err.Error()})
		return
	}
}

// GetFragment handles fragment requests
func GetFragment(c *gin.Context) {
	c.Header("Content-Type", "video/mp4")
	c.Header("Cache-Control", "no-cache")

	buf, err := hlsWriter.GetFragment(c,
		uint8(StringToInt(c.Param("id"))),
		uint64(StringToInt(c.Param("segment"))),
		uint8(StringToInt(c.Param("fragment"))))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"Error on getting fragment": err.Error()})
		return
	}

	_, err = c.Writer.Write(buf)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"Error on writing camera's fragment data to response": err.Error()})
		return
	}
}

func main() {
	// Get RTSP URL from environment variable
	rtspURL := os.Getenv("RTSP_URL")
	if rtspURL == "" {
		log.Fatal("RTSP_URL environment variable is required")
	}

	// Get server port from environment variable or use default
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Initialize RTSP reader
	rdr := reader.NewRTSP(0)
	rdr.Read()
	defer rdr.Close()
	rdr.AddURL() <- rtspURL

	// Initialize HLS writer
	hlsWriter = hls.New(0, 2, 6*time.Second, 10)
	hlsWriter.Write()
	defer hlsWriter.Close()

	// Start packet forwarding goroutine
	go func() {
		for pkt := range rdr.Packets() {
			hlsWriter.Packets() <- pkt
		}
	}()

	// Setup Gin router
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// Add CORS middleware for better browser compatibility
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})

	// Serve the HTML page
	r.StaticFile("/", "./index.html")
	r.StaticFile("/index.html", "./index.html")

	// Register HLS endpoints using the extracted handler functions
	r.GET("/master.m3u8", GetMaster)
	r.GET("/:uuid/:id/cubic.m3u8", GetManifest)
	r.GET("/:uuid/:id/init.mp4", GetInit)
	r.GET("/:uuid/:id/segment/:segment/:any", GetSegment)
	r.GET("/:uuid/:id/fragment/:segment/:fragment/:any", GetFragment)

	// Health check endpoint
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":   "healthy",
			"rtsp_url": rtspURL,
		})
	})

	// Start HTTP server
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Starting HTTP server on port %s", port)
		log.Printf("Open http://localhost:%s in your browser to view the stream", port)
		log.Printf("RTSP URL: %s", rtspURL)

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Create a context with timeout for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shutdown the server gracefully
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}
