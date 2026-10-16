package docparser

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-5 }

func TestToCHWLayoutAndNormalisation(t *testing.T) {
	// 2x1 image: left pixel (255,0,0), right pixel (0,0,255).
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})
	img.SetRGBA(1, 0, color.RGBA{0, 0, 255, 255})

	p := NormParams{Mean: [3]float32{0.5, 0.5, 0.5}, Std: [3]float32{0.5, 0.5, 0.5}}
	got := ToCHW(img, p)
	// CHW: R plane [x0,x1], G plane, B plane. value = (v/255-0.5)/0.5
	want := []float32{1, -1, -1, -1, -1, 1}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !near(got[i], want[i]) {
			t.Errorf("[%d] = %v, want %v (all: %v)", i, got[i], want[i], got)
		}
	}

	// BGR puts the blue plane first.
	p.BGR = true
	bgr := ToCHW(img, p)
	wantBGR := []float32{-1, 1, -1, -1, 1, -1}
	for i := range wantBGR {
		if !near(bgr[i], wantBGR[i]) {
			t.Errorf("BGR[%d] = %v, want %v", i, bgr[i], wantBGR[i])
		}
	}
}

func TestToCHWCompositesTransparencyOverWhite(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1)) // fully transparent, premultiplied zero
	p := NormParams{Mean: [3]float32{0, 0, 0}, Std: [3]float32{1, 1, 1}}
	for i, v := range ToCHW(img, p) {
		if !near(v, 1) {
			t.Errorf("channel %d = %v, a transparent pixel should read as white (1.0)", i, v)
		}
	}
}

func TestToCHWAcceptsOffsetSubImage(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	src.SetRGBA(2, 2, color.RGBA{255, 255, 255, 255})
	sub := src.SubImage(image.Rect(2, 2, 3, 3))
	p := NormParams{Std: [3]float32{1, 1, 1}}
	for _, v := range ToCHW(sub, p) {
		if !near(v, 1) {
			t.Fatalf("sub-image with non-zero origin read wrongly: %v", v)
		}
	}
}

func TestResizeDimensions(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 50))
	if r := Resize(src, 40, 20); r.Bounds().Dx() != 40 || r.Bounds().Dy() != 20 {
		t.Errorf("got %v", r.Bounds())
	}
	if r := Resize(src, 0, -3); r.Bounds().Dx() != 1 || r.Bounds().Dy() != 1 {
		t.Errorf("degenerate target should be 1x1, got %v", r.Bounds())
	}
}

func TestCropImage(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	src.SetRGBA(5, 5, color.RGBA{9, 9, 9, 255})
	c := cropImage(src, image.Rect(5, 5, 8, 8))
	if c.Bounds() != image.Rect(0, 0, 3, 3) {
		t.Errorf("origin not reset: %v", c.Bounds())
	}
	if r, _, _, _ := c.At(0, 0).RGBA(); r>>8 != 9 {
		t.Error("pixel not copied")
	}
	// A rectangle partly or wholly outside the image is clamped, never a panic.
	if c := cropImage(src, image.Rect(8, 8, 50, 50)); c.Bounds() != image.Rect(0, 0, 2, 2) {
		t.Errorf("clamp failed: %v", c.Bounds())
	}
	if c := cropImage(src, image.Rect(20, 20, 30, 30)); c.Bounds().Empty() {
		t.Error("an empty crop should still be a valid 1x1 image")
	}
}

func TestNormalizeCoords(t *testing.T) {
	got := normalize(image.Rect(100, 200, 500, 1000), 1000, 2000)
	if got != [4]int{50, 50, 250, 250} {
		t.Errorf("got %v", got)
	}
	if got := normalize(image.Rect(-10, 0, 5000, 5000), 1000, 1000); got != [4]int{0, 0, 500, 500} {
		t.Errorf("not clamped to the grid: %v", got)
	}
	if got := normalize(image.Rect(0, 0, 1, 1), 0, 0); got != [4]int{} {
		t.Errorf("zero page size: %v", got)
	}
}
