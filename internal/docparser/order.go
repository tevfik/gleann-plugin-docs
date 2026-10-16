package docparser

import (
	"image"
	"sort"
)

// orderBlocks arranges blocks into reading order with a recursive XY-cut:
// split at the widest vertical gap (columns) if there is one, otherwise at the
// widest horizontal gap (bands separated by full-width titles and tables). A
// plain "sort by Y, then X" interleaves the lines of a two-column page, which
// is why that is only the last resort.
func orderBlocks(blocks []ParsedBlock) []ParsedBlock {
	if len(blocks) <= 1 {
		return blocks
	}
	return xyCut(blocks, 0)
}

const xyCutMaxDepth = 16

func xyCut(blocks []ParsedBlock, depth int) []ParsedBlock {
	if len(blocks) <= 1 {
		return blocks
	}
	if depth < xyCutMaxDepth {
		// Columns first: a gutter that runs the full height of the group means
		// "read the left column to its end, then the right one".
		if parts := splitAtGap(blocks, false); parts != nil {
			return concatParts(parts, depth)
		}
		// Full-width titles and tables cross the gutter and so block that cut.
		// They separate the page into bands read top to bottom; inside a band
		// the columns can be cut.
		if parts := splitAtFullWidth(blocks); parts != nil {
			return concatParts(parts, depth)
		}
		if parts := splitAtGap(blocks, true); parts != nil {
			return concatParts(parts, depth)
		}
	}
	out := append([]ParsedBlock(nil), blocks...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].BBox.Rect, out[j].BBox.Rect
		if abs(a.Min.Y-b.Min.Y) > 15 {
			return a.Min.Y < b.Min.Y
		}
		return a.Min.X < b.Min.X
	})
	return out
}

// fullWidthShare is the share of a group's width a block must span to count as
// a full-width separator.
const fullWidthShare = 0.7

// splitAtFullWidth cuts blocks into bands around the blocks that span most of
// the group's width: [blocks above the first one], [it], [blocks between], ...
// Every part is smaller than the input, so recursion terminates. It returns nil
// when there is no full-width block, or when it would not split anything.
func splitAtFullWidth(blocks []ParsedBlock) [][]ParsedBlock {
	box := blocks[0].BBox.Rect
	for _, b := range blocks[1:] {
		box = box.Union(b.BBox.Rect)
	}
	var wide []ParsedBlock
	var rest []ParsedBlock
	for _, b := range blocks {
		if float64(b.BBox.Rect.Dx()) >= fullWidthShare*float64(box.Dx()) {
			wide = append(wide, b)
		} else {
			rest = append(rest, b)
		}
	}
	if len(wide) == 0 || len(rest) == 0 {
		return nil // nothing to separate (or a plain single column)
	}
	sort.SliceStable(wide, func(i, j int) bool { return wide[i].BBox.Rect.Min.Y < wide[j].BBox.Rect.Min.Y })

	// bands[k] holds the narrow blocks lying between wide[k-1] and wide[k].
	bands := make([][]ParsedBlock, len(wide)+1)
	for _, b := range rest {
		cy := (b.BBox.Rect.Min.Y + b.BBox.Rect.Max.Y) / 2
		k := 0
		for k < len(wide) && wide[k].BBox.Rect.Max.Y <= cy {
			k++
		}
		bands[k] = append(bands[k], b)
	}

	var parts [][]ParsedBlock
	for k := range bands {
		if len(bands[k]) > 0 {
			parts = append(parts, bands[k])
		}
		if k < len(wide) {
			parts = append(parts, []ParsedBlock{wide[k]})
		}
	}
	return parts
}

func concatParts(parts [][]ParsedBlock, depth int) []ParsedBlock {
	var out []ParsedBlock
	for _, p := range parts {
		out = append(out, xyCut(p, depth+1)...)
	}
	return out
}

// splitAtGap splits blocks into two groups at the widest empty band of the
// projection onto the Y axis (horizontal=true) or the X axis. It returns nil
// when no gap exists or the gap is too narrow to be a real separation.
func splitAtGap(blocks []ParsedBlock, horizontal bool) [][]ParsedBlock {
	span := func(r image.Rectangle) (lo, hi int) {
		if horizontal {
			return r.Min.Y, r.Max.Y
		}
		return r.Min.X, r.Max.X
	}

	idx := make([]int, len(blocks))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		la, _ := span(blocks[idx[a]].BBox.Rect)
		lb, _ := span(blocks[idx[b]].BBox.Rect)
		return la < lb
	})

	// Sweep along the axis, tracking the furthest edge seen so far.
	_, reach := span(blocks[idx[0]].BBox.Rect)
	bestGap, bestAt := 0, -1
	for k := 1; k < len(idx); k++ {
		lo, hi := span(blocks[idx[k]].BBox.Rect)
		if gap := lo - reach; gap > bestGap {
			bestGap, bestAt = gap, k
		}
		reach = max(reach, hi)
	}
	// A vertical cut needs a clear gutter; a horizontal one only has to exist.
	minGap := 1
	if !horizontal {
		minGap = 12
	}
	if bestAt < 0 || bestGap < minGap {
		return nil
	}

	var first, second []ParsedBlock
	for k, i := range idx {
		if k < bestAt {
			first = append(first, blocks[i])
		} else {
			second = append(second, blocks[i])
		}
	}
	return [][]ParsedBlock{first, second}
}
