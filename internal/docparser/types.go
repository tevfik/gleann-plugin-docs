// Package docparser is a CPU-only page parser: a layout model finds blocks, a
// light OCR (PP-OCRv4 via ONNX Runtime) reads text blocks, and tables/figures
// can be handed to a heavier recogniser (Granite-Docling through eif-runtime).
package docparser

import (
	"image"
	"time"
)

// ElementType is the kind of a layout block.
type ElementType string

const (
	TypeText    ElementType = "text"
	TypeTitle   ElementType = "title"
	TypeList    ElementType = "list"
	TypeTable   ElementType = "table"
	TypeFigure  ElementType = "figure"
	TypeChart   ElementType = "chart"
	TypeFormula ElementType = "formula"
)

// NormGrid is the side of the normalised coordinate grid Granite-Docling's
// <loc_N> tokens use (0..500).
const NormGrid = 500

// BoundingBox is a detected block.
type BoundingBox struct {
	Rect       image.Rectangle // page pixels
	NormCoords [4]int          // xmin, ymin, xmax, ymax on the 0..500 grid
	Type       ElementType
	Confidence float32
}

// ParsedBlock is a block together with its Markdown.
type ParsedBlock struct {
	BBox     BoundingBox
	Markdown string
	// Source tells which path produced Markdown: "ocr", "ocr-grid" (table
	// rebuilt from OCR boxes), "deep" (heavy recogniser) or "".
	Source string
}

// Timings breaks the cost of one page down per stage.
type Timings struct {
	Layout time.Duration
	Det    time.Duration // text detection, summed over blocks
	Rec    time.Duration // text recognition, summed over lines
	Deep   time.Duration
	Total  time.Duration
	Blocks int
	Lines  int
}

// normalize converts a pixel rectangle into the 0..500 grid of a w x h page.
func normalize(r image.Rectangle, w, h int) [4]int {
	if w <= 0 || h <= 0 {
		return [4]int{}
	}
	c := func(v, size int) int {
		n := v * NormGrid / size
		if n < 0 {
			return 0
		}
		if n > NormGrid {
			return NormGrid
		}
		return n
	}
	return [4]int{c(r.Min.X, w), c(r.Min.Y, h), c(r.Max.X, w), c(r.Max.Y, h)}
}
