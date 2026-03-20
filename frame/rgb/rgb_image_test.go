package rgb

import (
	"encoding/json"
	"image"
	"image/color"
	"testing"
)

// ---------------------------------------------------------------------------
// Color
// ---------------------------------------------------------------------------

func TestColor_RGBA(t *testing.T) {
	tests := []struct {
		name       string
		c          Color
		wantR      uint32
		wantG      uint32
		wantB      uint32
		wantA      uint32
	}{
		{"black", Color{0, 0, 0}, 0, 0, 0, solid},
		{"white", Color{255, 255, 255}, 0xffff, 0xffff, 0xffff, solid},
		{"red", Color{255, 0, 0}, 0xffff, 0, 0, solid},
		{"green", Color{0, 255, 0}, 0, 0xffff, 0, solid},
		{"blue", Color{0, 0, 255}, 0, 0, 0xffff, solid},
		{"mid-gray", Color{128, 128, 128}, 0x8080, 0x8080, 0x8080, solid},
		{"arbitrary", Color{0x42, 0xAB, 0x13}, 0x4242, 0xABAB, 0x1313, solid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, g, b, a := tt.c.RGBA()
			if r != tt.wantR || g != tt.wantG || b != tt.wantB || a != tt.wantA {
				t.Errorf("got (%d,%d,%d,%d), want (%d,%d,%d,%d)", r, g, b, a, tt.wantR, tt.wantG, tt.wantB, tt.wantA)
			}
		})
	}
}

func TestColor_AlphaAlwaysSolid(t *testing.T) {
	_, _, _, a := Color{0, 0, 0}.RGBA()
	if a != 0xffff {
		t.Errorf("alpha = %d, want 65535", a)
	}
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

func TestModel_ConvertPassthroughRGBColor(t *testing.T) {
	m := Model{}
	c := Color{10, 20, 30}
	got := m.Convert(c)
	if got != c {
		t.Errorf("passthrough failed: got %v, want %v", got, c)
	}
}

func TestModel_ConvertFromNRGBA(t *testing.T) {
	m := Model{}
	src := color.NRGBA{R: 200, G: 100, B: 50, A: 255}
	got := m.Convert(src).(Color)
	if got.R != 200 || got.G != 100 || got.B != 50 {
		t.Errorf("got %v, want {200,100,50}", got)
	}
}

func TestModel_ConvertFromRGBA(t *testing.T) {
	m := Model{}
	// color.RGBA stores alpha-premultiplied values.
	src := color.RGBA{R: 128, G: 64, B: 32, A: 255}
	got := m.Convert(src).(Color)
	// The conversion goes through RGBA() which returns 16-bit values,
	// then shifts right by 8. For color.RGBA{128,64,32,255} RGBA() returns
	// (0x8080, 0x4040, 0x2020, 0xffff), so >>8 = (128,64,32).
	if got.R != 128 || got.G != 64 || got.B != 32 {
		t.Errorf("got %v, want {128,64,32}", got)
	}
}

func TestModel_ConvertFromTransparent(t *testing.T) {
	m := Model{}
	src := color.RGBA{R: 0, G: 0, B: 0, A: 0}
	got := m.Convert(src).(Color)
	if got.R != 0 || got.G != 0 || got.B != 0 {
		t.Errorf("transparent should convert to black, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// NewRGB
// ---------------------------------------------------------------------------

func TestNewRGB_BasicProperties(t *testing.T) {
	r := image.Rect(0, 0, 10, 20)
	img := NewRGB(r)

	if img.Rect != r {
		t.Errorf("Rect = %v, want %v", img.Rect, r)
	}
	if img.Stride != 10*bytesPerPix {
		t.Errorf("Stride = %d, want %d", img.Stride, 10*bytesPerPix)
	}
	if len(img.Pix) != 10*20*bytesPerPix {
		t.Errorf("len(Pix) = %d, want %d", len(img.Pix), 10*20*bytesPerPix)
	}
}

func TestNewRGB_ZeroSize(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 0, 0))
	if len(img.Pix) != 0 {
		t.Errorf("zero-size image should have empty Pix, got len %d", len(img.Pix))
	}
}

func TestNewRGB_NonOriginRect(t *testing.T) {
	r := image.Rect(5, 10, 15, 30)
	img := NewRGB(r)

	if img.Rect != r {
		t.Errorf("Rect = %v, want %v", img.Rect, r)
	}
	// Width=10, Height=20
	if len(img.Pix) != 10*20*bytesPerPix {
		t.Errorf("len(Pix) = %d, want %d", len(img.Pix), 10*20*bytesPerPix)
	}
}

func TestNewRGB_PixInitializedToZero(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 5, 5))
	for i, v := range img.Pix {
		if v != 0 {
			t.Fatalf("Pix[%d] = %d, want 0", i, v)
		}
	}
}

