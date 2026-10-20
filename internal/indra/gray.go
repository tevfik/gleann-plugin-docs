package indra

import (
	"image"
	"image/color"
	"math"
)

// Gray is an 8-bit grayscale page.
type Gray struct {
	W, H int
	Pix  []uint8
}

// FromImage converts any image to grayscale (BT.601 weights, as OpenCV).
func FromImage(img image.Image) *Gray {
	b := img.Bounds()
	g := &Gray{W: b.Dx(), H: b.Dy(), Pix: make([]uint8, b.Dx()*b.Dy())}
	if src, ok := img.(*image.Gray); ok {
		for y := 0; y < g.H; y++ {
			copy(g.Pix[y*g.W:(y+1)*g.W], src.Pix[y*src.Stride:y*src.Stride+g.W])
		}
		return g
	}
	for y := 0; y < g.H; y++ {
		for x := 0; x < g.W; x++ {
			g.Pix[y*g.W+x] = color.GrayModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.Gray).Y
		}
	}
	return g
}

// roundEven is Python's round(): halves go to the even neighbour.
func roundEven(v float64) int { return int(math.RoundToEven(v)) }

func clampU8(v float64) uint8 {
	r := math.RoundToEven(v)
	if r < 0 {
		return 0
	}
	if r > 255 {
		return 255
	}
	return uint8(r)
}

type tap struct {
	i int
	w float32
}

// areaTaps returns, per destination index, the source samples and weights of
// an area (box) average, as OpenCV's INTER_AREA.
func areaTaps(sn, dn int) [][]tap {
	out := make([][]tap, dn)
	scale := float64(sn) / float64(dn)
	for d := 0; d < dn; d++ {
		lo, hi := float64(d)*scale, float64(d+1)*scale
		var ts []tap
		for i := int(math.Floor(lo)); i < sn && float64(i) < hi; i++ {
			w := math.Min(hi, float64(i+1)) - math.Max(lo, float64(i))
			if w > 1e-9 {
				ts = append(ts, tap{i, float32(w / scale)})
			}
		}
		out[d] = ts
	}
	return out
}

// linearTaps returns two-tap bilinear weights with half-pixel centres and
// replicated borders, as OpenCV's INTER_LINEAR.
func linearTaps(sn, dn int) [][]tap {
	out := make([][]tap, dn)
	scale := float64(sn) / float64(dn)
	for d := 0; d < dn; d++ {
		s := (float64(d)+0.5)*scale - 0.5
		i := int(math.Floor(s))
		f := float32(s - float64(i))
		i0, i1 := min(max(i, 0), sn-1), min(max(i+1, 0), sn-1)
		out[d] = []tap{{i0, 1 - f}, {i1, f}}
	}
	return out
}

// Resize scales to dw x dh with area averaging (area=true, for shrinking) or
// bilinear interpolation. The result is rounded to 8 bits like OpenCV.
func (g *Gray) Resize(dw, dh int, area bool) *Gray {
	if dw == g.W && dh == g.H {
		return g
	}
	taps := linearTaps
	if area {
		taps = areaTaps
	}
	tx, ty := taps(g.W, dw), taps(g.H, dh)
	tmp := make([]float32, dw*g.H)
	for y := 0; y < g.H; y++ {
		row := g.Pix[y*g.W : (y+1)*g.W]
		for x := 0; x < dw; x++ {
			var s float32
			for _, t := range tx[x] {
				s += float32(row[t.i]) * t.w
			}
			tmp[y*dw+x] = s
		}
	}
	out := &Gray{W: dw, H: dh, Pix: make([]uint8, dw*dh)}
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var s float32
			for _, t := range ty[y] {
				s += tmp[t.i*dw+x] * t.w
			}
			out.Pix[y*dw+x] = clampU8(float64(s))
		}
	}
	return out
}
