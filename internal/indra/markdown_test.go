package indra

import (
	"context"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// INDRA_PAGES also holds <page>.png.md, the Python pipeline's Markdown for the
// same page (language model and script routing off).
func TestMarkdownParityWithPython(t *testing.T) {
	dir, pages := os.Getenv("INDRA_MODELS"), os.Getenv("INDRA_PAGES")
	if dir == "" || pages == "" {
		t.Skip("INDRA_MODELS / INDRA_PAGES not set")
	}
	p, err := NewParser(dir, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	files, _ := filepath.Glob(filepath.Join(pages, "*.png"))
	for _, f := range files {
		raw, err := os.ReadFile(f + ".md")
		if err != nil {
			continue
		}
		fh, _ := os.Open(f)
		img, err := png.Decode(fh)
		fh.Close()
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.ParsePage(context.Background(), img)
		if err != nil {
			t.Fatal(err)
		}
		want := string(raw)
		ga, wa := strings.Split(got, "\n"), strings.Split(want, "\n")
		same := 0
		for i := range ga {
			if i < len(wa) && ga[i] == wa[i] {
				same++
			}
		}
		t.Logf("%s: %d/%d markdown lines identical to python (go %d lines, python %d)", filepath.Base(f), same, len(wa), len(ga), len(wa))
		if os.Getenv("INDRA_DUMP") != "" {
			os.WriteFile(f+".go.md", []byte(got), 0o644)
		}
		if float64(same) < 0.9*float64(len(wa)) {
			t.Errorf("%s: markdown differs from python", filepath.Base(f))
		}
	}
}

func TestDumpLines(t *testing.T) {
	dir, pages := os.Getenv("INDRA_MODELS"), os.Getenv("INDRA_PAGES")
	if dir == "" || pages == "" || os.Getenv("INDRA_DUMP") == "" {
		t.Skip()
	}
	p, err := NewParser(dir, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	files, _ := filepath.Glob(filepath.Join(pages, "*.png"))
	for _, f := range files {
		fh, _ := os.Open(f)
		img, _ := png.Decode(fh)
		fh.Close()
		pg, err := p.read(img)
		if err != nil {
			t.Fatal(err)
		}
		var out [][]any
		for _, i := range pg.layout.Order {
			l := pg.lines[i]
			out = append(out, []any{[]float64{l.box[0], l.box[1], l.box[2], l.box[3]}, l.text})
		}
		b, _ := json.Marshal(out)
		os.WriteFile(f+".golines.json", b, 0o644)
	}
}
