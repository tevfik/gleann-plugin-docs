package indra

import (
	"fmt"
	"html"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
)

var (
	wholeMath = regexp.MustCompile(`^\$[^$]+\$$`)
	itemStart = regexp.MustCompile(`^\s*(?:[•\-–▪◦*·→]|\(?\d{1,3}[.)]|\(?[a-zA-Z][.)])(?:\s|$)`)
)

func isCJK(r rune) bool {
	return (r >= 0x3000 && r <= 0x30FF) || (r >= 0x3400 && r <= 0x9FFF) || (r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFF00 && r <= 0xFFEF) || (r >= 0x20000 && r <= 0x2FA1F)
}

// joinLines joins the lines of one paragraph: "tumori-" + "genesis" becomes
// "tumorigenesis"; a break between two CJK characters takes no space.
func joinLines(texts []string) string {
	var out []rune
	for _, t := range texts {
		r := []rune(strings.TrimSpace(t))
		if len(r) == 0 {
			continue
		}
		switch {
		case len(out) > 1 && out[len(out)-1] == '-' && unicode.IsLetter(out[len(out)-2]) && unicode.IsLetter(r[0]) && unicode.IsLower(r[0]):
			out = append(out[:len(out)-1], r...)
		case len(out) > 0 && isCJK(out[len(out)-1]) && isCJK(r[0]):
			out = append(out, r...)
		case len(out) > 0:
			out = append(append(out, ' '), r...)
		default:
			out = r
		}
	}
	return string(out)
}

// splitBlocks cuts the reading order into runs whose neighbours have a
// positive same-block logit.
func splitBlocks(order []int, same []float32, n int) [][]int {
	var blocks [][]int
	for _, i := range order {
		if k := len(blocks); k > 0 && same[blocks[k-1][len(blocks[k-1])-1]*n+i] > 0 {
			blocks[k-1] = append(blocks[k-1], i)
		} else {
			blocks = append(blocks, []int{i})
		}
	}
	return blocks
}

func medianOf(v []float64) float64 {
	s := slices.Clone(v)
	slices.Sort(s)
	return median(s)
}

// cell is one table cell line.
type cell struct {
	box  [4]float64
	text string
}

// tableRows groups cells into rows by vertical centre, left to right.
func tableRows(cs []cell) [][]int {
	n := len(cs)
	cy := make([]float64, n)
	hs := make([]float64, n)
	for i, c := range cs {
		cy[i], hs[i] = (c.box[1]+c.box[3])/2, c.box[3]-c.box[1]
	}
	h := medianOf(hs)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return cy[idx[a]] < cy[idx[b]] })
	var rows [][]int
	for _, i := range idx {
		if k := len(rows); k > 0 {
			sum := 0.0
			for _, j := range rows[k-1] {
				sum += cy[j]
			}
			if math.Abs(cy[i]-sum/float64(len(rows[k-1]))) < 0.5*h {
				rows[k-1] = append(rows[k-1], i)
				continue
			}
		}
		rows = append(rows, []int{i})
	}
	for _, r := range rows {
		sort.SliceStable(r, func(a, b int) bool { return cs[r[a]].box[0] < cs[r[b]].box[0] })
	}
	return rows
}

// mergeWrapped joins the line rows of wrapped cells into one table row.
func mergeWrapped(rows [][]int, cs []cell) [][]int {
	if len(rows) < 3 {
		return rows
	}
	hs := make([]float64, len(cs))
	for i, c := range cs {
		hs[i] = c.box[3] - c.box[1]
	}
	h := medianOf(hs)
	gaps := make([]float64, len(rows)-1)
	for k := range gaps {
		minTop, maxBot := math.Inf(1), math.Inf(-1)
		for _, i := range rows[k+1] {
			minTop = math.Min(minTop, cs[i].box[1])
		}
		for _, i := range rows[k] {
			maxBot = math.Max(maxBot, cs[i].box[3])
		}
		gaps[k] = minTop - maxBot
	}
	med := medianOf(gaps)
	out := [][]int{slices.Clone(rows[0])}
	prev := rows[0]
	for k, r := range rows[1:] {
		if gaps[k] < 0.5*med && gaps[k] < 0.6*h && len(r) < len(prev) {
			out[len(out)-1] = append(out[len(out)-1], r...)
		} else {
			out = append(out, slices.Clone(r))
		}
		prev = r
	}
	return out
}

