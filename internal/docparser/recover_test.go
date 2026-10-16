package docparser

import (
	"image"
	"testing"
)

func TestUncoveredLines(t *testing.T) {
	page := image.Rect(0, 0, 1000, 1400)
	blocks := []BoundingBox{
		{Rect: image.Rect(50, 100, 500, 300), Type: TypeText},
		{Rect: image.Rect(50, 400, 300, 500), Type: TypeFigure}, // small figure: covers
	}
	lines := []image.Rectangle{
		image.Rect(60, 110, 480, 140),  // inside the text block: covered
		image.Rect(60, 600, 480, 630),  // nowhere: uncovered
		image.Rect(60, 420, 280, 450),  // inside a small figure: covered
		image.Rect(450, 110, 700, 140), // only the left 20% overlaps the block: uncovered
		image.Rect(60, 280, 480, 320),  // exactly half inside the block: covered
	}
	got := uncoveredLines(lines, blocks, page)
	if len(got) != 2 || got[0] != lines[1] || got[1] != lines[3] {
		t.Errorf("got %v", got)
	}
}

func TestHugeFigureDoesNotCoverText(t *testing.T) {
	page := image.Rect(0, 0, 1000, 1400)
	blocks := []BoundingBox{{Rect: page, Type: TypeFigure}} // a brochure's background photo
	lines := []image.Rectangle{image.Rect(100, 500, 600, 540)}
	if got := uncoveredLines(lines, blocks, page); len(got) != 1 {
		t.Errorf("text on a page-sized figure must still be read, got %v", got)
	}
	blocks[0].Type = TypeText
	if got := uncoveredLines(lines, blocks, page); len(got) != 0 {
		t.Errorf("a page-sized text block does cover its lines, got %v", got)
	}
}

func TestGroupLinesSeparatesColumnsAndParagraphs(t *testing.T) {
	lines := []image.Rectangle{
		image.Rect(50, 100, 450, 130), // left column, paragraph 1
		image.Rect(50, 135, 450, 165),
		image.Rect(520, 100, 920, 130), // right column
		image.Rect(520, 135, 920, 165),
		image.Rect(50, 300, 450, 330), // left column again after a big gap
	}
	groups := groupLines(lines)
	if len(groups) != 3 {
		t.Fatalf("got %d groups, want 3: %v", len(groups), groups)
	}
	sizes := map[int]int{}
	for _, g := range groups {
		sizes[len(g)]++
	}
	if sizes[2] != 2 || sizes[1] != 1 {
		t.Errorf("group sizes = %v", sizes)
	}
}

func TestIsFurniture(t *testing.T) {
	page := image.Rect(0, 0, 1000, 1000)
	cases := []struct {
		r    image.Rectangle
		want bool
	}{
		{image.Rect(100, 20, 900, 60), true},    // running header
		{image.Rect(450, 950, 550, 980), true},  // page number
		{image.Rect(100, 400, 900, 440), false}, // body
		{image.Rect(100, 60, 900, 100), false},  // starts inside the band's edge
	}
	for _, c := range cases {
		if got := isFurniture(c.r, page); got != c.want {
			t.Errorf("isFurniture(%v) = %v, want %v", c.r, got, c.want)
		}
	}
}

func TestUnionRect(t *testing.T) {
	got := unionRect([]image.Rectangle{image.Rect(10, 10, 20, 20), image.Rect(5, 30, 15, 40)})
	if got != image.Rect(5, 10, 20, 40) {
		t.Errorf("got %v", got)
	}
	if unionRect(nil) != (image.Rectangle{}) {
		t.Error("empty input")
	}
}

func bb(t ElementType, conf float32, r image.Rectangle) BoundingBox {
	return BoundingBox{Type: t, Confidence: conf, Rect: r}
}

func TestDropNested(t *testing.T) {
	big := image.Rect(0, 0, 400, 400)
	t.Run("near duplicates keep the more confident label", func(t *testing.T) {
		got := dropNested([]BoundingBox{bb(TypeText, 0.6, big), bb(TypeList, 0.9, image.Rect(5, 5, 395, 395))})
		if len(got) != 1 || got[0].Type != TypeList {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("a small block inside a text block is dropped", func(t *testing.T) {
		got := dropNested([]BoundingBox{bb(TypeText, 0.5, big), bb(TypeTitle, 0.99, image.Rect(10, 10, 100, 40))})
		if len(got) != 1 || got[0].Type != TypeText {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("a block inside a figure is kept", func(t *testing.T) {
		got := dropNested([]BoundingBox{bb(TypeFigure, 0.9, big), bb(TypeText, 0.8, image.Rect(10, 10, 100, 40))})
		if len(got) != 2 {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("partial overlap keeps both", func(t *testing.T) {
		got := dropNested([]BoundingBox{bb(TypeText, 0.9, image.Rect(0, 0, 200, 200)), bb(TypeText, 0.9, image.Rect(150, 150, 350, 350))})
		if len(got) != 2 {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("disjoint and empty", func(t *testing.T) {
		if got := dropNested(nil); len(got) != 0 {
			t.Errorf("nil: %v", got)
		}
		got := dropNested([]BoundingBox{bb(TypeText, 0.9, image.Rect(0, 0, 10, 10)), bb(TypeText, 0.9, image.Rect(50, 50, 60, 60))})
		if len(got) != 2 {
			t.Errorf("disjoint: %v", got)
		}
	})
}

func TestAssignLines(t *testing.T) {
	page := image.Rect(0, 0, 1000, 1400)
	blocks := []BoundingBox{
		{Rect: image.Rect(50, 100, 500, 300), Type: TypeText, Confidence: 0.9},
		{Rect: image.Rect(50, 400, 500, 600), Type: TypeFigure, Confidence: 0.8},
		{Rect: image.Rect(50, 100, 500, 300), Type: TypeList, Confidence: 0.5}, // same area, less confident
	}
	lines := []image.Rectangle{
		image.Rect(60, 110, 480, 140), // text block (first match wins over the duplicate)
		image.Rect(60, 450, 480, 480), // inside the figure
		image.Rect(60, 900, 480, 930), // nowhere
		image.Rect(60, 150, 480, 180), // text block again
		image.Rect(0, 0, 0, 0),        // empty box is ignored
	}
	per, un := assignLines(lines, blocks, page)
	if len(per[0]) != 2 || len(per[1]) != 1 || len(per[2]) != 0 {
		t.Errorf("per-block counts = %d/%d/%d, want 2/1/0", len(per[0]), len(per[1]), len(per[2]))
	}
	if len(un) != 1 || un[0] != lines[2] {
		t.Errorf("uncovered = %v", un)
	}
	// Order inside a block follows the order of the input.
	if per[0][0] != lines[0] || per[0][1] != lines[3] {
		t.Errorf("block lines out of order: %v", per[0])
	}
}
