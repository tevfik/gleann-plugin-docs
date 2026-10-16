package docparser

import (
	"image"
	"strings"
	"testing"
)

func tl(text string, x1, y1, x2, y2 int) TextLine {
	return TextLine{Text: text, Rect: image.Rect(x1, y1, x2, y2), Conf: 0.9}
}

func lines(texts ...string) []TextLine {
	out := make([]TextLine, len(texts))
	for i, s := range texts {
		out[i] = tl(s, 0, i*30, 400, i*30+25)
	}
	return out
}

func TestParagraphText(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"joins with spaces", []string{"The quick", "brown fox"}, "The quick brown fox"},
		{"undoes hyphenation before a lower-case word", []string{"the interna-", "tional system"}, "the international system"},
		{"keeps a hyphen before a capital", []string{"the Anglo-", "Saxon era"}, "the Anglo- Saxon era"},
		{"keeps a spaced dash", []string{"range 5 -", "10 units"}, "range 5 - 10 units"},
		{"skips blanks", []string{"", "  one ", "  "}, "one"},
		{"empty", nil, ""},
	}
	for _, c := range cases {
		if got := paragraphText(lines(c.in...)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestListText(t *testing.T) {
	got := listText(lines("• first item", "continues here", "- second", "3. third", "[12] Brown R 2019", "of a paper"))
	want := strings.Join([]string{
		"- first item continues here",
		"- second",
		"3. third",
		"- [12] Brown R 2019 of a paper",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	// A bullet glued to its text (common in OCR output) still starts an item.
	if got := listText(lines("•glued", "•also")); got != "- glued\n- also" {
		t.Errorf("glued bullets: %q", got)
	}
	// A lone line with no marker still becomes an item.
	if got := listText(lines("plain")); got != "- plain" {
		t.Errorf("plain: %q", got)
	}
	// A minus sign in front of a number is not a bullet.
	if got := listText(lines("-5 degrees")); got != "- -5 degrees" {
		t.Errorf("negative number: %q", got)
	}
}

func TestLooksLikeList(t *testing.T) {
	if !looksLikeList(lines("[1] a", "[2] b", "continued")) {
		t.Error("reference list not recognised")
	}
	if !looksLikeList(lines("• a", "• b")) {
		t.Error("bullets not recognised")
	}
	if looksLikeList(lines("Just a normal", "paragraph of text", "with three lines")) {
		t.Error("prose taken for a list")
	}
	if looksLikeList(lines("[1] single")) {
		t.Error("one line is not a list")
	}
}

func TestGridMarkdown(t *testing.T) {
	// 3 rows x 3 columns, with slightly irregular positions.
	in := []TextLine{
		tl("Item", 10, 10, 90, 40), tl("Qty", 200, 12, 260, 42), tl("Price", 400, 10, 470, 40),
		tl("Bolt", 10, 60, 80, 90), tl("120", 205, 62, 255, 92), tl("0.35", 405, 60, 465, 90),
		tl("Washer", 12, 110, 110, 140), tl("300", 203, 112, 253, 142), tl("0.10", 402, 110, 462, 140),
	}
	got := gridMarkdown(in)
	want := "| Item | Qty | Price |\n| --- | --- | --- |\n| Bolt | 120 | 0.35 |\n| Washer | 300 | 0.10 |"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestGridMarkdownMergesWordsOfOneCell(t *testing.T) {
	in := []TextLine{
		tl("Name", 10, 10, 80, 40), tl("Total cost", 300, 10, 380, 40), tl("EUR", 392, 10, 440, 40),
		tl("A", 10, 60, 30, 90), tl("5", 300, 60, 320, 90),
		tl("B", 10, 110, 30, 140), tl("7", 300, 110, 320, 140),
	}
	got := gridMarkdown(in)
	if !strings.Contains(got, "| Name | Total cost EUR |") {
		t.Errorf("two nearby boxes should form one cell:\n%s", got)
	}
}

func TestGridMarkdownRejectsNonTables(t *testing.T) {
	// One column of text.
	col := []TextLine{tl("a", 10, 10, 300, 40), tl("b", 10, 60, 300, 90), tl("c", 10, 110, 300, 140), tl("d", 10, 160, 300, 190)}
	if got := gridMarkdown(col); got != "" {
		t.Errorf("single column: %q", got)
	}
	// One row.
	row := []TextLine{tl("a", 10, 10, 60, 40), tl("b", 200, 10, 260, 40), tl("c", 400, 10, 460, 40), tl("d", 600, 10, 660, 40)}
	if got := gridMarkdown(row); got != "" {
		t.Errorf("single row: %q", got)
	}
	if got := gridMarkdown(nil); got != "" {
		t.Errorf("empty: %q", got)
	}
}

func TestGridMarkdownEscapesPipes(t *testing.T) {
	in := []TextLine{
		tl("a|b", 10, 10, 80, 40), tl("c", 300, 10, 340, 40),
		tl("d", 10, 60, 40, 90), tl("e", 300, 60, 340, 90),
	}
	if got := gridMarkdown(in); !strings.Contains(got, `a\|b`) {
		t.Errorf("pipe not escaped: %s", got)
	}
}

func TestPlainLines(t *testing.T) {
	if got := plainLines(lines("a", " ", "b")); got != "a\nb" {
		t.Errorf("got %q", got)
	}
}

func TestDeepMarkdown(t *testing.T) {
	if got := deepMarkdown(TypeFormula, "<formula> E = mc^2 </formula>"); got != "$$E = mc^2$$" {
		t.Errorf("formula: %q", got)
	}
	if got := deepMarkdown(TypeFormula, "<formula></formula>"); got != "" {
		t.Errorf("empty formula: %q", got)
	}
	got := deepMarkdown(TypeTable, "<otsl><fcel>a<fcel>b<nl><fcel>c<fcel>d<nl></otsl>")
	if !strings.Contains(got, "| a | b |") || !strings.Contains(got, "| c | d |") {
		t.Errorf("table: %q", got)
	}
}
