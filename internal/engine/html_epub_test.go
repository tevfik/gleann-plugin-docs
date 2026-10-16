package engine

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractHTML(t *testing.T) {
	htmlInput := `
<!DOCTYPE html>
<html>
<head>
    <title>Test Page</title>
    <style>body { font-size: 14px; }</style>
    <script>console.log("ignore me");</script>
</head>
<body>
    <h1>Main Title</h1>
    <p>This is a <strong>bold</strong> and <em>italic</em> paragraph with <code>inline code</code>.</p>
    <h2>Subheading</h2>
    <p>Here is a link: <a href="https://gleann.ai">Gleann AI</a></p>
    <ul>
        <li>First item</li>
        <li>Second item</li>
    </ul>
    <ol>
        <li>Step one</li>
        <li>Step two</li>
    </ol>
    <pre><code>func main() {
    println("hello")
}</code></pre>
    <blockquote>
        A wise quote goes here.
    </blockquote>
    <table>
        <thead>
            <tr><th>Header 1</th><th>Header 2</th></tr>
        </thead>
        <tbody>
            <tr><td>Cell 1</td><td>Cell 2</td></tr>
            <tr><td>Cell 3</td><td>Cell 4</td></tr>
        </tbody>
    </table>
</body>
</html>
`
	md, err := ExtractHTML(strings.NewReader(htmlInput))
	if err != nil {
		t.Fatalf("ExtractHTML failed: %v", err)
	}

	if !strings.Contains(md, "# Main Title") {
		t.Errorf("Expected '# Main Title', got:\n%s", md)
	}
	if !strings.Contains(md, "## Subheading") {
		t.Errorf("Expected '## Subheading', got:\n%s", md)
	}
	if !strings.Contains(md, "**bold**") || !strings.Contains(md, "*italic*") {
		t.Errorf("Expected bold/italic formatting, got:\n%s", md)
	}
	if !strings.Contains(md, "[Gleann AI](https://gleann.ai)") {
		t.Errorf("Expected markdown link, got:\n%s", md)
	}
	if !strings.Contains(md, "- First item") || !strings.Contains(md, "1. Step one") {
		t.Errorf("Expected list items, got:\n%s", md)
	}
	if !strings.Contains(md, "> A wise quote goes here.") {
		t.Errorf("Expected blockquote, got:\n%s", md)
	}
	if !strings.Contains(md, "| Header 1 | Header 2 |") || !strings.Contains(md, "| Cell 1 | Cell 2 |") {
		t.Errorf("Expected markdown table, got:\n%s", md)
	}
	if strings.Contains(md, "ignore me") || strings.Contains(md, "font-size") {
		t.Errorf("Expected script and style to be stripped, got:\n%s", md)
	}
}

func TestExtractEPUB(t *testing.T) {
	tmpDir := t.TempDir()
	epubFile := filepath.Join(tmpDir, "test.epub")

	// Create a synthetic EPUB file
	f, err := os.Create(epubFile)
	if err != nil {
		t.Fatalf("create test epub: %v", err)
	}

	zw := zip.NewWriter(f)

	// 1. META-INF/container.xml
	cw, err := zw.Create("META-INF/container.xml")
	if err != nil {
		t.Fatalf("create container.xml: %v", err)
	}
	cw.Write([]byte(`<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`))

	// 2. OEBPS/content.opf
	ow, err := zw.Create("OEBPS/content.opf")
	if err != nil {
		t.Fatalf("create content.opf: %v", err)
	}
	ow.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="ch1"/>
    <itemref idref="ch2"/>
  </spine>
</package>`))

	// 3. OEBPS/ch1.xhtml
	ch1w, err := zw.Create("OEBPS/ch1.xhtml")
	if err != nil {
		t.Fatalf("create ch1.xhtml: %v", err)
	}
	ch1w.Write([]byte(`<!DOCTYPE html><html><body><h1>Chapter 1</h1><p>First chapter content.</p></body></html>`))

	// 4. OEBPS/ch2.xhtml
	ch2w, err := zw.Create("OEBPS/ch2.xhtml")
	if err != nil {
		t.Fatalf("create ch2.xhtml: %v", err)
	}
	ch2w.Write([]byte(`<!DOCTYPE html><html><body><h1>Chapter 2</h1><p>Second chapter content.</p></body></html>`))

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	_ = f.Close()

	// Now extract
	md, chapters, err := ExtractEPUB(epubFile)
	if err != nil {
		t.Fatalf("ExtractEPUB failed: %v", err)
	}

	if chapters != 2 {
		t.Errorf("Expected 2 chapters, got %d", chapters)
	}
	if !strings.Contains(md, "# Chapter 1") || !strings.Contains(md, "First chapter content.") {
		t.Errorf("Expected Chapter 1 content, got:\n%s", md)
	}
	if !strings.Contains(md, "# Chapter 2") || !strings.Contains(md, "Second chapter content.") {
		t.Errorf("Expected Chapter 2 content, got:\n%s", md)
	}
	if !strings.Contains(md, "---") {
		t.Errorf("Expected separator between chapters, got:\n%s", md)
	}
}
