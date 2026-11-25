package engine

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docxWithHeading builds a minimal .docx whose single paragraph carries the
// given pStyle value and, optionally, a styles.xml mapping style ids to names.
func docxWithHeading(t *testing.T, pStyle string, styles map[string]string) string {
	t.Helper()
	doc := `<w:document><w:body><w:p><w:pPr><w:pStyle w:val="` + pStyle + `"/></w:pPr>` +
		`<w:r><w:t>Deep heading</w:t></w:r></w:p></w:body></w:document>`
	files := map[string]string{"word/document.xml": doc}
	if styles != nil {
		var sb strings.Builder
		sb.WriteString(`<w:styles>`)
		for id, name := range styles {
			sb.WriteString(`<w:style w:styleId="` + id + `"><w:name w:val="` + name + `"/></w:style>`)
		}
		sb.WriteString(`</w:styles>`)
		files["word/styles.xml"] = sb.String()
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "heading.docx")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Markdown has six heading levels. A Word style beyond level 6 (Heading7,
// Heading9, a localized id, or outlineLvl 6..8) must clamp to ###### instead
// of emitting ####### or more.
func TestDocxHeadingBeyond6ClampsToSix(t *testing.T) {
	cases := []struct {
		name   string
		pStyle string
		styles map[string]string
	}{
		{"style id Heading9", "Heading9", nil},
		{"style id Heading7", "Heading7", nil},
		{"localized id Balk9", "Balk9", nil},
		{"style name heading 9", "Custom9", map[string]string{"Custom9": "heading 9"}},
		{"style name heading 7", "Custom7", map[string]string{"Custom7": "heading 7"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			md, err := ExtractDocx(docxWithHeading(t, c.pStyle, c.styles))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(md, "#######") {
				t.Fatalf("invalid 7+ level heading: %q", md)
			}
			if !strings.Contains(md, "###### Deep heading") {
				t.Fatalf("want clamped ###### heading, got %q", md)
			}
		})
	}
}

func TestDocxOutlineLvlBeyond6ClampsToSix(t *testing.T) {
	for _, lvl := range []string{"6", "7", "8"} {
		doc := `<w:document><w:body><w:p><w:pPr><w:outlineLvl w:val="` + lvl + `"/></w:pPr>` +
			`<w:r><w:t>Deep heading</w:t></w:r></w:p></w:body></w:document>`
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("word/document.xml")
		_, _ = w.Write([]byte(doc))
		_ = zw.Close()
		path := filepath.Join(t.TempDir(), "outline.docx")
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		md, err := ExtractDocx(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(md, "#######") || !strings.Contains(md, "###### Deep heading") {
			t.Fatalf("outlineLvl %s: want ###### heading, got %q", lvl, md)
		}
	}
}

// Levels 1..6 keep their exact level.
func TestDocxHeadingLevels1To6Unchanged(t *testing.T) {
	for lvl := 1; lvl <= 6; lvl++ {
		style := "Heading" + string(rune('0'+lvl))
		md, err := ExtractDocx(docxWithHeading(t, style, nil))
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Repeat("#", lvl) + " Deep heading"
		if !strings.Contains(md, want) || strings.Contains(md, "#"+want) {
			t.Fatalf("level %d: want %q, got %q", lvl, want, md)
		}
	}
}
