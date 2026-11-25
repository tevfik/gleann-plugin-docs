package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A failing anydoc CLI is corrupt input (422), and its error must not carry
// the temporary upload path.
func TestAnyDocFailureIsCorruptInputWithoutTempPath(t *testing.T) {
	if HasInProcessAnyDoc {
		t.Skip("built with in-process anydoc; the CLI path is not compiled")
	}
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "anydoc")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir()) // no ~/.cargo/bin/anydoc either

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("content.xml")
	_, _ = w.Write([]byte("<office:document-content/>"))
	_ = zw.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.odt")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := &EIFDoclingEngine{}
	t.Cleanup(func() { _ = eng.Close() })
	_, _, err := eng.Convert(context.Background(), path)
	if !errors.Is(err, ErrCorruptInput) {
		t.Fatalf("got %v, want ErrCorruptInput", err)
	}
	if strings.Contains(err.Error(), dir) {
		t.Fatalf("error leaks the temp path %q: %v", dir, err)
	}
}
