package indra

import (
	"encoding/json"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Parity against the Python package: INDRA_MODELS is the model directory and
// INDRA_PAGES a directory with page PNGs next to <page>.png.nolm.json files
// (boxes and texts of the Python pipeline with the language model and script
// routing off). Skipped when unset.
type refPage struct {
	Boxes [][]float64 `json:"boxes"`
	Texts []string    `json:"texts"`
	Order []int       `json:"order"`
}

func lev(a, b []rune) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			c := 1
			if a[i-1] == b[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev = cur
	}
	return prev[len(b)]
}

func TestParityWithPython(t *testing.T) {
	dir, pages := os.Getenv("INDRA_MODELS"), os.Getenv("INDRA_PAGES")
	if dir == "" || pages == "" {
		t.Skip("INDRA_MODELS / INDRA_PAGES not set")
	}
	m, err := Open(dir, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	files, _ := filepath.Glob(filepath.Join(pages, "*.png"))
	for _, f := range files {
		raw, err := os.ReadFile(f + ".nolm.json")
		if err != nil {
			continue
		}
		var ref refPage
		if err := json.Unmarshal(raw, &ref); err != nil {
			t.Fatal(err)
		}
		fh, _ := os.Open(f)
		img, err := png.Decode(fh)
		fh.Close()
		if err != nil {
			t.Fatal(err)
		}
		g := FromImage(img)
		boxes, err := m.Detect(g)
		if err != nil {
			t.Fatal(err)
		}
		if len(boxes) != len(ref.Boxes) {
			t.Errorf("%s: %d boxes, python %d", filepath.Base(f), len(boxes), len(ref.Boxes))
			continue
		}
		var maxd float64
		for i, b := range boxes {
			for k, v := range []float64{b.X0, b.Y0, b.X1, b.Y1} {
				maxd = math.Max(maxd, math.Abs(v-ref.Boxes[i][k]))
			}
		}
		lines, err := m.Recognize(g, boxes)
		if err != nil {
			t.Fatal(err)
		}
		var edits, chars, exact int
		for i, l := range lines {
			a, b := []rune(l.Text), []rune(ref.Texts[i])
			edits += lev(a, b)
			chars += len(b)
			if l.Text == ref.Texts[i] {
				exact++
			}
		}
		t.Logf("%s: %d lines, max box diff %.2fpx, exact %d/%d, CER vs python %.4f", filepath.Base(f), len(lines), maxd, exact, len(lines), float64(edits)/float64(max(chars, 1)))
		lay, err := m.ReadingOrder(lines, g.W, g.H)
		if err != nil {
			t.Fatal(err)
		}
		got := lay.Order
		same := 0
		for i := range got {
			if i < len(ref.Order) && got[i] == ref.Order[i] {
				same++
			}
		}
		t.Logf("%s: %d lines, reading order identical at %d/%d positions", filepath.Base(f), len(got), same, len(ref.Order))
		// Boxes agree to ~0.03 px, not bit for bit; cells of one table row sit on a
		// rounding boundary of the top-down row key, so a few swaps are expected.
		if len(got) != len(ref.Order) || float64(same) < 0.9*float64(len(ref.Order)) {
			t.Errorf("%s: reading order differs from python", filepath.Base(f))
		}
		if maxd > 3 {
			t.Errorf("%s: box diff %.2f px", filepath.Base(f), maxd)
		}
		if float64(edits)/float64(max(chars, 1)) > 0.01 {
			t.Errorf("%s: text differs from python", filepath.Base(f))
		}
	}
}