// ---------------------------------------------------------------------------
// image.Image interface compliance
// ---------------------------------------------------------------------------

func TestRGB_ImplementsImageInterface(t *testing.T) {
	var _ image.Image = (*RGB)(nil)
}

func TestRGB_ColorModel(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 1, 1))
	if _, ok := img.ColorModel().(Model); !ok {
		t.Errorf("ColorModel should return Model, got %T", img.ColorModel())
	}
}

func TestRGB_Bounds(t *testing.T) {
	r := image.Rect(3, 7, 30, 40)
	img := NewRGB(r)
	if img.Bounds() != r {
		t.Errorf("Bounds() = %v, want %v", img.Bounds(), r)
	}
}

func TestRGB_Opaque(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 10, 10))
	if !img.Opaque() {
		t.Error("Opaque() should always return true for RGB images")
	}
}

// ---------------------------------------------------------------------------
// PixOffset
// ---------------------------------------------------------------------------

func TestRGB_PixOffset_Origin(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 10, 10))
	if off := img.PixOffset(0, 0); off != 0 {
		t.Errorf("PixOffset(0,0) = %d, want 0", off)
	}
	if off := img.PixOffset(1, 0); off != 3 {
		t.Errorf("PixOffset(1,0) = %d, want 3", off)
	}
	if off := img.PixOffset(0, 1); off != 30 {
		t.Errorf("PixOffset(0,1) = %d, want 30 (stride=30)", off)
	}
}

func TestRGB_PixOffset_NonOrigin(t *testing.T) {
	img := NewRGB(image.Rect(5, 10, 15, 20))
	// PixOffset(5,10) should be 0 (top-left of the image)
	if off := img.PixOffset(5, 10); off != 0 {
		t.Errorf("PixOffset(5,10) = %d, want 0", off)
	}
	// PixOffset(6,10) should be 3
	if off := img.PixOffset(6, 10); off != 3 {
		t.Errorf("PixOffset(6,10) = %d, want 3", off)
	}
}

// ---------------------------------------------------------------------------
// At / Set
// ---------------------------------------------------------------------------

func TestRGB_SetAndAt(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 10, 10))
	c := Color{R: 100, G: 150, B: 200}
	img.Set(3, 4, c)

	got := img.At(3, 4).(Color)
	if got != c {
		t.Errorf("At(3,4) = %v, want %v", got, c)
	}
}

func TestRGB_SetAndAt_NonOriginRect(t *testing.T) {
	img := NewRGB(image.Rect(10, 20, 20, 30))
	c := Color{R: 42, G: 84, B: 126}
	img.Set(15, 25, c)

	got := img.At(15, 25).(Color)
	if got != c {
		t.Errorf("At(15,25) = %v, want %v", got, c)
	}
}

func TestRGB_At_OutOfBoundsReturnsBlack(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 10, 10))
	img.Set(0, 0, Color{255, 255, 255})

	// Out of bounds on all sides
	positions := []image.Point{
		{-1, 0}, {0, -1}, {10, 0}, {0, 10}, {100, 100},
	}
	for _, p := range positions {
		got := img.At(p.X, p.Y).(Color)
		if got.R != 0 || got.G != 0 || got.B != 0 {
			t.Errorf("At(%d,%d) = %v, want black", p.X, p.Y, got)
		}
	}
}

func TestRGB_Set_OutOfBoundsIsNoop(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 2, 2))
	// Fill with known color
	for y := range 2 {
		for x := range 2 {
			img.Set(x, y, Color{42, 42, 42})
		}
	}
	// Attempt to set out of bounds
	img.Set(-1, 0, Color{255, 0, 0})
	img.Set(2, 0, Color{0, 255, 0})

	// Verify nothing changed
	for y := range 2 {
		for x := range 2 {
			got := img.At(x, y).(Color)
			if got.R != 42 || got.G != 42 || got.B != 42 {
				t.Errorf("At(%d,%d) = %v after out-of-bounds Set", x, y, got)
			}
		}
	}
}

