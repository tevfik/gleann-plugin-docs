package indra

import (
	"fmt"
	"math"
	"slices"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	coreXShrink   = 0.15
	outStride     = 2
	inputMultiple = 32
	detMinArea    = 2

	adaptiveMinH    = 9.0
	adaptiveTargetH = 12.0
	adaptiveMaxSide = 2048
)

// Box is a line box in page pixels.
type Box struct{ X0, Y0, X1, Y1, Score float64 }

func (b Box) H() float64 { return b.Y1 - b.Y0 }

// pageToInput resizes the page so its long side is longSide, pads to a multiple
// of 32 and returns the ink map (0 = white) with its size and the scale.
func pageToInput(g *Gray, longSide int) ([]float32, int, int, float64) {
	scale := float64(longSide) / float64(max(g.H, g.W))
	nh, nw := max(1, roundEven(float64(g.H)*scale)), max(1, roundEven(float64(g.W)*scale))
	r := g
	if nh != g.H || nw != g.W {
		r = g.Resize(nw, nh, scale < 1)
	}
	ph := (nh + inputMultiple - 1) / inputMultiple * inputMultiple
	pw := (nw + inputMultiple - 1) / inputMultiple * inputMultiple
	out := make([]float32, ph*pw)
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			out[y*pw+x] = 1 - float32(r.Pix[y*nw+x])/255
		}
	}
	return out, ph, pw, scale
}

func (m *Model) detectAt(g *Gray, longSide int) ([]Box, float64, error) {
	x, h, w, s := pageToInput(g, longSide)
	in, err := ort.NewTensor(ort.NewShape(1, 1, int64(h), int64(w)), x)
	if err != nil {
		return nil, 0, err
	}
	defer in.Destroy()
	outs := []ort.Value{nil}
	if err := m.det.Run([]ort.Value{in}, outs); err != nil {
		return nil, 0, fmt.Errorf("indra det: %w", err)
	}
	defer outs[0].Destroy()
	t, ok := outs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, 0, fmt.Errorf("indra det: unexpected output type")
	}
	sh := t.GetShape()
	if len(sh) != 4 || sh[1] < 3 {
		return nil, 0, fmt.Errorf("indra det: output shape %v", sh)
	}
	return decodeDet(t.GetData(), int(sh[1]), int(sh[2]), int(sh[3])), s, nil
}

// Detect returns the text-line boxes of a page in page pixels.
func (m *Model) Detect(g *Gray) ([]Box, error) {
	boxes, s, err := m.detectAt(g, m.longSide)
	if err != nil {
		return nil, err
	}
	// Small text: detect again at a larger scale.
	if len(boxes) >= 8 {
		hs := make([]float64, len(boxes))
		for i, b := range boxes {
			hs[i] = b.H()
		}
		slices.Sort(hs)
		med := median(hs)
		if med < adaptiveMinH {
			ls := int(math.Min(adaptiveMaxSide, float64(m.longSide)*adaptiveTargetH/math.Max(med, 1))) / 32 * 32
			if ls > m.longSide {
				if boxes, s, err = m.detectAt(g, ls); err != nil {
					return nil, err
				}
			}
		}
	}
	for i := range boxes {
		b := &boxes[i]
		b.X0 = math.Min(math.Max(b.X0/s, 0), float64(g.W))
		b.X1 = math.Min(math.Max(b.X1/s, 0), float64(g.W))
		b.Y0 = math.Min(math.Max(b.Y0/s, 0), float64(g.H))
		b.Y1 = math.Min(math.Max(b.Y1/s, 0), float64(g.H))
	}
	return boxes, nil
}

// median of an already sorted slice (numpy semantics).
func median(s []float64) float64 {
	n := len(s)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// decodeDet turns the [C, h, w] output (core logit, distance to top, distance
// to bottom) into boxes in input pixels: 4-connected components of the core
// map, each box from the mean top and bottom of its pixels.
func decodeDet(out []float32, c, h, w int) []Box {
	plane := h * w
	core, top, bot := out[:plane], out[plane:2*plane], out[2*plane:3*plane]
	label := make([]int32, plane)
	var boxes []Box
	stack := make([]int, 0, 1024)
	for start := 0; start < plane; start++ {
		if core[start] <= 0 || label[start] != 0 {
			continue
		}
		id := int32(len(boxes) + 1)
		label[start] = id
		stack = append(stack[:0], start)
		minX, maxX, area := w, -1, 0
		var sTop, sBot, sScore float64
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			y, x := p/w, p%w
			area++
			minX, maxX = min(minX, x), max(maxX, x)
			ys := (float64(y) + 0.5) * outStride
			sTop += ys - float64(top[p])
			sBot += ys + float64(bot[p])
			sScore += 1 / (1 + math.Exp(-float64(core[p])))
			for _, q := range [4]int{p - w, p + w, p - 1, p + 1} {
				if q < 0 || q >= plane || label[q] != 0 || core[q] <= 0 {
					continue
				}
				if (q == p-1 && x == 0) || (q == p+1 && x == w-1) {
					continue
				}
				label[q] = id
				stack = append(stack, q)
			}
		}
		n := float64(area)
		if area < detMinArea {
			boxes = append(boxes, Box{Score: -1}) // keep numbering, dropped below
			continue
		}
		y0, y1 := sTop/n, sBot/n
		lh := math.Max(1, y1-y0)
		ex := math.Min(coreXShrink*lh, 0.5*lh)
		boxes = append(boxes, Box{
			X0: float64(minX*outStride) - ex, Y0: y0,
			X1: float64((maxX+1)*outStride) + ex, Y1: y1, Score: sScore / n,
		})
	}
	return slices.DeleteFunc(boxes, func(b Box) bool { return b.Score < 0 })
}
