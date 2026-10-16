package docparser

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestParser builds a parser from the models in $GLEANN_DOCPARSER_MODELS or
// ~/.gleann/models/docparser and skips the test when they, or the ONNX
// Runtime library, are not installed (`gleann-plugin-docs models download`).
func newTestParser(t *testing.T, profile string) *Parser {
	t.Helper()
	if _, err := FindORTLibrary(); err != nil {
		t.Skipf("onnxruntime not available: %v", err)
	}
	p, err := ProfileByName(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range DefaultModels(p) {
		if VerifyModel(ModelDir(), m) != nil {
			t.Skipf("model %s not installed in %s", m.File, ModelDir())
		}
	}
	parser, err := New(Options{Profile: profile})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { parser.Close() })
	return parser
}

func loadFixture(t *testing.T) image.Image {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "page.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestParsePageSyntheticLatin(t *testing.T) {
	p := newTestParser(t, "latin")
	blocks, timings, err := p.ParsePageBlocks(context.Background(), loadFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, b := range blocks {
		sb.WriteString(b.Markdown + "\n\n")
	}
	md := sb.String()
	t.Logf("%d blocks, %d lines, layout=%v det=%v rec=%v total=%v\n%s",
		timings.Blocks, timings.Lines, timings.Layout, timings.Det, timings.Rec, timings.Total, md)

	for _, want := range []string{
		"Quarterly Inventory Report", "warehouse received three shipments",
		"Deliveries were on time",
	} {
		if !strings.Contains(NormalizeText(md), NormalizeText(want)) {
			t.Errorf("output lacks %q", want)
		}
	}

	// Turkish letters the Chinese/English model has no class for: ç ğ ö ş ü.
	for _, want := range []string{"Çok güzel", "şehirde", "Öğretmenler", "güneşli", "hava bugün"} {
		if !strings.Contains(md, want) {
			t.Errorf("output lacks Turkish text %q", want)
		}
	}
	// Known limitation, logged and not asserted: the recogniser often confuses
	// dotless ı / dotted İ with i / I ("yaşıyoruz" -> "yaşiyoruz", "İstanbul" -> "Istanbul").
	for _, word := range []string{"yaşıyoruz", "İstanbul", "ığdır"} {
		t.Logf("dotted/dotless check %-10s exact=%v", word, strings.Contains(md, word))
	}

	// Reading order: title first, table last.
	if i, j := strings.Index(md, "Quarterly"), strings.Index(md, "Bracket"); i < 0 || j < 0 || i > j {
		t.Errorf("title should precede the table (title@%d, table@%d)", i, j)
	}
	// The table is rebuilt as a Markdown table with its cells in their columns.
	if !strings.Contains(md, "| --- |") {
		t.Errorf("no Markdown table in output")
	}
	for _, cell := range []string{"Bolt", "Washer", "Bracket", "120", "300"} {
		if !strings.Contains(md, cell) {
			t.Errorf("table cell %q missing", cell)
		}
	}
}

func TestParsePageRespectsCancelledContext(t *testing.T) {
	p := newTestParser(t, "latin")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := p.ParsePageBlocks(ctx, loadFixture(t)); err == nil {
		t.Error("expected an error for a cancelled context")
	}
}

func TestParsePageIsDeterministicUnderConcurrency(t *testing.T) {
	p := newTestParser(t, "latin")
	img := loadFixture(t)
	first, err := p.ParsePage(context.Background(), img)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 4)
	for i := 0; i < cap(done); i++ {
		go func() {
			md, err := p.ParsePage(context.Background(), img)
			if err != nil {
				md = "ERROR: " + err.Error()
			}
			done <- md
		}()
	}
	for i := 0; i < cap(done); i++ {
		if got := <-done; got != first {
			t.Errorf("concurrent run differs from the first:\n%s\n--- vs ---\n%s", got, first)
		}
	}
}

func TestParseBlankPage(t *testing.T) {
	p := newTestParser(t, "latin")
	blank := image.NewRGBA(image.Rect(0, 0, 800, 1000))
	for i := range blank.Pix {
		blank.Pix[i] = 255
	}
	md, err := p.ParsePage(context.Background(), blank)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(md) != "" {
		t.Errorf("a blank page should give no text, got %q", md)
	}
}
