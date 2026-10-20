package indra

import (
	"math"
	"sort"
)

const (
	lineHeight    = 32
	padY          = 0.2
	padX          = 0.3
	widthMultiple = 8
	maxCropWidth  = 4096
)

// percentiles of a uint8 sample at the 2nd, 50th and 98th percentile, with
// numpy's linear interpolation, from the histogram.
func percentiles(b []uint8) (lo, med, hi float64) {
	var hist [256]int
	for _, v := range b {
		hist[v]++
	}
	n := len(b)
	rank := func(i int) float64 { // value of the i-th smallest sample
		c := 0
		for v := 0; v < 256; v++ {
			c += hist[v]
			if c > i {
				return float64(v)
			}
		}
		return 255
	}
	at := func(q float64) float64 {
		x := q * float64(n-1)
		i := int(math.Floor(x))
		g := x - float64(i)
		a, b := rank(i), rank(min(i+1, n-1))
		d := b - a
		if g >= 0.5 {
			return b - d*(1-g)
		}
		return a + d*g
	}
	return at(0.02), at(0.5), at(0.98)
}

// isDarkLine reports light text on a dark ground (the recognisers read dark
// text on light paper, so such a line is read inverted).
func isDarkLine(g *Gray, b Box) bool {
	x0, y0 := max(0, int(b.X0)), max(0, int(b.Y0))
	x1, y1 := min(g.W, int(b.X1)+1), min(g.H, int(b.Y1)+1)
	if x1 <= x0 || y1 <= y0 {
		return false
	}
	buf := make([]uint8, 0, (x1-x0)*(y1-y0))
	for y := y0; y < y1; y++ {
		buf = append(buf, g.Pix[y*g.W+x0:y*g.W+x1]...)
	}
	lo, med, hi := percentiles(buf)
	return hi-med > 2*(med-lo)+8
}

// cropGeometry returns the integer page window of a line crop and the width of
// its resized ink map.
func cropGeometry(b Box, window int) (ix0, iy0, cw, ch, width, height int) {
	height = lineHeight * window
	py := (float64(window)*(1+2*padY) - 1) / 2
	h := math.Max(1, b.Y1-b.Y0)
	ix0, iy0 = int(math.Floor(b.X0-padX*h)), int(math.Floor(b.Y0-py*h))
	ix1, iy1 := int(math.Ceil(b.X1+padX*h)), int(math.Ceil(b.Y1+py*h))
	cw, ch = max(1, ix1-ix0), max(1, iy1-iy0)
	width = max(widthMultiple, min(maxCropWidth, roundEven(float64(cw)*float64(height)/float64(ch))))
	return
}

// cropLine cuts one line out of the page and returns its ink map (0 = paper)
// of window*lineHeight rows. window > 1 keeps the text scale and adds page
// above and below the line.
func cropLine(g *Gray, b Box, window int, dark bool) (ink []float32, width, height int) {
	height = lineHeight * window
	py := (float64(window)*(1+2*padY) - 1) / 2
	h := math.Max(1, b.Y1-b.Y0)
	X0, X1 := b.X0-padX*h, b.X1+padX*h
	Y0, Y1 := b.Y0-py*h, b.Y1+py*h
	ix0, iy0 := int(math.Floor(X0)), int(math.Floor(Y0))
	ix1, iy1 := int(math.Ceil(X1)), int(math.Ceil(Y1))
	cw, ch := max(1, ix1-ix0), max(1, iy1-iy0)
	crop := &Gray{W: cw, H: ch, Pix: make([]uint8, cw*ch)}
	if !dark {
		for i := range crop.Pix {
			crop.Pix[i] = 255
		}
	}
	sx0, sy0, sx1, sy1 := max(0, ix0), max(0, iy0), min(g.W, ix1), min(g.H, iy1)
	if sx1 > sx0 && sy1 > sy0 {
		for y := sy0; y < sy1; y++ {
			copy(crop.Pix[(y-iy0)*cw+(sx0-ix0):(y-iy0)*cw+(sx1-ix0)], g.Pix[y*g.W+sx0:y*g.W+sx1])
		}
	}
	width = max(widthMultiple, min(maxCropWidth, roundEven(float64(cw)*float64(height)/float64(ch))))
	r := crop.Resize(width, height, ch > height)
	ink = make([]float32, len(r.Pix))
	for i, v := range r.Pix {
		f := float32(v) / 255
		if !dark {
			f = 1 - f
		}
		ink[i] = f
	}
	return ink, width, height
}

// widthBatches groups line indices into batches of similar width, narrowest
// first, so little compute goes to padding.
func widthBatches(widths []int, maxBatch int, maxRatio float64, minWidth, maxArea int) [][]int {
	order := make([]int, len(widths))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return widths[order[a]] < widths[order[b]] })
	var batches [][]int
	for _, i := range order {
		if n := len(batches); n > 0 {
			cur := batches[n-1]
			wi := max(widths[i], minWidth)
			if len(cur) < maxBatch && float64(wi) <= maxRatio*float64(max(widths[cur[0]], minWidth)) && (len(cur)+1)*wi <= maxArea {
				batches[n-1] = append(cur, i)
				continue
			}
		}
		batches = append(batches, []int{i})
	}
	return batches
}
