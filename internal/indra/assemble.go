package indra

import (
	"math"
	"regexp"
	"slices"
	"strings"
)

const (
	splitGap    = 3.0 // cut a detected line at a wider gap (line heights)
	cellGap     = 1.8 // cut a table line into cells at a wider gap
	gutterGap   = 1.6 // smaller gap when other lines' edges line up with it
	gutterEdges = 3   // how many other line edges must line up
	markerGap   = 2.5 // a list marker and its item are at most this far apart
	spaceID     = 1   // class id of " " (chars[0] of the Latin vocabulary)
)

// rline is a recognised line in the form the assembly stages work on.
type rline struct {
	box  [4]float64
	text string
	conf float64
	toks []Tok
}

func (m *Model) isSpace(t Tok) bool { return t.ID == spaceID }

func (m *Model) decodeToks(toks []Tok) string {
	ids := make([]int64, len(toks))
	for i, t := range toks {
		ids[i] = t.ID
	}
	return m.decode(ids)
}

// gutterCuts returns, per line, the x positions of gaps that sit on a column
// gutter: a gap over gutterGap line heights where at least gutterEdges other
// lines end at its left side or start at its right side.
func (m *Model) gutterCuts(ls []rline) [][]float64 {
	cuts := make([][]float64, len(ls))
	for i, l := range ls {
		h := math.Max(1, l.box[3]-l.box[1])
		var xs []float64
		for _, t := range l.toks {
			if !m.isSpace(t) {
				xs = append(xs, t.X)
			}
		}
		for k := 0; k+1 < len(xs); k++ {
			a, c := xs[k], xs[k+1]
			if c-a <= gutterGap*h {
				continue
			}
			gl, gr := a+0.35*h, c-0.35*h
			near := 0
			for j, o := range ls {
				if j != i && (math.Abs(o.box[2]-gl) < 0.5*h || math.Abs(o.box[0]-gr) < 0.5*h) {
					near++
				}
			}
			if near >= gutterEdges {
				cuts[i] = append(cuts[i], (a+c)/2)
			}
		}
	}
	return cuts
}

// splitLine cuts a line where two neighbouring characters are more than gap
// line heights apart (the detector merged two cells or columns), or where a
// position in at falls between them.
func (m *Model) splitLine(l rline, gap float64, at []float64) []rline {
	h := math.Max(1, l.box[3]-l.box[1])
	slices.Sort(at)
	segs := [][]Tok{{}}
	prev, havePrev := 0.0, false
	for _, t := range l.toks {
		if !m.isSpace(t) {
			cut := false
			if havePrev {
				cut = t.X-prev > gap*h
				for _, c := range at {
					if prev < c && c < t.X {
						cut = true
					}
				}
			}
			if cut {
				segs = append(segs, nil)
			}
			prev, havePrev = t.X, true
		}
		segs[len(segs)-1] = append(segs[len(segs)-1], t)
	}
	if len(segs) == 1 {
		return []rline{{box: l.box, text: m.decodeToks(l.toks), conf: l.conf, toks: l.toks}}
	}
	var out []rline
	for _, sg := range segs {
		text := strings.TrimSpace(m.decodeToks(sg))
		lo, hi, any := math.Inf(1), math.Inf(-1), false
		for _, t := range sg {
			if !m.isSpace(t) {
				lo, hi, any = math.Min(lo, t.X), math.Max(hi, t.X), true
			}
		}
		if text != "" && any {
			out = append(out, rline{
				box:  [4]float64{math.Max(l.box[0], lo-0.35*h), l.box[1], math.Min(l.box[2], hi+0.35*h), l.box[3]},
				text: text, conf: l.conf, toks: sg,
			})
		}
	}
	return out
}

var (
	leaderChars = ".·…‥_-–—="
	dashChars   = "-–—"
	bulletChars = "•◦▪▫●○■□‣⁃∙*"
	enumerator  = regexp.MustCompile(`^(?:(?:\d{1,3}|[A-Za-z])[.)]|\((?:\d{1,3}|[A-Za-z]|[ivxlcIVXLC]{1,5})\))$`)
)

func onlyIn(s, set string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return !strings.ContainsRune(set, r) }) < 0
}

// tidyLines drops lines of leader characters only (form fill-in dots and rules)
// and joins a lone list marker to the line that starts right after it.
func (m *Model) tidyLines(ls []rline) []rline {
	keep := make([]bool, len(ls))
	for i := range keep {
		keep[i] = true
	}
	ls = slices.Clone(ls)
	for i, l := range ls {
		core := strings.ReplaceAll(l.text, " ", "")
		if onlyIn(core, leaderChars) {
			h, w := l.box[3]-l.box[1], l.box[2]-l.box[0]
			wide := w >= 3*h
			if len([]rune(core)) >= 3 || wide || !strings.ContainsAny(core, dashChars) {
				keep[i] = false
			}
		}
	}
	for i := range ls {
		t := strings.TrimSpace(ls[i].text)
		if !keep[i] || !(onlyIn(t, bulletChars) && len([]rune(t)) == 1 || enumerator.MatchString(t)) {
			continue
		}
		best, bestGap := -1, 0.0
		hi := ls[i].box[3] - ls[i].box[1]
		for j := range ls {
			if j == i || !keep[j] || strings.TrimSpace(ls[j].text) == "" {
				continue
			}
			hj := ls[j].box[3] - ls[j].box[1]
			ov := math.Min(ls[i].box[3], ls[j].box[3]) - math.Max(ls[i].box[1], ls[j].box[1])
			if ov < 0.5*math.Min(hi, hj) {
				continue
			}
			gap := ls[j].box[0] - ls[i].box[2]
			if -0.3*hi <= gap && gap <= markerGap*math.Max(hi, hj) && (best < 0 || gap < bestGap) {
				best, bestGap = j, gap
			}
		}
		if best < 0 {
			continue
		}
		j := &ls[best]
		if len(ls[i].toks) > 0 && len(j.toks) > 0 {
			x := (ls[i].box[2] + j.box[0]) / 2
			toks := append(slices.Clone(ls[i].toks), Tok{spaceID, x})
			j.toks = append(toks, j.toks...)
		}
		j.text = t + " " + strings.TrimSpace(j.text)
		j.box = [4]float64{
			math.Min(ls[i].box[0], j.box[0]), math.Min(ls[i].box[1], j.box[1]),
			math.Max(ls[i].box[2], j.box[2]), math.Max(ls[i].box[3], j.box[3]),
		}
		keep[i] = false
	}
	var out []rline
	for i, l := range ls {
		if keep[i] {
			out = append(out, l)
		}
	}
	return out
}
