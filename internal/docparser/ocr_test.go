package docparser

import (
	"image"
	"testing"
)

// paint fills a rectangle of a w-wide probability map.
func paint(m []float32, w int, r image.Rectangle, v float32) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			m[y*w+x] = v
		}
	}
}

func TestDBBoxes(t *testing.T) {
	const w, h = 100, 80
	m := make([]float32, w*h)
	a := image.Rect(10, 20, 60, 30)
	b := image.Rect(10, 50, 60, 60)
	paint(m, w, a, 0.9)                           // text line
	paint(m, w, b, 0.9)                           // a second one
	paint(m, w, image.Rect(70, 10, 90, 20), 0.35) // above the binarisation threshold, below the box score
	paint(m, w, image.Rect(70, 40, 95, 41), 0.95) // one pixel thin: noise

	got := dbBoxes(m, w, h, OCROptions{}.withDefaults())
	if len(got) != 2 {
		t.Fatalf("got %d boxes, want 2: %v", len(got), got)
	}
	for _, r := range []image.Rectangle{a, b} {
		found := false
		for _, g := range got {
			if r.In(g) {
				found = true
				// "Unclip" must grow the kernel, but not absurdly.
				if g.Dx() > r.Dx()+30 || g.Dy() > r.Dy()+30 {
					t.Errorf("box %v grew too much from %v", g, r)
				}
			}
		}
		if !found {
			t.Errorf("no detected box contains %v: %v", r, got)
		}
	}
}

func TestDBBoxesMergesTouchingPixelsDiagonally(t *testing.T) {
	const w, h = 40, 40
	m := make([]float32, w*h)
	// A diagonal staircase of 4x4 squares is one region under 8-connectivity.
	// The box-score threshold is lowered because the staircase fills only a
	// quarter of its bounding box.
	for i := 0; i < 4; i++ {
		paint(m, w, image.Rect(5+4*i, 5+4*i, 9+4*i, 9+4*i), 0.95)
	}
	if got := dbBoxes(m, w, h, OCROptions{BoxThresh: 0.1}.withDefaults()); len(got) != 1 {
		t.Errorf("got %d regions, want 1", len(got))
	}
	// With the default threshold the sparse region is rejected as noise.
	if got := dbBoxes(m, w, h, OCROptions{}.withDefaults()); len(got) != 0 {
		t.Errorf("sparse region should be dropped, got %v", got)
	}
}

func TestDBBoxesShortInput(t *testing.T) {
	if got := dbBoxes(make([]float32, 3), 10, 10, OCROptions{}.withDefaults()); got != nil {
		t.Errorf("a truncated map must give no boxes, got %v", got)
	}
}

func TestCTCGreedy(t *testing.T) {
	dict := []string{"", "a", "b", " "}
	step := func(class int) []float32 {
		row := make([]float32, len(dict))
		row[class] = 0.9
		return row
	}
	var probs []float32
	for _, c := range []int{1, 1, 0, 1, 2, 2, 0, 3, 2} { // a a _ a b b _ ' ' b
		probs = append(probs, step(c)...)
	}
	text, conf := ctcGreedy(probs, 9, len(dict), dict)
	// Repeats collapse, a blank separates a genuine double letter.
	if text != "aab b" {
		t.Errorf("text = %q, want %q", text, "aab b")
	}
	if conf < 0.89 || conf > 0.91 {
		t.Errorf("conf = %v", conf)
	}

	if text, conf := ctcGreedy(step(0), 1, len(dict), dict); text != "" || conf != 0 {
		t.Errorf("all-blank output should be empty, got %q/%v", text, conf)
	}
}

func TestCTCGreedyIgnoresClassesOutsideTheDictionary(t *testing.T) {
	// A model with more classes than the dictionary has entries must not panic.
	probs := []float32{0, 0, 0, 0.9}
	if text, _ := ctcGreedy(probs, 1, 4, []string{"", "a"}); text != "" {
		t.Errorf("text = %q", text)
	}
}

func TestBuildDictionary(t *testing.T) {
	d := buildDictionary("x\ny\nz")
	if len(d) != 5 || d[0] != "" || d[1] != "x" || d[3] != "z" || d[4] != " " {
		t.Errorf("dict = %q", d)
	}
}

func TestRoundUp32(t *testing.T) {
	for in, want := range map[int]int{0: 32, 1: 32, 32: 32, 33: 64, 960: 960, 961: 992} {
		if got := roundUp32(in); got != want {
			t.Errorf("roundUp32(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestSortReadingOrder(t *testing.T) {
	// Two lines; the second line's boxes are given right-to-left and slightly
	// offset vertically, which must not split them into separate lines.
	in := []image.Rectangle{
		image.Rect(200, 105, 300, 125), // line 2, right
		image.Rect(10, 10, 90, 30),     // line 1, left
		image.Rect(10, 100, 90, 120),   // line 2, left
		image.Rect(120, 12, 190, 32),   // line 1, right
	}
	got := sortReadingOrder(in)
	want := []image.Rectangle{
		image.Rect(10, 10, 90, 30), image.Rect(120, 12, 190, 32),
		image.Rect(10, 100, 90, 120), image.Rect(200, 105, 300, 125),
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestOCROptionsDefaults(t *testing.T) {
	o := OCROptions{Unclip: 1.2}.withDefaults()
	if o.Unclip != 1.2 || o.Thresh != detThresh || o.BoxThresh != detBoxThresh || o.MaxSide != detMaxSide {
		t.Errorf("%+v", o)
	}
}
