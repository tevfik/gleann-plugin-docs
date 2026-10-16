package docparser

import (
	"image"
	"sort"
)

// The layout model only knows what it was trained on (PubLayNet: no reference
// lists, headers, footers, side notes). Text it does not cover would silently
// vanish, so the parser also detects text lines on the whole page and reads the
// ones no block covers.

const (
	coveredFraction = 0.5  // a line is covered if this much of it lies inside one block
	furnitureBand   = 0.08 // top/bottom share of the page holding headers, footers, page numbers
)

// maxFigureCover is the share of the page above which a "figure" block is
// treated as a misdetection (a brochure photo or coloured background) and no
// longer counts as covering the text on it.
const maxFigureCover = 0.5

// assignLines gives every detected line to the layout block that covers it (the
// first, i.e. most confident, block holding at least half of the line) and
// returns the lines no block covers. perBlock[i] belongs to blocks[i]; lines
// that fall in a figure are assigned to it, so they are neither read nor
// recovered. A page-sized "figure" (a brochure's background photo) is a
// misdetection and covers nothing.
func assignLines(lines []image.Rectangle, blocks []BoundingBox, page image.Rectangle) (perBlock [][]image.Rectangle, uncovered []image.Rectangle) {
	perBlock = make([][]image.Rectangle, len(blocks))
	pageArea := page.Dx() * page.Dy()
	for _, ln := range lines {
		area := ln.Dx() * ln.Dy()
		if area == 0 {
			continue
		}
		assigned := false
		for i, b := range blocks {
			if b.Type == TypeFigure && float64(b.Rect.Dx()*b.Rect.Dy()) >= maxFigureCover*float64(pageArea) {
				continue
			}
			in := ln.Intersect(b.Rect)
			if !in.Empty() && float64(in.Dx()*in.Dy()) >= coveredFraction*float64(area) {
				perBlock[i] = append(perBlock[i], ln)
				assigned = true
				break
			}
		}
		if !assigned {
			uncovered = append(uncovered, ln)
		}
	}
	return perBlock, uncovered
}

// uncoveredLines returns the line boxes that no layout block covers on a page
// of the given bounds.
func uncoveredLines(lines []image.Rectangle, blocks []BoundingBox, page image.Rectangle) []image.Rectangle {
	_, uncovered := assignLines(lines, blocks, page)
	return uncovered
}

// groupLines clusters line boxes into paragraph-like groups: a line joins the
// previous group when it follows closely below it and overlaps it horizontally
// (so the two columns of a page stay apart). The input must be in reading order.
func groupLines(lines []image.Rectangle) [][]image.Rectangle {
	// Work column by column: order by x first so interleaved columns separate.
	type group struct {
		rects []image.Rectangle
		box   image.Rectangle
	}
	var groups []*group
	sorted := append([]image.Rectangle(nil), lines...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Min.Y < sorted[j].Min.Y })

	for _, ln := range sorted {
		var best *group
		for _, g := range groups {
			last := g.rects[len(g.rects)-1]
			gap := ln.Min.Y - last.Max.Y
			h := max(last.Dy(), ln.Dy())
			if gap > h { // too far below
				continue
			}
			ox := min(ln.Max.X, g.box.Max.X) - max(ln.Min.X, g.box.Min.X)
			if ox <= 0 || float64(ox) < 0.3*float64(min(ln.Dx(), g.box.Dx())) {
				continue
			}
			if best == nil || last.Max.Y > best.rects[len(best.rects)-1].Max.Y {
				best = g
			}
		}
		if best == nil {
			groups = append(groups, &group{rects: []image.Rectangle{ln}, box: ln})
			continue
		}
		best.rects = append(best.rects, ln)
		best.box = best.box.Union(ln)
	}

	out := make([][]image.Rectangle, len(groups))
	for i, g := range groups {
		out[i] = sortReadingOrder(g.rects)
	}
	return out
}

// isFurniture reports whether r lies inside the header or footer band of a page.
func isFurniture(r, page image.Rectangle) bool {
	band := int(furnitureBand * float64(page.Dy()))
	return r.Max.Y <= page.Min.Y+band || r.Min.Y >= page.Max.Y-band
}

func unionRect(rects []image.Rectangle) image.Rectangle {
	var u image.Rectangle
	for i, r := range rects {
		if i == 0 {
			u = r
		} else {
			u = u.Union(r)
		}
	}
	return u
}
