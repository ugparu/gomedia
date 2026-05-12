package rgb

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"time"
)

const (
	byteSize    = 8
	solid       = 65535
	bytesPerPix = 3
)

// Color represents a color in RGB model.
type Color struct {
	R, G, B byte
}

// RGBA returns the alpha-premultiplied red, green, blue, and alpha values for the color.
func (rgb Color) RGBA() (uint32, uint32, uint32, uint32) {
	r := uint32(rgb.R)
	r |= r << byteSize
	g := uint32(rgb.G)
	g |= g << byteSize
	b := uint32(rgb.B)
	b |= b << byteSize
	return r, g, b, solid
}

// Model is a color.Model that can convert any Color to an RGBColor.
type Model struct{}

// Convert converts a color.Color to an RGBColor using RGBModel.
func (Model) Convert(c color.Color) color.Color {
	if _, ok := c.(Color); ok {
		return c
	}
	r, g, b, _ := c.RGBA()
	return Color{byte(r >> byteSize), byte(g >> byteSize), byte(b >> byteSize)}
}

// ReleasableImage extends image.Image with a Release method for pool-backed
// frames. Consumers must call Release exactly once when done with the frame.
type ReleasableImage interface {
	image.Image
	Release()
	// GetRGB returns the underlying *RGB for direct pixel access, avoiding
	// the per-pixel interface boxing caused by At().
	GetRGB() *RGB
}

const (
	defaultPoolSize = 4
	timeout         = time.Second
)

// FramePool is a resolution-specific fixed-size pool for RGB frames.
// Unlike sync.Pool, it is NOT cleared by the GC — frames stay allocated
// for the lifetime of the pool, guaranteeing zero allocations after warmup.
type FramePool struct {
	ch   chan *RGB
	w, h int
}

// NewFramePool creates a frame pool for the given resolution pre-filled
// with defaultPoolSize frames.
func NewFramePool(w, h int) *FramePool {
	p := &FramePool{
		ch: make(chan *RGB, defaultPoolSize),
		w:  w,
		h:  h,
	}
	for range defaultPoolSize {
		p.ch <- &RGB{Pix: make([]byte, bytesPerPix*w*h)}
	}
	return p
}

// Get returns a pooled RGB frame with Pix, Stride, and Rect pre-set.
// If the pool is exhausted, a new frame is allocated (should not happen
// after warmup).
func (p *FramePool) Get() *RGB {
	var img *RGB
	select {
	case img = <-p.ch:
	default:
		img = &RGB{Pix: make([]byte, bytesPerPix*p.w*p.h)}
	}
	img.Stride = p.w * bytesPerPix
	img.Rect = image.Rect(0, 0, p.w, p.h)
	img.pool = p
	return img
}

// Put returns a frame to the pool. Called by RGB.Release().
func (p *FramePool) Put(img *RGB) {
	img.pool = nil
	select {
	case p.ch <- img:
	default:
	}
}

// RGB is an interleaved 24-bit RGB image with no per-pixel alpha. It implements
// image.Image and can be returned to its FramePool via Release.
type RGB struct {
	Pix    []byte
	Stride int
	Rect   image.Rectangle
	pool   *FramePool // nil = heap-allocated (not pooled)
}

// Release returns the frame to its pool. No-op for heap-allocated frames.
func (rgb *RGB) Release() {
	if rgb == nil {
		return
	}
	if rgb.pool != nil {
		rgb.pool.Put(rgb)
	}
}

// GetRGB returns the underlying *RGB for direct pixel access.
func (rgb *RGB) GetRGB() *RGB { return rgb }

// NewRGB creates a new RGB image with the specified rectangle.
func NewRGB(r image.Rectangle) *RGB {
	return &RGB{
		Pix:    make([]byte, bytesPerPix*r.Dx()*r.Dy()),
		Stride: r.Dx() * bytesPerPix,
		Rect:   r,
	}
}

func (*RGB) ColorModel() color.Model {
	return Model{}
}

func (rgb *RGB) Bounds() image.Rectangle {
	return rgb.Rect
}

// PixOffset returns the byte offset of pixel (x, y) inside Pix.
func (rgb *RGB) PixOffset(x, y int) int {
	return (y-rgb.Rect.Min.Y)*rgb.Stride + (x-rgb.Rect.Min.X)*3
}

func (*RGB) Opaque() bool {
	return true
}