func TestRGB_Set_WithNonRGBColor(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 5, 5))
	// Set using standard library color — should be converted via Model
	img.Set(2, 3, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
	got := img.At(2, 3).(Color)
	if got.R != 200 || got.G != 100 || got.B != 50 {
		t.Errorf("got %v, want {200,100,50}", got)
	}
}

func TestRGB_SetAllPixels(t *testing.T) {
	w, h := 4, 3
	img := NewRGB(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, Color{byte(x), byte(y), byte(x + y)})
		}
	}
	for y := range h {
		for x := range w {
			got := img.At(x, y).(Color)
			want := Color{byte(x), byte(y), byte(x + y)}
			if got != want {
				t.Errorf("At(%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// SubImage
// ---------------------------------------------------------------------------

func TestRGB_SubImage_SharesBackingBuffer(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 10, 10))
	img.Set(5, 5, Color{255, 0, 0})

	sub := img.SubImage(image.Rect(3, 3, 8, 8)).(*RGB)
	// The pixel at (5,5) in the original should be at (5,5) in the sub-image
	got := sub.At(5, 5).(Color)
	if got.R != 255 || got.G != 0 || got.B != 0 {
		t.Errorf("sub.At(5,5) = %v, want {255,0,0}", got)
	}

	// Modifying the sub-image should reflect in the original
	sub.Set(4, 4, Color{0, 255, 0})
	got2 := img.At(4, 4).(Color)
	if got2.G != 255 {
		t.Error("SubImage should share backing buffer with parent")
	}
}

func TestRGB_SubImage_Bounds(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 20, 20))
	sub := img.SubImage(image.Rect(5, 5, 15, 15)).(*RGB)

	want := image.Rect(5, 5, 15, 15)
	if sub.Bounds() != want {
		t.Errorf("SubImage Bounds = %v, want %v", sub.Bounds(), want)
	}
}

func TestRGB_SubImage_ClipsToParentBounds(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 10, 10))
	sub := img.SubImage(image.Rect(-5, -5, 100, 100)).(*RGB)

	// Should be clipped to parent bounds
	if sub.Bounds() != img.Bounds() {
		t.Errorf("SubImage should clip to parent: got %v, want %v", sub.Bounds(), img.Bounds())
	}
}

func TestRGB_SubImage_EmptyIntersection(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 10, 10))
	sub := img.SubImage(image.Rect(20, 20, 30, 30)).(*RGB)

	if !sub.Bounds().Empty() {
		t.Error("SubImage with no intersection should have empty bounds")
	}
	if len(sub.Pix) != 0 {
		t.Error("SubImage with no intersection should have empty Pix")
	}
}

func TestRGB_SubImage_OutOfBoundsReturnsBlack(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 10, 10))
	img.Set(0, 0, Color{255, 255, 255})

	sub := img.SubImage(image.Rect(2, 2, 8, 8)).(*RGB)
	// (0,0) is out of sub-image bounds
	got := sub.At(0, 0).(Color)
	if got.R != 0 || got.G != 0 || got.B != 0 {
		t.Errorf("sub.At(0,0) outside bounds should be black, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Clone
// ---------------------------------------------------------------------------

func TestRGB_Clone_IndependentCopy(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 5, 5))
	img.Set(2, 3, Color{100, 200, 50})

	cloned := img.Clone()

	// Verify the clone has the same pixel values
	got := cloned.At(2, 3).(Color)
	if got.R != 100 || got.G != 200 || got.B != 50 {
		t.Errorf("Clone pixel = %v, want {100,200,50}", got)
	}

	// Modifying the clone should not affect the original
	cloned.Set(2, 3, Color{0, 0, 0})
	orig := img.At(2, 3).(Color)
	if orig.R != 100 || orig.G != 200 || orig.B != 50 {
		t.Error("Clone should be independent — modifying clone changed original")
	}
}

