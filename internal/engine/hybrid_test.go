package engine

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann-plugin-docs/internal/docparser"
)

func TestHybridAutoStartsOnlyWhenInstalled(t *testing.T) {
	t.Setenv(envONNX, "")
	t.Setenv(docparser.ModelDirEnv, t.TempDir()) // nothing installed here
	t.Setenv("GLEANN_DOCLING_VLM", "0")
	if onnxEnabled() || onnxDisabled() {
		t.Error("unset must mean automatic, neither forced on nor off")
	}
	eng := &EIFDoclingEngine{}
	eng.initHybrid()
	if eng.hybrid != nil || eng.IsAvailable() || eng.canReadScanned() {
		t.Error("with no models installed the engine has neither VLM nor OCR")
	}
	if eng.pageDPI() != 150 {
		t.Errorf("default page DPI = %d, want the VLM's 150", eng.pageDPI())
	}
}

func TestHybridExplicitOffWins(t *testing.T) {
	t.Setenv(envONNX, "0")
	if !onnxDisabled() {
		t.Fatal("GLEANN_DOCLING_ONNX=0 must disable the parser")
	}
	eng := &EIFDoclingEngine{}
	eng.initHybrid()
	if eng.hybrid != nil {
		t.Error("parser started although switched off")
	}
}

func TestVLMUsable(t *testing.T) {
	model := filepath.Join(t.TempDir(), "m.eifm")
	if err := os.WriteFile(model, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		env   string
		model string
		want  bool
	}{
		{"auto, model present", "", model, true},
		{"switched off", "0", model, false},
		{"model missing", "", filepath.Join(t.TempDir(), "none.eifm"), false},
		{"no model resolved", "", "", false},
	}
	for _, c := range cases {
		t.Setenv("GLEANN_DOCLING_VLM", c.env)
		eng := &EIFDoclingEngine{modelPath: c.model}
		if got := eng.vlmUsable(); got != c.want {
			t.Errorf("%s: vlmUsable = %v, want %v", c.name, got, c.want)
		}
	}
	// A failed lazy load is not retried page after page.
	t.Setenv("GLEANN_DOCLING_VLM", "")
	eng := &EIFDoclingEngine{modelPath: model, vlmFailed: true}
	if eng.vlmUsable() {
		t.Error("vlmUsable must be false after a failed load")
	}
}

