package parser

import (
	"strings"
	"testing"
)

func TestOTSL_PlainTableIsMarkdown(t *testing.T) {
	in := `<otsl><loc_10><loc_20><loc_300><loc_400><fcel>Name<fcel>Qty<nl><fcel>Bolt<fcel>5<nl><fcel>Nut<ecel><nl></otsl>`
	got := DocTagsToMarkdown(in)
	want := "| Name | Qty |\n| --- | --- |\n| Bolt | 5 |\n| Nut |  |"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestOTSL_ColumnSpanBecomesHTML(t *testing.T) {
	in := `<otsl><fcel>Total<lcel><lcel><nl><fcel>A<fcel>B<fcel>C<nl></otsl>`
	got := DocTagsToMarkdown(in)
	if !strings.Contains(got, `<th colspan="3">Total</th>`) {
		t.Errorf("missing colspan header in:\n%s", got)
	}
	if strings.Count(got, "<td>") != 3 {
		t.Errorf("want 3 body cells, got:\n%s", got)
	}
	if strings.Contains(got, "|") {
		t.Errorf("a table with spans must not be a pipe table:\n%s", got)
	}
}

func TestOTSL_RowAndCrossSpan(t *testing.T) {
	// "X" covers 2 columns and 2 rows; the cells it hides are lcel, ucel, xcel.
	in := `<otsl><fcel>X<lcel><fcel>Y<nl><ucel><xcel><fcel>Z<nl><fcel>p<fcel>q<fcel>r<nl></otsl>`
	got := DocTagsToMarkdown(in)
	if !strings.Contains(got, `colspan="2" rowspan="2"`) {
		t.Errorf("want a 2x2 cell in:\n%s", got)
	}
	// Row 1 has only Z (the other two columns are covered), row 2 has three cells.
	rows := strings.Split(got, "</tr>")
	if !strings.Contains(rows[1], "Z") || strings.Contains(rows[1], ">X<") {
		t.Errorf("second row should hold only Z: %q", rows[1])
	}
}

func TestOTSL_HeaderTokens(t *testing.T) {
	in := `<otsl><ched>H1<ched>H2<nl><rhed>R<lcel><nl></otsl>`
	got := DocTagsToMarkdown(in)
	if !strings.Contains(got, "<th>H1</th>") || !strings.Contains(got, `<th colspan="2">R</th>`) {
		t.Errorf("header cells not marked:\n%s", got)
	}
}

func TestOTSL_EscapesAndCaption(t *testing.T) {
	in := `<otsl><caption>Table 1: a|b</caption><fcel>x|y<fcel><b>bold</b><nl></otsl>`
	got := DocTagsToMarkdown(in)
	if !strings.Contains(got, "*Table 1: a|b*") {
		t.Errorf("caption missing:\n%s", got)
	}
	if !strings.Contains(got, `| x\|y | bold |`) {
		t.Errorf("cell not escaped or tags not stripped:\n%s", got)
	}

	html := DocTagsToMarkdown(`<otsl><fcel>1 < 2 & 3<lcel><nl><fcel>a<fcel>b<nl></otsl>`)
	if !strings.Contains(html, "1 &lt; 2 &amp; 3") {
		t.Errorf("HTML cell not escaped:\n%s", html)
	}
}

func TestOTSL_RaggedRowsArePadded(t *testing.T) {
	grid, _ := parseOTSL(`<fcel>a<fcel>b<fcel>c<nl><fcel>d<nl>`)
	if len(grid) != 2 || len(grid[1]) != 3 || grid[1][2].kind != otslEmpty {
		t.Errorf("ragged row not padded: %+v", grid)
	}
}

func TestOTSL_TextBeforeAndAfterTable(t *testing.T) {
	in := `<doctag><text>Intro</text><otsl><fcel>a<fcel>b<nl></otsl><text>Outro</text></doctag>`
	got := DocTagsToMarkdown(in)
	i, j, k := strings.Index(got, "Intro"), strings.Index(got, "| a | b |"), strings.Index(got, "Outro")
	if i < 0 || j < 0 || k < 0 || !(i < j && j < k) {
		t.Errorf("wrong order or content:\n%s", got)
	}
}

func TestOTSL_HTMLRowTableStillWorks(t *testing.T) {
	in := `<table><trow><fcel>a</fcel><fcel>b</fcel></trow><trow><fcel>c</fcel><fcel>d</fcel></trow></table>`
	got := DocTagsToMarkdown(in)
	if !strings.Contains(got, "| a | b |") || !strings.Contains(got, "| c | d |") {
		t.Errorf("legacy row format broke:\n%s", got)
	}
}
