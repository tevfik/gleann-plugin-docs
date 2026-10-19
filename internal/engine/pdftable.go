package engine

import (
	"context"
	"encoding/xml"
	"fmt"
	"image"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/tevfik/gleann-plugin-docs/internal/docparser"
)

// Word-level geometry of a digital PDF page, from `pdftotext -bbox-layout`
// (coordinates in PDF points, origin top-left).
type bboxPage struct {
	Width  float64     `xml:"width,attr"`
	Height float64     `xml:"height,attr"`
	Blocks []bboxBlock `xml:"flow>block"`
}

type bboxBlock struct {
	Lines []bboxLine `xml:"line"`
}

type bboxLine struct {
	Words []bboxWord `xml:"word"`
}

type bboxWord struct {
	XMin float64 `xml:"xMin,attr"`
	YMin float64 `xml:"yMin,attr"`
	XMax float64 `xml:"xMax,attr"`
	YMax float64 `xml:"yMax,attr"`
	Text string  `xml:",chardata"`
}

// pdfPageBBox returns the text layer of one page with word positions.
func pdfPageBBox(ctx context.Context, pdfPath string, page int) (bboxPage, error) {
	p := strconv.Itoa(page)
	out, err := exec.CommandContext(ctx, "pdftotext", "-bbox-layout", "-f", p, "-l", p, pdfPath, "-").Output()
	if err != nil {
		return bboxPage{}, err
	}
	return parseBBox(out)
}

func parseBBox(data []byte) (bboxPage, error) {
	var doc struct {
		Pages []bboxPage `xml:"body>doc>page"`
	}
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	if err := dec.Decode(&doc); err != nil {
		return bboxPage{}, err
	}
	if len(doc.Pages) == 0 {
		return bboxPage{}, fmt.Errorf("pdftotext -bbox-layout returned no page")
	}
	return doc.Pages[0], nil
}

func (l bboxLine) text() string {
	parts := make([]string, 0, len(l.Words))
	for _, w := range l.Words {
		parts = append(parts, strings.TrimSpace(w.Text))
	}
	return strings.Join(parts, " ")
}

// center of the line in image pixels.
func (l bboxLine) center(scale float64) image.Point {
	if len(l.Words) == 0 {
		return image.Point{}
	}
	x0, y0, x1, y1 := l.Words[0].XMin, l.Words[0].YMin, l.Words[0].XMax, l.Words[0].YMax
	for _, w := range l.Words[1:] {
		x0, y0 = math.Min(x0, w.XMin), math.Min(y0, w.YMin)
		x1, y1 = math.Max(x1, w.XMax), math.Max(y1, w.YMax)
	}
	return image.Pt(int((x0+x1)/2*scale), int((y0+y1)/2*scale))
}

// spliceTables returns the page text with every table region replaced by a
// Markdown table built from the words inside it. scale converts points to the
// pixels the table rectangles are in. Blocks keep their order and are separated
// by a blank line, as pdftotext does. A region whose words do not form a grid is
// left as ordinary text; ok is false when no table was rebuilt.
func spliceTables(pg bboxPage, scale float64, tables []image.Rectangle) (md string, ok bool) {
	// Words per table, by the centre of the line they belong to.
	inTable := func(l bboxLine) int {
		c := l.center(scale)
		for i, r := range tables {
			if c.In(r.Inset(-2)) {
				return i
			}
		}
		return -1
	}
	cells := make(map[int][]docparser.TextLine)
	for i := range tables {
		var words []bboxWord
		for _, b := range pg.Blocks {
			for _, l := range b.Lines {
				if inTable(l) == i {
					words = append(words, l.Words...)
				}
			}
		}
		cells[i] = wordsToCells(words, scale)
	}
	rendered := make(map[int]string)
	for i, c := range cells {
		if t := docparser.GridMarkdown(c); t != "" {
			rendered[i] = t
		}
	}
	if len(rendered) == 0 {
		return "", false
	}

	var out []string
	emitted := make(map[int]bool)
	for _, b := range pg.Blocks {
		var chunk []string
		flush := func() {
			if len(chunk) > 0 {
				out = append(out, strings.Join(chunk, "\n"))
				chunk = nil
			}
		}
		for _, l := range b.Lines {
			if ti := inTable(l); ti >= 0 && rendered[ti] != "" {
				if !emitted[ti] {
					flush()
					out = append(out, rendered[ti])
					emitted[ti] = true
				}
				continue
			}
			if t := l.text(); t != "" {
				chunk = append(chunk, t)
			}
		}
		flush()
	}
	return strings.Join(out, "\n\n"), true
}

// wordsToCells groups the words of a table into cell-sized text pieces: words
// on one row whose gap is no wider than a word space belong to the same cell.
func wordsToCells(words []bboxWord, scale float64) []docparser.TextLine {
	if len(words) == 0 {
		return nil
	}
	type pw struct {
		r image.Rectangle
		t string
	}
	ws := make([]pw, 0, len(words))
	var heights []int
	for _, w := range words {
		t := strings.TrimSpace(w.Text)
		if t == "" {
			continue
		}
		r := image.Rect(int(w.XMin*scale), int(w.YMin*scale), int(w.XMax*scale), int(w.YMax*scale))
		ws = append(ws, pw{r, t})
		heights = append(heights, r.Dy())
	}
	if len(ws) == 0 {
		return nil
	}
	sort.Ints(heights)
	medH := heights[len(heights)/2]

	sort.SliceStable(ws, func(i, j int) bool {
		ci, cj := ws[i].r.Min.Y+ws[i].r.Dy()/2, ws[j].r.Min.Y+ws[j].r.Dy()/2
		return ci < cj
	})
	var rows [][]pw
	for _, w := range ws {
		cy := w.r.Min.Y + w.r.Dy()/2
		if n := len(rows); n > 0 {
			last := rows[n-1][0]
			if abs(cy-(last.r.Min.Y+last.r.Dy()/2)) <= medH*6/10 {
				rows[n-1] = append(rows[n-1], w)
				continue
			}
		}
		rows = append(rows, []pw{w})
	}

	gap := medH * 7 / 10 // a word space is ~0.3 em; a column gutter is wider
	var lines []docparser.TextLine
	for _, row := range rows {
		sort.SliceStable(row, func(i, j int) bool { return row[i].r.Min.X < row[j].r.Min.X })
		cur := docparser.TextLine{Rect: row[0].r, Text: row[0].t}
		for _, w := range row[1:] {
			if w.r.Min.X-cur.Rect.Max.X <= gap {
				cur.Text += " " + w.t
				cur.Rect = cur.Rect.Union(w.r)
				continue
			}
			lines = append(lines, cur)
			cur = docparser.TextLine{Rect: w.r, Text: w.t}
		}
		lines = append(lines, cur)
	}
	return lines
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
