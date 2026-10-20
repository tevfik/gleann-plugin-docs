package indra

import (
	"fmt"
	"math"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	recBatch     = 64
	recMaxRatio  = 1.3
	recMinWidth  = 64
	recMaxArea   = 64 * 640
	minConf      = 0.5
	recWidthStep = widthMultiple
)

// Tok is one recognised character: its class id and the page x of its CTC peak.
type Tok struct {
	ID int64
	X  float64
}

// Line is one recognised text line.
type Line struct {
	Box  Box
	Text string
	Conf float64
	Toks []Tok
}

// Recognize reads the lines with the Latin recogniser. A line under minConf
// keeps its box and gets no text (empty fields, rules, checkboxes).
func (m *Model) Recognize(g *Gray, boxes []Box) ([]Line, error) {
	lines := make([]Line, len(boxes))
	crops := make([][]float32, len(boxes))
	widths := make([]int, len(boxes))
	single := make([]int, len(boxes)) // width of the one-row crop: batching and x mapping use it
	var height int
	for i, b := range boxes {
		lines[i].Box = b
		crops[i], widths[i], height = cropLine(g, b, m.window, isDarkLine(g, b))
		_, _, _, _, single[i], _ = cropGeometry(b, 1)
	}
	for _, idx := range widthBatches(single, recBatch, recMaxRatio, recMinWidth, recMaxArea) {
		wm := 0
		for _, j := range idx {
			wm = max(wm, widths[j])
		}
		wm = (wm + recWidthStep - 1) / recWidthStep * recWidthStep
		x := make([]float32, len(idx)*height*wm)
		ws := make([]int64, len(idx))
		for k, j := range idx {
			for y := 0; y < height; y++ {
				copy(x[(k*height+y)*wm:], crops[j][y*widths[j]:(y+1)*widths[j]])
			}
			ws[k] = int64(widths[j])
		}
		xt, err := ort.NewTensor(ort.NewShape(int64(len(idx)), 1, int64(height), int64(wm)), x)
		if err != nil {
			return nil, err
		}
		wt, err := ort.NewTensor(ort.NewShape(int64(len(idx))), ws)
		if err != nil {
			xt.Destroy()
			return nil, err
		}
		outs := []ort.Value{nil, nil, nil}
		err = m.rec.Run([]ort.Value{xt, wt}, outs)
		xt.Destroy()
		wt.Destroy()
		if err != nil {
			return nil, fmt.Errorf("indra rec: %w", err)
		}
		ids, ok1 := outs[0].(*ort.Tensor[int64])
		conf, ok2 := outs[1].(*ort.Tensor[float32])
		lens, ok3 := outs[2].(*ort.Tensor[int64])
		if !ok1 || !ok2 || !ok3 {
			for _, o := range outs {
				if o != nil {
					o.Destroy()
				}
			}
			return nil, fmt.Errorf("indra rec: unexpected output types")
		}
		steps := int(ids.GetShape()[1])
		stride := float64(wm) / float64(max(steps, 1))
		for k, j := range idx {
			n := min(int(lens.GetData()[k]), steps)
			seq := ids.GetData()[k*steps : k*steps+n]
			pm := conf.GetData()[k*steps : k*steps+n]
			var kept []int64
			var steps_ []int
			var sum float64
			for t, id := range seq {
				if id != 0 && (t == 0 || id != seq[t-1]) {
					kept = append(kept, id)
					steps_ = append(steps_, t)
					sum += float64(pm[t])
				}
			}
			c := 0.0
			if len(kept) > 0 {
				c = sum / float64(len(kept))
			}
			lines[j].Conf = c
			if c >= minConf {
				lines[j].Text = m.decode(kept)
				// Page x of each peak: the crop starts at floor(x0 - padX*h) and was
				// resized from its integer width to widths[j].
				b := boxes[j]
				h := math.Max(1, b.Y1-b.Y0)
				ix0 := math.Floor(b.X0 - padX*h)
				f := (math.Ceil(b.X1+padX*h) - ix0) / float64(single[j])
				for i, id := range kept {
					lines[j].Toks = append(lines[j].Toks, Tok{id, ix0 + (float64(steps_[i])+0.5)*stride*f})
				}
			}
		}
		for _, o := range outs {
			o.Destroy()
		}
	}
	return lines, nil
}