func TestVLMPageBudget(t *testing.T) {
	for in, want := range map[string]int{"": defaultVLMPageBudget, "5": 5, "0": 0, "-1": defaultVLMPageBudget, "x": defaultVLMPageBudget} {
		t.Setenv("GLEANN_DOCLING_VLM_MAX_PAGES", in)
		if got := vlmPageBudget(); got != want {
			t.Errorf("budget(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestHybridDPI(t *testing.T) {
	for in, want := range map[string]int{"": 150, "300": 300, "abc": 150, "10": 150, "9999": 150, "72": 72} {
		t.Setenv(envDPI, in)
		if got := hybridDPI(); got != want {
			t.Errorf("%s=%q: got %d, want %d", envDPI, in, got, want)
		}
	}
}

func TestInitHybridWithMissingModelsIsNotFatal(t *testing.T) {
	t.Setenv(envONNX, "1")
	t.Setenv(envOffline, "1")
	t.Setenv(docparser.ModelDirEnv, t.TempDir()) // empty: nothing to load, nothing to download
	eng := &EIFDoclingEngine{}
	eng.initHybrid()
	if eng.hybrid != nil {
		t.Fatal("hybrid parser should be absent when its models are missing")
	}
	if eng.canReadScanned() {
		t.Error("without models and VLM, scanned pages cannot be read")
	}
}

func TestInitHybridRejectsUnknownProfile(t *testing.T) {
	t.Setenv(envONNX, "1")
	t.Setenv(envOffline, "1")
	t.Setenv(envProfile, "klingon")
	eng := &EIFDoclingEngine{}
	eng.initHybrid()
	if eng.hybrid != nil {
		t.Error("an unknown profile must not start a parser")
	}
}

func TestDeepPrompt(t *testing.T) {
	box := docparser.BoundingBox{Type: docparser.TypeTable, NormCoords: [4]int{12, 34, 456, 478}}
	got, err := deepPrompt(box)
	if err != nil || got != "Convert table at <loc_12><loc_34><loc_456><loc_478> to OTSL." {
		t.Errorf("table prompt = %q, %v", got, err)
	}
	box.Type = docparser.TypeFormula
	if got, _ := deepPrompt(box); !strings.HasSuffix(got, "to latex.") {
		t.Errorf("formula prompt = %q", got)
	}
	box.Type = docparser.TypeText
	if _, err := deepPrompt(box); err == nil {
		t.Error("text blocks are not for the deep path")
	}
}

// engineWithHybrid builds an engine with the ONNX parser, skipping the test when
// models, the ONNX Runtime library or poppler are not installed.
func engineWithHybrid(t *testing.T) *EIFDoclingEngine {
	t.Helper()
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm not installed")
	}
	if _, err := docparser.FindORTLibrary(); err != nil {
		t.Skipf("onnxruntime not available: %v", err)
	}
	for _, m := range docparser.DefaultModels(docparser.DefaultProfile) {
		if docparser.VerifyModel(docparser.ModelDir(), m) != nil {
			t.Skipf("model %s not installed in %s", m.File, docparser.ModelDir())
		}
	}
	t.Setenv(envONNX, "1")
	t.Setenv(envOffline, "1")
	t.Setenv("GLEANN_DOCLING_VLM", "0") // test the OCR path, not whatever VLM is installed
	t.Setenv("GLEANN_DOCLING_FORCE_VLM", "")
	eng, err := NewEIFDoclingEngine("", "")
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	if eng.hybrid == nil {
		t.Fatal("hybrid parser did not start")
	}
	return eng
}

const fixturePage = "../docparser/testdata/page.png"

func TestConvertImageWithHybrid(t *testing.T) {
	eng := engineWithHybrid(t)
	md, pages, err := eng.Convert(context.Background(), fixturePage)
	if err != nil {
		t.Fatal(err)
	}
	if pages != 1 || !strings.Contains(md, "Quarterly Inventory Report") || !strings.Contains(md, "| Bolt | 120 | 0.35 |") {
		t.Errorf("pages=%d markdown:\n%s", pages, md)
	}
	if !eng.IsAvailable() {
		t.Error("an engine with the ONNX parser should report itself available")
	}
}

func TestConvertScannedPDFWithHybrid(t *testing.T) {
	eng := engineWithHybrid(t)
	pdf := filepath.Join(t.TempDir(), "scanned.pdf")
	writeImagePDF(t, pdf, fixturePage)

	// It has no text layer, so without OCR it would fail.
	if out, _ := exec.Command("pdftotext", pdf, "-").Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("test PDF unexpectedly has a text layer: %q", out)
	}

	md, pages, err := eng.Convert(context.Background(), pdf)
	if err != nil {
		t.Fatal(err)
	}
	if pages != 1 || !strings.Contains(md, "Quarterly Inventory Report") || !strings.Contains(md, "warehouse received") {
		t.Errorf("pages=%d markdown:\n%s", pages, md)
	}
}

func TestScannedPDFWithoutOCRStillExplainsItself(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm not installed")
	}
	t.Setenv(envONNX, "0")
	t.Setenv("GLEANN_DOCLING_VLM", "0")
	t.Setenv("GLEANN_DOCLING_FORCE_VLM", "")
	pdf := filepath.Join(t.TempDir(), "scanned.pdf")
	writeImagePDF(t, pdf, fixturePage)

	eng, err := NewEIFDoclingEngine("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	_, _, err = eng.Convert(context.Background(), pdf)
	if err == nil || !strings.Contains(err.Error(), "GLEANN_DOCLING_ONNX=1") {
		t.Errorf("error should name the switch that fixes it, got %v", err)
	}
}

// writeImagePDF writes a one-page PDF whose only content is the image: a
// "scanned" document without a text layer.
func writeImagePDF(t *testing.T, path, imagePath string) {
	t.Helper()
	f, err := os.Open(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	pageW, pageH := float64(w)*72/150, float64(h)*72/150 // the fixture is A4 at 150 dpi

	var buf bytes.Buffer
	var offsets []int
	obj := func(body string) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	buf.WriteString("%PDF-1.4\n")
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj("<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>", pageW, pageH))
	content := fmt.Sprintf("q %.2f 0 0 %.2f 0 0 cm /Im0 Do Q", pageW, pageH)
	obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	offsets = append(offsets, buf.Len())
	fmt.Fprintf(&buf, "5 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", w, h, jpg.Len())
	buf.Write(jpg.Bytes())
	buf.WriteString("\nendstream\nendobj\n")

	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A digital PDF page with a table: the text stays exact (no OCR errors), and the
// table becomes Markdown because the layout model finds it. Without the parser
// the same file yields flat text.
func TestConvertDigitalPDFTableWithHybrid(t *testing.T) {
	eng := engineWithHybrid(t)
	const pdf = "../../testdata/documents/benchmark_report.pdf"
	md, _, err := eng.Convert(context.Background(), pdf)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"| Metric | Description | Target |",
		"| Section Detection | Known headings found | >= 90% |",
		"MarkItDown", // exact text layer, not OCR ("MarkltDown")
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
	if strings.Index(md, "2.2 Metrics Definition") > strings.Index(md, "| Metric |") {
		t.Error("table must come after its heading")
	}

	eng.Close()
	t.Setenv(envONNX, "0")
	flat, err := NewEIFDoclingEngine("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer flat.Close()
	md, _, err = flat.Convert(context.Background(), pdf)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(md, "| --- |") {
		t.Error("without the ONNX parser pages keep the plain text layer")
	}
}
