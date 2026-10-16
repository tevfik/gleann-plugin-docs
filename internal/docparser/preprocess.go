package docparser

import (
	"image"
	"image/draw"

	xdraw "golang.org/x/image/draw"
)

// NormParams describes how pixels become model inputs:
// out = (pixel/255*Scale - Mean) / Std, per channel, in the order given by BGR.
type NormParams struct {
	Mean [3]float32
	Std  [3]float32
	// BGR emits the blue channel first. PaddleOCR/RapidOCR models were trained on
	// OpenCV (BGR) images, the layout model on RGB.
	BGR bool
}

// toRGBA returns img as a tightly packed *image.RGBA with its origin at (0,0),
// without copying when img already is one.
func toRGBA(img image.Image) *image.RGBA {
	if m, ok := img.(*image.RGBA); ok && m.Rect.Min == (image.Point{}) && m.Stride == 4*m.Rect.Dx() {
		return m
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

// Resize scales img to w x h (bilinear). A degenerate target returns a 1x1 image.
func Resize(img image.Image, w, h int) *image.RGBA {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	src := toRGBA(img)
	if src.Rect.Dx() == w && src.Rect.Dy() == h {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return dst
}

// ToCHW converts img into a normalised float32 tensor in CHW layout
// (len = 3*H*W). Transparent pixels are composited over white first, so a
// page rendered with an alpha channel does not turn black.
func ToCHW(img image.Image, p NormParams) []float32 {
	src := toRGBA(img)
	w, h := src.Rect.Dx(), src.Rect.Dy()
	plane := w * h
	out := make([]float32, 3*plane)

	order := [3]int{0, 1, 2}
	if p.BGR {
		order = [3]int{2, 1, 0}
	}
	var scale [3]float32
	var bias [3]float32
	for c := 0; c < 3; c++ {
		scale[c] = 1 / (255 * p.Std[c])
		bias[c] = -p.Mean[c] / p.Std[c]
	}

	pix := src.Pix
	for y := 0; y < h; y++ {
		row := pix[y*src.Stride : y*src.Stride+4*w]
		for x := 0; x < w; x++ {
			i := 4 * x
			r, g, b, a := float32(row[i]), float32(row[i+1]), float32(row[i+2]), float32(row[i+3])
			if a < 255 {
				// image.RGBA is alpha-premultiplied: over white = c + (255-a).
				r, g, b = r+255-a, g+255-a, b+255-a
			}
			rgb := [3]float32{r, g, b}
			o := y*w + x
			for c := 0; c < 3; c++ {
				out[c*plane+o] = rgb[order[c]]*scale[c] + bias[c]
			}
		}
	}
	return out
}

// ResizeToCHW resizes img to w x h and converts it to a CHW tensor.
func ResizeToCHW(img image.Image, w, h int, p NormParams) []float32 {
	return ToCHW(Resize(img, w, h), p)
}

// clampRect clips r to bounds.
func clampRect(r, bounds image.Rectangle) image.Rectangle {
	return r.Intersect(bounds)
}

// cropImage returns the part of img inside rect as an image with its origin at
// (0,0). Unlike SubImage it also works for image types without that method.
func cropImage(img image.Image, rect image.Rectangle) image.Image {
	rect = clampRect(rect, img.Bounds())
	if rect.Empty() {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(dst, dst.Bounds(), img, rect.Min, draw.Src)
	return dst
}