// RGBAt is At without the color.Color interface boxing.
func (rgb *RGB) RGBAt(x, y int) Color {
	if !(image.Point{x, y}.In(rgb.Rect)) {
		return Color{}
	}
	i := rgb.PixOffset(x, y)
	s := rgb.Pix[i : i+3 : i+3]
	return Color{s[0], s[1], s[2]}
}

func (rgb *RGB) At(x, y int) color.Color {
	if !(image.Point{x, y}.In(rgb.Rect)) {
		return Color{
			R: 0,
			G: 0,
			B: 0,
		}
	}
	i := rgb.PixOffset(x, y)
	s := rgb.Pix[i : i+3 : i+3]
	return Color{s[0], s[1], s[2]}
}

func (rgb *RGB) Set(x, y int, c color.Color) {
	if !(image.Point{x, y}.In(rgb.Rect)) {
		return
	}
	i := rgb.PixOffset(x, y)

	c1, _ := rgb.ColorModel().Convert(c).(Color)

	s := rgb.Pix[i : i+3 : i+3]
	s[0] = c1.R
	s[1] = c1.G
	s[2] = c1.B
}

// SubImage returns the slice of rgb covered by r. The returned image shares
// the underlying buffer; modifying it modifies rgb.
func (rgb *RGB) SubImage(r image.Rectangle) image.Image {
	r = r.Intersect(rgb.Rect)
	if r.Empty() {
		return &RGB{
			Pix:    []byte{},
			Stride: 0,
			Rect:   r,
		}
	}
	i := rgb.PixOffset(r.Min.X, r.Min.Y)
	return &RGB{
		Pix:    rgb.Pix[i:],
		Stride: rgb.Stride,
		Rect:   r,
	}
}

// Clone deep-copies the pixels into a fresh, heap-allocated RGB (no FramePool).
func (rgb *RGB) Clone() *RGB {
	w := rgb.Bounds().Dx()
	h := rgb.Bounds().Dy()
	newStride := w * bytesPerPix
	newSlice := make([]byte, newStride*h)
	if newStride == rgb.Stride {
		copy(newSlice, rgb.Pix)
	} else {
		// SubImage has a larger stride than its width — copy row by row.
		for y := range h {
			srcOff := y * rgb.Stride
			dstOff := y * newStride
			copy(newSlice[dstOff:dstOff+newStride], rgb.Pix[srcOff:srcOff+newStride])
		}
	}
	return &RGB{
		Pix:    newSlice,
		Stride: newStride,
		Rect:   rgb.Rect,
	}
}

// RGBJSON represents the JSON structure for the RGB image.
type rgbJSON struct {
	MinX   int    `json:"minX"`
	MinY   int    `json:"minY"`
	MaxX   int    `json:"maxX"`
	MaxY   int    `json:"maxY"`
	Stride int    `json:"stride"`
	Data   string `json:"data"`
}

// MarshalJSON encodes rgb as JSON with base64-encoded pixel data.
func (rgb *RGB) MarshalJSON() ([]byte, error) {
	if rgb == nil {
		return nil, errors.New("cannot marshal nil RGB image")
	}
	return json.Marshal(rgbJSON{
		MinX:   rgb.Rect.Min.X,
		MinY:   rgb.Rect.Min.Y,
		MaxX:   rgb.Rect.Max.X,
		MaxY:   rgb.Rect.Max.Y,
		Stride: rgb.Stride,
		Data:   base64.StdEncoding.EncodeToString(rgb.Pix),
	})
}

// UnmarshalJSON decodes JSON written by MarshalJSON into rgb in place.
func (rgb *RGB) UnmarshalJSON(data []byte) error {
	var jsonData rgbJSON
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return err
	}

	pixData, err := base64.StdEncoding.DecodeString(jsonData.Data)
	if err != nil {
		return fmt.Errorf("failed to decode base64 image data: %w", err)
	}

	rect := image.Rectangle{
		Min: image.Point{X: jsonData.MinX, Y: jsonData.MinY},
		Max: image.Point{X: jsonData.MaxX, Y: jsonData.MaxY},
	}

	expectedSize := bytesPerPix * rect.Dx() * rect.Dy()
	if expectedSize > 0 && len(pixData) != expectedSize {
		return fmt.Errorf("invalid pixel data size: got %d bytes, expected %d bytes", len(pixData), expectedSize)
	}

	rgb.Pix = pixData
	rgb.Stride = jsonData.Stride
	rgb.Rect = rect
	return nil
}
