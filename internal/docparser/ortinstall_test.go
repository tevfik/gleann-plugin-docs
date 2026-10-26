package docparser

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeORTArchive(t *testing.T, member string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "pkg/include/x.h", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.WriteHeader(&tar.Header{Name: member, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write(body)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestInstallORTVerifiesAndInstalls(t *testing.T) {
	lib := []byte("not really a shared object")
	arc := fakeORTArchive(t, "pkg/lib/libonnxruntime.so.9.9.9", lib)
	sum := sha256.Sum256(arc)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/ort.tgz") {
			http.NotFound(w, r)
			return
		}
		w.Write(arc)
	}))
	defer srv.Close()

	rel := ortRelease{Asset: "ort.tgz", SHA256: hex.EncodeToString(sum[:]), Lib: "pkg/lib/libonnxruntime.so.9.9.9", Name: "libonnxruntime.so.9.9.9"}
	dir := t.TempDir()
	got, err := installORT(context.Background(), srv.Client(), srv.URL, rel, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(got)
	if !bytes.Equal(b, lib) {
		t.Fatalf("installed %q", b)
	}
	if target, err := os.Readlink(filepath.Join(dir, "libonnxruntime.so")); err != nil || target != rel.Name {
		t.Fatalf("symlink %q %v", target, err)
	}

	// A tampered archive must be refused and leave nothing behind.
	rel.SHA256 = strings.Repeat("0", 64)
	dir2 := t.TempDir()
	if _, err := installORT(context.Background(), srv.Client(), srv.URL, rel, dir2, nil); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("expected a checksum error, got %v", err)
	}
	if entries, _ := os.ReadDir(dir2); len(entries) != 0 {
		t.Fatalf("left files behind: %v", entries)
	}
}

func TestORTTooOld(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "libonnxruntime.so.1.21.0")
	if err := os.WriteFile(old, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "libonnxruntime.so")
	if err := os.Symlink(old, link); err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		old:                                  true,
		link:                                 true, // judged by its target
		"/x/libonnxruntime.so.1.24.0":        false,
		"/x/libonnxruntime.so." + ORTVersion: false,
		"/x/libonnxruntime.1.20.1.dylib":     true,
		"/x/libonnxruntime.so":               false, // no version: trust it
		"/x/onnxruntime.dll":                 false,
	}
	for p, want := range cases {
		if got := ortTooOld(p); got != want {
			t.Errorf("ortTooOld(%s) = %v, want %v", p, got, want)
		}
	}
}