// tableHTML builds an HTML table from cell lines: rows by vertical centre,
// columns anchored on the fullest rows.
func tableHTML(cs []cell) string {
	rows := tableRows(cs)
	minX, maxX := math.Inf(1), math.Inf(-1)
	for _, c := range cs {
		minX, maxX = math.Min(minX, c.box[0]), math.Max(maxX, c.box[2])
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r))
	}
	if len(rows) > 2 && len(rows[0]) == 1 && width > 1 && cs[rows[0][0]].box[2]-cs[rows[0][0]].box[0] > 0.6*(maxX-minX) {
		var rest []cell
		for k, c := range cs {
			if k != rows[0][0] {
				rest = append(rest, c)
			}
		}
		return cs[rows[0][0]].text + "\n\n" + tableHTML(rest)
	}
	var full [][]int
	for _, r := range rows {
		if len(r) == width {
			full = append(full, r)
		}
	}
	rows = mergeWrapped(rows, cs)
	bands := make([][2]float64, width)
	for c := 0; c < width; c++ {
		var l, r []float64
		for _, row := range full {
			l, r = append(l, cs[row[c]].box[0]), append(r, cs[row[c]].box[2])
		}
		bands[c] = [2]float64{medianOf(l), medianOf(r)}
	}
	edges := []float64{math.Inf(-1)}
	for c := 0; c+1 < width; c++ {
		edges = append(edges, (bands[c][1]+bands[c+1][0])/2)
	}
	edges = append(edges, math.Inf(1))
	at := func(x float64) int { // searchsorted(edges, x, "right") - 1
		return sort.Search(len(edges), func(i int) bool { return edges[i] > x }) - 1
	}
	var sb strings.Builder
	sb.WriteString("<table>")
	for ri, r := range rows {
		tag := "td"
		if ri == 0 && len(rows) > 1 {
			tag = "th"
		}
		type span struct {
			n    int
			text string
		}
		cells := map[int]span{}
		order := slices.Clone(r)
		sort.SliceStable(order, func(a, b int) bool {
			ka, kb := cs[order[a]].box, cs[order[b]].box
			if ka[1] != kb[1] {
				return ka[1] < kb[1]
			}
			return ka[0] < kb[0]
		})
		for _, k := range order {
			x0, x1 := cs[k].box[0], cs[k].box[2]
			first, last := at(x0), at(x1)
			var cover []int
			for c := max(first, 0); c <= min(last, width-1); c++ {
				if math.Min(x1, bands[c][1])-math.Max(x0, bands[c][0]) > 0.5*(bands[c][1]-bands[c][0]) {
					cover = append(cover, c)
				}
			}
			if len(cover) == 0 {
				cover = []int{min(max(first, 0), width-1)}
			}
			c0 := cover[0]
			if prev, ok := cells[c0]; ok {
				cells[c0] = span{max(prev.n, len(cover)), prev.text + " " + cs[k].text}
			} else {
				cells[c0] = span{len(cover), cs[k].text}
			}
		}
		sb.WriteString("\n<tr>")
		for c := 0; c < width; {
			if sp, ok := cells[c]; ok {
				attr := ""
				if sp.n > 1 {
					attr = fmt.Sprintf(` colspan="%d"`, sp.n)
				}
				fmt.Fprintf(&sb, "<%s%s>%s</%s>", tag, attr, html.EscapeString(sp.text), tag)
				c += sp.n
			} else {
				fmt.Fprintf(&sb, "<%s></%s>", tag, tag)
				c++
			}
		}
		sb.WriteString("</tr>")
	}
	sb.WriteString("\n</table>")
	return sb.String()
}

// pageBlock is one layout block of a page, in reading order.
type pageBlock struct {
	Lines []int
	Class int
	Box   [4]float64
}

// pageBlocks cuts the reading order into blocks and classifies each.
func pageBlocks(ls []rline, lay *Layout) []pageBlock {
	var out []pageBlock
	for _, blk := range splitBlocks(lay.Order, lay.Same, lay.N) {
		pb := pageBlock{Lines: blk, Class: argmaxSum(lay.Cls, blk)}
		pb.Box = [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
		for _, i := range blk {
			b := ls[i].box
			pb.Box = [4]float64{math.Min(pb.Box[0], b[0]), math.Min(pb.Box[1], b[1]), math.Max(pb.Box[2], b[2]), math.Max(pb.Box[3], b[3])}
		}
		out = append(out, pb)
	}
	return out
}

// blockMarkdown renders one block by its class; "" for dropped furniture.
func (m *Model) blockMarkdown(ls []rline, blk pageBlock, keepFurniture bool) string {
	texts := make([]string, len(blk.Lines))
	for k, i := range blk.Lines {
		texts[k] = ls[i].text
	}
	if (blk.Class == ClsPageHeader || blk.Class == ClsPageFooter) && !keepFurniture {
		return ""
	}
	allMath := true
	for _, t := range texts {
		allMath = allMath && wholeMath.MatchString(strings.TrimSpace(t))
	}
	if allMath {
		eq := make([]string, len(texts))
		for k, t := range texts {
			t = strings.TrimSpace(t)
			eq[k] = "$$" + t[1:len(t)-1] + "$$"
		}
		return strings.Join(eq, "\n")
	}
	switch {
	case blk.Class == ClsSectionHeader:
		return "## " + joinLines(texts)
	case blk.Class == ClsTable && len(blk.Lines) > 1:
		var cs []cell
		for _, i := range blk.Lines {
			for _, p := range m.splitLine(ls[i], cellGap, nil) {
				cs = append(cs, cell{p.box, p.text})
			}
		}
		return tableHTML(cs)
	case blk.Class == ClsListGroup:
		var items [][]string
		for _, t := range texts {
			if len(items) == 0 || itemStart.MatchString(t) {
				items = append(items, []string{t})
			} else {
				items[len(items)-1] = append(items[len(items)-1], t)
			}
		}
		out := make([]string, len(items))
		for k, it := range items {
			out[k] = joinLines(it)
		}
		return strings.Join(out, "\n")
	}
	return joinLines(texts)
}
