package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildPDF writes a minimal PDF; pageText "" yields a page without a text layer.
// pdftotext rebuilds the xref table itself, so offsets are not computed.
func buildPDF(t *testing.T, pageTexts ...string) string {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("%PDF-1.4\n")
	kids := make([]string, len(pageTexts))
	for i := range pageTexts {
		kids[i] = fmt.Sprintf("%d 0 R", 4+i*2)
	}
	sb.WriteString("1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n")
	fmt.Fprintf(&sb, "2 0 obj<</Type/Pages/Count %d/Kids[%s]>>endobj\n", len(pageTexts), strings.Join(kids, " "))
	sb.WriteString("3 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj\n")
	for i, txt := range pageTexts {
		content := ""
		if txt != "" {
			content = fmt.Sprintf("BT /F1 12 Tf 72 700 Td (%s) Tj ET", txt)
		}
		fmt.Fprintf(&sb, "%d 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Resources<</Font<</F1 3 0 R>>>>/Contents %d 0 R>>endobj\n", 4+i*2, 5+i*2)
		fmt.Fprintf(&sb, "%d 0 obj<</Length %d>>stream\n%s\nendstream endobj\n", 5+i*2, len(content), content)
	}
	sb.WriteString("trailer<</Root 1 0 R>>\n%%EOF\n")
	p := filepath.Join(t.TempDir(), "t.pdf")
	if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConvertPDF_PerPageRouting(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	t.Setenv("GLEANN_DOCLING_VLM", "")
	t.Setenv("GLEANN_DOCLING_FORCE_VLM", "")
	eng := &EIFDoclingEngine{} // VLM not loaded
	ctx := context.Background()

	// Mixed PDF: text pages are kept, the page without a text layer is skipped,
	// and the real page count is reported.
	p := buildPDF(t, "First digital page with enough text", "", "Third digital page also has text")
	md, pages, err := eng.Convert(ctx, p)
	if err != nil {
		t.Fatalf("mixed pdf: %v", err)
	}
	if pages != 3 {
		t.Errorf("pages = %d, want 3", pages)
	}
	if !strings.Contains(md, "First digital page") || !strings.Contains(md, "Third digital page") {
		t.Errorf("text pages missing:\n%s", md)
	}

	// Fully scanned PDF without VLM is a clear error, not an empty success.
	_, _, err = eng.Convert(ctx, buildPDF(t, "", ""))
	if err == nil || !strings.Contains(err.Error(), "GLEANN_DOCLING_VLM") {
		t.Errorf("expected actionable error for scanned pdf, got %v", err)
	}
}

func TestNewEngine_VLMOptIn(t *testing.T) {
	t.Setenv("GLEANN_DOCLING_VLM", "")
	t.Setenv("GLEANN_DOCLING_FORCE_VLM", "")
	eng, err := NewEIFDoclingEngine("/nonexistent.eifm", "")
	if err != nil {
		t.Fatalf("VLM off must not try to load a model: %v", err)
	}
	if eng.IsAvailable() {
		t.Error("model must not be loaded when VLM is disabled")
	}
	if _, _, err := eng.Convert(context.Background(), "x.png"); err == nil || !strings.Contains(err.Error(), "GLEANN_DOCLING_VLM") {
		t.Errorf("image without VLM should explain how to enable it, got %v", err)
	}
}