func TestRGB_Clone_PreservesMetadata(t *testing.T) {
	r := image.Rect(5, 10, 15, 30)
	img := NewRGB(r)
	cloned := img.Clone()

	if cloned.Rect != img.Rect {
		t.Errorf("Clone Rect = %v, want %v", cloned.Rect, img.Rect)
	}
	if cloned.Stride != img.Stride {
		t.Errorf("Clone Stride = %d, want %d", cloned.Stride, img.Stride)
	}
}

// ---------------------------------------------------------------------------
// JSON round-trip
// ---------------------------------------------------------------------------

func TestRGB_MarshalUnmarshalJSON_RoundTrip(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 4, 3))
	// Set some distinctive pixels
	img.Set(0, 0, Color{255, 0, 0})
	img.Set(1, 1, Color{0, 255, 0})
	img.Set(3, 2, Color{0, 0, 255})

	data, err := json.Marshal(img)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var restored RGB
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("UnmarshalJSON error: %v", err)
	}

	if restored.Rect != img.Rect {
		t.Errorf("Rect = %v, want %v", restored.Rect, img.Rect)
	}
	if restored.Stride != img.Stride {
		t.Errorf("Stride = %d, want %d", restored.Stride, img.Stride)
	}

	// Verify all pixel values match
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			orig := img.At(x, y).(Color)
			got := restored.At(x, y).(Color)
			if orig != got {
				t.Errorf("At(%d,%d): got %v, want %v", x, y, got, orig)
			}
		}
	}
}

func TestRGB_MarshalJSON_NonOriginRect(t *testing.T) {
	img := NewRGB(image.Rect(10, 20, 14, 23))
	img.Set(12, 21, Color{42, 84, 126})

	data, err := json.Marshal(img)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var restored RGB
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("UnmarshalJSON error: %v", err)
	}

	got := restored.At(12, 21).(Color)
	if got.R != 42 || got.G != 84 || got.B != 126 {
		t.Errorf("restored pixel = %v, want {42,84,126}", got)
	}
}

func TestRGB_MarshalJSON_Nil(t *testing.T) {
	var img *RGB
	_, err := img.MarshalJSON()
	if err == nil {
		t.Error("MarshalJSON on nil should return error")
	}
}

func TestRGB_UnmarshalJSON_InvalidJSON(t *testing.T) {
	var img RGB
	err := json.Unmarshal([]byte(`{invalid`), &img)
	if err == nil {
		t.Error("UnmarshalJSON with invalid JSON should return error")
	}
}

func TestRGB_UnmarshalJSON_InvalidBase64(t *testing.T) {
	var img RGB
	err := json.Unmarshal([]byte(`{"minX":0,"minY":0,"maxX":2,"maxY":2,"stride":6,"data":"!!!invalid!!!"}`), &img)
	if err == nil {
		t.Error("UnmarshalJSON with invalid base64 should return error")
	}
}

func TestRGB_UnmarshalJSON_WrongDataSize(t *testing.T) {
	var img RGB
	// 2x2 image expects 12 bytes, but we provide only 3 bytes ("AQID" = [1,2,3])
	err := json.Unmarshal([]byte(`{"minX":0,"minY":0,"maxX":2,"maxY":2,"stride":6,"data":"AQID"}`), &img)
	if err == nil {
		t.Error("UnmarshalJSON with wrong pixel data size should return error")
	}
}

func TestRGB_UnmarshalJSON_ZeroSize(t *testing.T) {
	var img RGB
	// 0x0 image, empty base64
	err := json.Unmarshal([]byte(`{"minX":0,"minY":0,"maxX":0,"maxY":0,"stride":0,"data":""}`), &img)
	if err != nil {
		t.Errorf("UnmarshalJSON zero-size should succeed, got: %v", err)
	}
	if len(img.Pix) != 0 {
		t.Errorf("zero-size Pix len = %d, want 0", len(img.Pix))
	}
}

// ---------------------------------------------------------------------------
// JSON field structure
// ---------------------------------------------------------------------------

