package rgb

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"sync"
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

// FramePool is a resolution-specific pool for RGB frames. Create one per
// decoder instance at Init() time.
type FramePool struct {
	pool sync.Pool
	size int // expected Pix length (bytesPerPix * w * h)
	w, h int
}

// NewFramePool creates a frame pool for the given resolution.
func NewFramePool(w, h int) *FramePool {
	size := bytesPerPix * w * h
	return &FramePool{
		pool: sync.Pool{New: func() any {
			return &RGB{Pix: make([]byte, size)}
		}},
		size: size,
		w:    w,
		h:    h,
	}
}

// Get returns a pooled RGB frame with Pix, Stride, and Rect pre-set.
func (p *FramePool) Get() *RGB {
	img := p.pool.Get().(*RGB)
	img.Stride = p.w * bytesPerPix
	img.Rect = image.Rect(0, 0, p.w, p.h)
	img.pool = p
	return img
}

// Put returns a frame to the pool. Called by RGB.Release().
func (p *FramePool) Put(img *RGB) {
	img.pool = nil
	p.pool.Put(img)
}

// RGB represents an RGB image.
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

// ColorModel returns the RGBModel for the RGB image.
func (*RGB) ColorModel() color.Model {
	return Model{}
}

// Bounds returns the rectangle of the RGB image.
func (rgb *RGB) Bounds() image.Rectangle {
	return rgb.Rect
}

// PixOffset returns the index into the Pix slice for the given pixel coordinates (x, y).
func (rgb *RGB) PixOffset(x, y int) int {
	return (y-rgb.Rect.Min.Y)*rgb.Stride + (x-rgb.Rect.Min.X)*3
}

// Opaque reports whether the RGB image is opaque.
func (*RGB) Opaque() bool {
	return true
}

// RGBAt returns the RGB color at (x, y) without interface boxing.
func (rgb *RGB) RGBAt(x, y int) Color {
	if !(image.Point{x, y}.In(rgb.Rect)) {
		return Color{}
	}
	i := rgb.PixOffset(x, y)
	s := rgb.Pix[i : i+3 : i+3]
	return Color{s[0], s[1], s[2]}
}

// At returns the color at the specified pixel coordinates (x, y).
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

// Set sets the color at the specified pixel coordinates (x, y).
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

// SubImage returns a new image representing the portion of the RGB image specified by the rectangle r.
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

// Clone returns a new RGB image that is a copy of the original RGB image.
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

// MarshalJSON encodes an RGB image as JSON with base64-encoded pixel data.
func (rgb *RGB) MarshalJSON() ([]byte, error) {
	if rgb == nil {
		return nil, errors.New("cannot marshal nil RGB image")
	}

	// Encode the pixel data using base64
	encodedData := base64.StdEncoding.EncodeToString(rgb.Pix)

	// Create JSON representation
	data := rgbJSON{
		MinX:   rgb.Rect.Min.X,
		MinY:   rgb.Rect.Min.Y,
		MaxX:   rgb.Rect.Max.X,
		MaxY:   rgb.Rect.Max.Y,
		Stride: rgb.Stride,
		Data:   encodedData,
	}

	return json.Marshal(data)
}

// UnmarshalJSON decodes JSON data into an RGB image with base64-encoded pixel data.
func (rgb *RGB) UnmarshalJSON(data []byte) error {
	var jsonData rgbJSON
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return err
	}

	// Decode the base64-encoded pixel data
	pixData, err := base64.StdEncoding.DecodeString(jsonData.Data)
	if err != nil {
		return fmt.Errorf("failed to decode base64 image data: %w", err)
	}

	// Create rectangle
	rect := image.Rectangle{
		Min: image.Point{X: jsonData.MinX, Y: jsonData.MinY},
		Max: image.Point{X: jsonData.MaxX, Y: jsonData.MaxY},
	}

	// Validate dimensions
	expectedSize := bytesPerPix * rect.Dx() * rect.Dy()
	if expectedSize > 0 && len(pixData) != expectedSize {
		return fmt.Errorf("invalid pixel data size: got %d bytes, expected %d bytes", len(pixData), expectedSize)
	}

	// Set the RGB image fields
	rgb.Pix = pixData
	rgb.Stride = jsonData.Stride
	rgb.Rect = rect

	return nil
}
