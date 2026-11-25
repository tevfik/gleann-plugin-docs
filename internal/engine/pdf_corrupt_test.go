package engine

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// corruptPDFs returns damaged PDFs: a real one cut short, and garbage after a
// valid header.
func corruptPDFs(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	full, err := os.ReadFile(filepath.Join("..", "..", "testdata", "documents", "academic_paper.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	garbage := []byte("%PDF-1.4\n")
	for i := 0; i < 600; i++ {
		garbage = append(garbage, byte(i*37+11), byte(i*91+5), '\n')
	}
	out := map[string]string{}
	for name, data := range map[string][]byte{
		"truncated.pdf":  full[:3000],
		"garbage.pdf":    garbage,
		"headeronly.pdf": []byte("%PDF-1.7\n"),
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		out[name] = p
	}
	return out
}

// A damaged PDF is the client's corrupt input (422), not a server fault, and
// the message does not reveal where the upload lives.
func TestConvertCorruptPDFIsCorruptInput(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	t.Setenv("GLEANN_DOCLING_VLM", "")
	t.Setenv("GLEANN_DOCLING_FORCE_VLM", "")
	for name, p := range corruptPDFs(t) {
		_, _, err := (&EIFDoclingEngine{}).Convert(t.Context(), p)
		if !errors.Is(err, ErrCorruptInput) {
			t.Errorf("%s: got %v, want ErrCorruptInput", name, err)
			continue
		}
		if strings.Contains(err.Error(), filepath.Dir(p)) {
			t.Errorf("%s: error leaks the temp path: %v", name, err)
		}
	}
}

// A cancelled or timed-out pdftotext is not a damaged file.
func TestConvertPDFCancelledIsNotCorrupt(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for name, p := range corruptPDFs(t) {
		_, _, err := (&EIFDoclingEngine{}).Convert(ctx, p)
		if err == nil || errors.Is(err, ErrCorruptInput) {
			t.Errorf("%s: cancelled conversion answered %v", name, err)
		}
	}
}

func exitErr(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("no exit error: %v", err)
	}
	return err
}

// Exit 1 is damaged input only when the spooled file is there and readable;
// a missing or unreadable file is the server's problem (500, no path).
func TestPDFParseErrorExit1NeedsReadableFile(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "ok.pdf")
	if err := os.WriteFile(ok, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pdfParseError(t.Context(), ok, exitErr(t, 1)); !errors.Is(err, ErrCorruptInput) {
		t.Errorf("readable file: %v, want ErrCorruptInput", err)
	}
	missing := filepath.Join(dir, "gone.pdf")
	err := pdfParseError(t.Context(), missing, exitErr(t, 1))
	if err == nil || errors.Is(err, ErrCorruptInput) || strings.Contains(err.Error(), dir) {
		t.Errorf("missing file: %v, want a non-corrupt error without a path", err)
	}
	locked := filepath.Join(dir, "locked.pdf")
	if err := os.WriteFile(locked, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	if f, oerr := os.Open(locked); oerr == nil { // running as root
		f.Close()
	} else if err := pdfParseError(t.Context(), locked, exitErr(t, 1)); err == nil || errors.Is(err, ErrCorruptInput) {
		t.Errorf("unreadable file: %v, want a non-corrupt error", err)
	}
}

type fakeOCR struct{}

func (fakeOCR) ParsePage(context.Context, image.Image) (string, error) {
	return "text read by OCR from the rendered page", nil
}
func (fakeOCR) TableBoxes(image.Image) ([]image.Rectangle, error) { return nil, nil }
func (fakeOCR) Close() error                                      { return nil }

// A copy-restricted PDF (pdftotext exit 3) is read by OCR when it is available,
// and is 422 only when it is not.
func TestRestrictedPDFFallsBackToOCR(t *testing.T) {
	bin := t.TempDir()
	png := filepath.Join(bin, "page.png")
	var buf bytes.Buffer
	if err := pngEncode(&buf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(png, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	script := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script("pdftotext", "echo 'Permission Error: Copying of text from this document is not allowed.' >&2; exit 3")
	script("pdftoppm", `for last; do :; done; cp "`+png+`" "${last}-1.png"`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GLEANN_DOCLING_VLM", "0")
	t.Setenv("GLEANN_DOCLING_FORCE_VLM", "")
	pdf := filepath.Join(t.TempDir(), "r.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	md, _, err := (&EIFDoclingEngine{hybrid: fakeOCR{}}).Convert(t.Context(), pdf)
	if err != nil || !strings.Contains(md, "read by OCR") {
		t.Fatalf("with OCR: %q, %v; want the OCR text", md, err)
	}
	_, _, err = (&EIFDoclingEngine{}).Convert(t.Context(), pdf)
	if !errors.Is(err, ErrCorruptInput) || strings.Contains(err.Error(), filepath.Dir(pdf)) {
		t.Fatalf("without OCR: %v, want ErrCorruptInput without a path", err)
	}
}

func pngEncode(w io.Writer) error {
	return png.Encode(w, image.NewGray(image.Rect(0, 0, 8, 8)))
}
