package docparser

import (
	"image"
	"testing"
)

func blk(name string, x1, y1, x2, y2 int) ParsedBlock {
	return ParsedBlock{Markdown: name, BBox: BoundingBox{Rect: image.Rect(x1, y1, x2, y2), Type: TypeText, Confidence: 0.9}}
}

func names(bs []ParsedBlock) []string {
	var out []string
	for _, b := range bs {
		out = append(out, b.Markdown)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOrderTwoColumnsReadsColumnByColumn(t *testing.T) {
	// Paragraph heights differ between the columns, so a sort by Y interleaves them.
	in := []ParsedBlock{
		blk("R1", 520, 100, 940, 300),
		blk("L2", 60, 320, 480, 500),
		blk("L1", 60, 100, 480, 300),
		blk("R2", 520, 310, 940, 520),
	}
	got := names(orderBlocks(in))
	if want := []string{"L1", "L2", "R1", "R2"}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestOrderFullWidthBlocksSplitBands(t *testing.T) {
	in := []ParsedBlock{
		blk("R1", 520, 200, 940, 400),
		blk("Table", 60, 600, 940, 700), // spans both columns
		blk("Title", 60, 40, 940, 120),
		blk("L1", 60, 200, 480, 400),
		blk("L2", 60, 420, 480, 580),
		blk("Tail", 60, 720, 940, 800),
	}
	got := names(orderBlocks(in))
	if want := []string{"Title", "L1", "L2", "R1", "Table", "Tail"}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestOrderSingleColumnByY(t *testing.T) {
	in := []ParsedBlock{blk("c", 60, 500, 900, 600), blk("a", 60, 100, 900, 200), blk("b", 60, 300, 900, 400)}
	if got, want := names(orderBlocks(in)), []string{"a", "b", "c"}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestOrderOverlappingBlocksFallBackToYThenX(t *testing.T) {
	// Overlap in both projections: no gap anywhere, so the last resort applies.
	in := []ParsedBlock{blk("b", 100, 105, 400, 400), blk("a", 50, 100, 350, 390)}
	if got, want := names(orderBlocks(in)), []string{"a", "b"}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := orderBlocks(nil); len(got) != 0 {
		t.Errorf("nil input: %v", got)
	}
}

func TestOrderDoesNotMutateItsInput(t *testing.T) {
	in := []ParsedBlock{blk("b", 60, 300, 900, 400), blk("a", 60, 100, 900, 200)}
	orderBlocks(in)
	if in[0].Markdown != "b" {
		t.Error("input slice was reordered in place")
	}
}
