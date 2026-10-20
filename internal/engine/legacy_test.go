package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeLegacy turns a testdata OOXML file into .doc/.xls/.ppt with LibreOffice.
func makeLegacy(t *testing.T, src, target string) string {
	t.Helper()
	soffice, err := findSoffice()
	if err != nil {
		t.Skip("LibreOffice not installed")
	}
	dir := t.TempDir()
	cmd := exec.Command(soffice, "-env:UserInstallation=file://"+filepath.Join(dir, "p"),
		"--headless", "--convert-to", target, "--outdir", dir, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build legacy fixture: %v %s", err, out)
	}
	return filepath.Join(dir, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))+"."+target)
}

func TestLegacyOfficeMatchesOOXML(t *testing.T) {
	docs := filepath.Join("..", "..", "testdata", "documents")
	eng := &EIFDoclingEngine{}
	for _, c := range []struct{ src, legacy string }{
		{"technical_report.docx", "doc"},
		{"benchmark_data.xlsx", "xls"},
		{"research_slides.pptx", "ppt"},
	} {
		src := filepath.Join(docs, c.src)
		if _, err := os.Stat(src); err != nil {
			t.Skip("testdata missing")
		}
		legacy := makeLegacy(t, src, c.legacy)
		want, _, err := eng.Convert(context.Background(), src)
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := eng.Convert(context.Background(), legacy)
		if err != nil {
			t.Fatalf("%s: %v", c.legacy, err)
		}
		if len(strings.TrimSpace(got)) < 50 {
			t.Fatalf("%s: almost empty output: %q", c.legacy, got)
		}
		// Round-tripping through LibreOffice may reformat; the words must survive.
		missing := 0
		words := strings.Fields(want)
		for _, w := range words {
			if len(w) > 5 && !strings.Contains(got, w) {
				missing++
			}
		}
		if missing > len(words)/10 {
			t.Errorf("%s: %d of %d long words lost", c.legacy, missing, len(words))
		}
	}
}

func TestLegacyOfficeWithoutLibreOfficeSaysWhatToDo(t *testing.T) {
	t.Setenv("GLEANN_SOFFICE", filepath.Join(t.TempDir(), "nope"))
	f := filepath.Join(t.TempDir(), "x.doc")
	os.WriteFile(f, []byte("x"), 0o644)
	_, _, err := (&EIFDoclingEngine{}).Convert(context.Background(), f)
	if err == nil || !strings.Contains(err.Error(), "GLEANN_SOFFICE") {
		t.Fatalf("want an actionable error, got %v", err)
	}
}
