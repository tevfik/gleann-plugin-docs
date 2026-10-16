package docparser

import (
	"image"
	"testing"
)

// emptyLevels returns zeroed per-stride score and distribution tensors.
func emptyLevels() (scores, regs [4][]float32) {
	for lvl, stride := range layoutStrides {
		n := ((layoutInH + stride - 1) / stride) * ((layoutInW + stride - 1) / stride)
		scores[lvl] = make([]float32, n*layoutClsN)
		regs[lvl] = make([]float32, n*4*layoutBins)
	}
	return
}

// oneHot makes the softmax over a side's bins peak at bin k.
func oneHot(reg []float32, side, k int) {
	for b := 0; b < layoutBins; b++ {
		reg[side*layoutBins+b] = -20
	}
	reg[side*layoutBins+k] = 20
}

func TestDecodePicoDetBox(t *testing.T) {
	scores, regs := emptyLevels()
	// Level 0 (stride 8, 76 columns): cell (row 10, col 20), class "table" (3).
	const row, col = 10, 20
	idx := row*76 + col
	scores[0][idx*layoutClsN+3] = 0.9
	for side, k := range []int{2, 3, 4, 5} { // left, top, right, bottom distances in strides
		oneHot(regs[0][idx*4*layoutBins:], side, k)
	}

	got := decodePicoDet(scores, regs, 0.4)
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1", len(got))
	}
	c := got[0]
	cx, cy := float32(col)*8+4, float32(row)*8+4
	want := cand{x1: cx - 2*8, y1: cy - 3*8, x2: cx + 4*8, y2: cy + 5*8}
	for name, pair := range map[string][2]float32{"x1": {c.x1, want.x1}, "y1": {c.y1, want.y1}, "x2": {c.x2, want.x2}, "y2": {c.y2, want.y2}} {
		if d := pair[0] - pair[1]; d > 0.01 || d < -0.01 {
			t.Errorf("%s = %v, want %v", name, pair[0], pair[1])
		}
	}
	if c.cls != 3 || c.score != 0.9 {
		t.Errorf("class/score = %d/%v", c.cls, c.score)
	}
}

func TestDecodePicoDetThresholdAndLevels(t *testing.T) {
	scores, regs := emptyLevels()
	scores[0][0*layoutClsN+0] = 0.3 // below threshold
	scores[3][5*layoutClsN+4] = 0.8 // coarsest level, class "figure"
	for side := 0; side < 4; side++ {
		oneHot(regs[3][5*4*layoutBins:], side, 1)
	}
	got := decodePicoDet(scores, regs, 0.4)
	if len(got) != 1 || got[0].cls != 4 {
		t.Fatalf("got %+v", got)
	}
	// Stride 64: each side is 1*64 from the cell centre, so the box is 128 wide.
	if w := got[0].x2 - got[0].x1; w < 127.9 || w > 128.1 {
		t.Errorf("width = %v, want 128", w)
	}
}

func TestDecodePicoDetShortRegressionIsSkipped(t *testing.T) {
	scores, regs := emptyLevels()
	regs[0] = regs[0][:10] // malformed tensor
	scores[0][0] = 0.99
	if got := decodePicoDet(scores, regs, 0.4); len(got) != 0 {
		t.Errorf("got %d candidates from a malformed level", len(got))
	}
}

func TestSoftmaxExpect(t *testing.T) {
	flat := make([]float32, layoutBins)
	if got := softmaxExpect(flat); got < 3.49 || got > 3.51 {
		t.Errorf("uniform over 8 bins should have mean 3.5, got %v", got)
	}
	peak := make([]float32, layoutBins)
	peak[6] = 50
	if got := softmaxExpect(peak); got < 5.99 || got > 6.01 {
		t.Errorf("peak at 6: got %v", got)
	}
}

func TestNMS(t *testing.T) {
	in := []cand{
		{x1: 0, y1: 0, x2: 100, y2: 100, score: 0.6, cls: 0},
		{x1: 5, y1: 5, x2: 105, y2: 105, score: 0.9, cls: 0}, // overlaps the first, better
		{x1: 5, y1: 5, x2: 105, y2: 105, score: 0.7, cls: 1}, // same area, other class: kept
		{x1: 300, y1: 300, x2: 400, y2: 400, score: 0.5, cls: 0},
	}
	got := nms(in, 0.5)
	if len(got) != 3 {
		t.Fatalf("got %d boxes, want 3: %+v", len(got), got)
	}
	if got[0].score != 0.9 {
		t.Errorf("best box should come first, got %v", got[0].score)
	}
	for _, g := range got {
		if g.score == 0.6 {
			t.Error("the weaker overlapping box should be suppressed")
		}
	}
}

func TestIoU(t *testing.T) {
	a := cand{x1: 0, y1: 0, x2: 10, y2: 10}
	if got := iou(a, a); got < 0.999 {
		t.Errorf("identical boxes: %v", got)
	}
	if got := iou(a, cand{x1: 20, y1: 20, x2: 30, y2: 30}); got != 0 {
		t.Errorf("disjoint boxes: %v", got)
	}
	if got := iou(a, cand{x1: 5, y1: 0, x2: 15, y2: 10}); got < 0.33 || got > 0.34 {
		t.Errorf("half overlap should be 1/3, got %v", got)
	}
	if got := iou(cand{}, cand{}); got != 0 {
		t.Errorf("empty boxes: %v", got)
	}
}

func TestLayoutClassesMatchModel(t *testing.T) {
	// The PubLayNet model's metadata lists: text, title, list, table, figure.
	want := []ElementType{TypeText, TypeTitle, TypeList, TypeTable, TypeFigure}
	for i, w := range want {
		if layoutClasses[i] != w {
			t.Errorf("class %d = %s, want %s", i, layoutClasses[i], w)
		}
	}
	_ = image.Rect // keep the import honest if the list above shrinks
}
