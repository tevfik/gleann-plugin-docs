package engine

import (
	"image"
	"strings"
	"testing"
)

func word(x0, y0, x1, y1 float64, t string) bboxWord {
	return bboxWord{XMin: x0, YMin: y0, XMax: x1, YMax: y1, Text: t}
}

// cellLine is one table row of two cells, each a single word, 10pt high.
func cellLine(y float64, a, b string) bboxLine {
	return bboxLine{Words: []bboxWord{word(100, y, 140, y+10, a)}}
}

func TestSpliceTables(t *testing.T) {
	pg := bboxPage{Width: 600, Height: 800, Blocks: []bboxBlock{
		{Lines: []bboxLine{
			{Words: []bboxWord{word(50, 50, 90, 60, "Results"), word(95, 50, 150, 60, "overview")}},
			{Words: []bboxWord{word(50, 62, 120, 72, "follow-up")}},
		}},
		// One poppler line per table row, cells far apart.
		{Lines: []bboxLine{
			{Words: []bboxWord{word(100, 200, 140, 210, "Name"), word(300, 200, 340, 210, "Value")}},
			{Words: []bboxWord{word(100, 220, 140, 230, "Alpha"), word(300, 220, 340, 230, "100")}},
			{Words: []bboxWord{word(100, 240, 140, 250, "Beta"), word(300, 240, 340, 250, "200")}},
		}},
		{Lines: []bboxLine{{Words: []bboxWord{word(50, 400, 100, 410, "Closing"), word(105, 400, 150, 410, "text")}}}},
	}}
	table := image.Rect(90, 190, 350, 260) // scale 1

	md, ok := spliceTables(pg, 1, []image.Rectangle{table})
	if !ok {
		t.Fatal("table was not rebuilt")
	}
	want := "Results overview\nfollow-up\n\n" +
		"| Name | Value |\n| --- | --- |\n| Alpha | 100 |\n| Beta | 200 |\n\n" +
		"Closing text"
	if md != want {
		t.Errorf("got:\n%s\nwant:\n%s", md, want)
	}
}

func TestSpliceTablesScalesRectangles(t *testing.T) {
	pg := bboxPage{Blocks: []bboxBlock{{Lines: []bboxLine{
		{Words: []bboxWord{word(10, 10, 30, 15, "A"), word(60, 10, 80, 15, "B")}},
		{Words: []bboxWord{word(10, 20, 30, 25, "1"), word(60, 20, 80, 25, "2")}},
	}}}}
	// The same table as above but rectangles are in pixels at 2x.
	md, ok := spliceTables(pg, 2, []image.Rectangle{image.Rect(10, 10, 180, 60)})
	if !ok || !strings.Contains(md, "| A | B |") || !strings.Contains(md, "| 1 | 2 |") {
		t.Errorf("ok=%v md=%q", ok, md)
	}
}

func TestSpliceTablesKeepsTextWhenNoGrid(t *testing.T) {
	pg := bboxPage{Blocks: []bboxBlock{{Lines: []bboxLine{
		{Words: []bboxWord{word(10, 10, 80, 20, "single")}},
	}}}}
	if md, ok := spliceTables(pg, 1, []image.Rectangle{image.Rect(0, 0, 100, 100)}); ok {
		t.Errorf("a one-cell region is not a table, got %q", md)
	}
	if _, ok := spliceTables(pg, 1, nil); ok {
		t.Error("no table boxes, nothing to splice")
	}
}

func TestParseBBox(t *testing.T) {
	const x = `<!DOCTYPE html><html xmlns="http://www.w3.org/1999/xhtml"><head><title>t</title></head><body><doc>
<page width="100.5" height="200"><flow><block><line>
<word xMin="1" yMin="2" xMax="3" yMax="4">R&amp;D</word><word xMin="5" yMin="2" xMax="9" yMax="4">ok</word>
</line></block></flow></page></doc></body></html>`
	pg, err := parseBBox([]byte(x))
	if err != nil {
		t.Fatal(err)
	}
	if pg.Width != 100.5 || len(pg.Blocks) != 1 || pg.Blocks[0].Lines[0].text() != "R&D ok" {
		t.Errorf("parsed %+v", pg)
	}
	if _, err := parseBBox([]byte("<html><body><doc></doc></body></html>")); err == nil {
		t.Error("a document without pages must be an error")
	}
}