func TestRGB_MarshalJSON_FieldNames(t *testing.T) {
	img := NewRGB(image.Rect(1, 2, 5, 7))
	data, err := json.Marshal(img)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	requiredFields := []string{"minX", "minY", "maxX", "maxY", "stride", "data"}
	for _, f := range requiredFields {
		if _, ok := parsed[f]; !ok {
			t.Errorf("missing JSON field %q", f)
		}
	}

	if int(parsed["minX"].(float64)) != 1 {
		t.Errorf("minX = %v, want 1", parsed["minX"])
	}
	if int(parsed["minY"].(float64)) != 2 {
		t.Errorf("minY = %v, want 2", parsed["minY"])
	}
	if int(parsed["maxX"].(float64)) != 5 {
		t.Errorf("maxX = %v, want 5", parsed["maxX"])
	}
	if int(parsed["maxY"].(float64)) != 7 {
		t.Errorf("maxY = %v, want 7", parsed["maxY"])
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestRGB_SinglePixel(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, Color{42, 84, 126})

	got := img.At(0, 0).(Color)
	if got.R != 42 || got.G != 84 || got.B != 126 {
		t.Errorf("single pixel = %v, want {42,84,126}", got)
	}
	if len(img.Pix) != 3 {
		t.Errorf("single pixel Pix len = %d, want 3", len(img.Pix))
	}
}

func TestRGB_LargeImage(t *testing.T) {
	// 1920x1080 HD frame
	img := NewRGB(image.Rect(0, 0, 1920, 1080))
	expectedLen := 1920 * 1080 * 3
	if len(img.Pix) != expectedLen {
		t.Errorf("HD image Pix len = %d, want %d", len(img.Pix), expectedLen)
	}

	// Set and read corners
	img.Set(0, 0, Color{1, 2, 3})
	img.Set(1919, 1079, Color{4, 5, 6})

	tl := img.At(0, 0).(Color)
	br := img.At(1919, 1079).(Color)
	if tl.R != 1 || tl.G != 2 || tl.B != 3 {
		t.Errorf("top-left = %v, want {1,2,3}", tl)
	}
	if br.R != 4 || br.G != 5 || br.B != 6 {
		t.Errorf("bottom-right = %v, want {4,5,6}", br)
	}
}

func TestRGB_PixDirectAccess(t *testing.T) {
	img := NewRGB(image.Rect(0, 0, 3, 3))
	// Write directly into Pix buffer (simulates what C decoders do)
	img.Pix[0] = 10
	img.Pix[1] = 20
	img.Pix[2] = 30

	got := img.At(0, 0).(Color)
	if got.R != 10 || got.G != 20 || got.B != 30 {
		t.Errorf("direct Pix access: got %v, want {10,20,30}", got)
	}
}

func TestRGB_Clone_SubImage_Bug(t *testing.T) {
	// Clone of a SubImage is expected to produce an independent copy that
	// correctly reads all pixels. SubImage shares the parent's Pix slice
	// and Stride. Clone copies raw bytes and preserves the original Stride,
	// but allocates only Dx()*Dy()*3 bytes. For a sub-image where
	// Stride > Dx()*3, later rows would exceed the allocated size.
	img := NewRGB(image.Rect(0, 0, 10, 10))
	img.Set(5, 5, Color{100, 200, 50})
	img.Set(6, 6, Color{10, 20, 30})

	sub := img.SubImage(image.Rect(4, 4, 8, 8)).(*RGB)

	// Verify sub-image reads correctly first
	got := sub.At(5, 5).(Color)
	if got.R != 100 {
		t.Fatalf("sub.At(5,5) = %v, want R=100", got)
	}

	// Clone the sub-image — this may panic or produce wrong results
	// because Clone allocates Dx()*Dy()*3 bytes but uses the parent's Stride
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("BUG DETECTED: Clone() of SubImage panics: %v. "+
				"Clone allocates %d bytes but Stride=%d requires %d bytes for %d rows",
				r, sub.Bounds().Dx()*sub.Bounds().Dy()*3, sub.Stride,
				sub.Stride*sub.Bounds().Dy(), sub.Bounds().Dy())
		}
	}()

	cloned := sub.Clone()
	// Try reading a pixel on a later row — if Stride > Dx()*3 this
	// will access beyond the allocated buffer
	got2 := cloned.At(6, 6).(Color)
	if got2.R != 10 || got2.G != 20 || got2.B != 30 {
		t.Errorf("BUG DETECTED: Clone of SubImage returns wrong pixel at (6,6): got %v, want {10,20,30}. "+
			"Clone preserves parent Stride=%d but allocates only %d bytes (Dx=%d*Dy=%d*3)",
			got2, sub.Stride, len(cloned.Pix), sub.Bounds().Dx(), sub.Bounds().Dy())
	}
}
