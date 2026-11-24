package httpserver

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"fmt"
	"hash/crc32"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newRealServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("GLEANN_PLUGIN_TOKEN", "")
	t.Setenv("GLEANN_DOCS_TOKEN", "")
	return New(isolatedEngine(t), 8765)
}

// A header that declares 40000x40000 pixels must be refused from the header
// alone, long before the decoder would allocate 6.4 GB.
func TestHugeImageIs413Quickly(t *testing.T) {
	srv := newRealServer(t)
	t.Setenv("GLEANN_DOCS_MAX_PIXELS", "100000000") // 100 MP: the 40000x40000 header is far above, so no test machine ever allocates it
	start := time.Now()
	w := post(t, srv, "bomb.png", bigPNG(40000, 40000))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want 413: %s", w.Code, w.Body.String())
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v, want under 2s", d)
	}
}

func TestHugeImageBudgetFromEnv(t *testing.T) {
	srv := newRealServer(t)
	t.Setenv("GLEANN_DOCS_MAX_PIXELS", "1000")
	if w := post(t, srv, "a.png", bigPNG(100, 100)); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("10000 pixels over a 1000 pixel budget: got %d, want 413: %s", w.Code, w.Body.String())
	}
}

func TestZipDeclaringHugeMemberIs413(t *testing.T) {
	srv := newRealServer(t)
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name:               "word/document.xml",
		Method:             zip.Deflate,
		CompressedSize64:   4,
		UncompressedSize64: 3 << 30, // declares 3 GiB
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte{1, 2, 3, 4})
	_ = zw.Close()
	if rec := post(t, srv, "lie.docx", b.Bytes()); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

// A real deflate bomb: 50 MB of zeros compress to about 50 KB.
func TestDeflateBombIs413(t *testing.T) {
	srv := newRealServer(t)
	t.Setenv("GLEANN_DOCS_ZIP_MAX_ENTRY_BYTES", fmt.Sprint(10<<20))
	zeros := make([]byte, 50<<20)
	var comp bytes.Buffer
	fw, _ := flate.NewWriter(&comp, flate.BestCompression)
	_, _ = fw.Write(zeros)
	_ = fw.Close()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name: "word/document.xml", Method: zip.Deflate,
		CRC32: crc32.ChecksumIEEE(zeros), CompressedSize64: uint64(comp.Len()), UncompressedSize64: uint64(len(zeros)),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(comp.Bytes())
	_ = zw.Close()
	if rec := post(t, srv, "bomb.docx", b.Bytes()); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

func TestZipWithTooManyEntriesIs413(t *testing.T) {
	srv := newRealServer(t)
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for i := 0; i < 20000; i++ {
		w, _ := zw.Create(fmt.Sprintf("f/%d.txt", i))
		_, _ = w.Write([]byte("x"))
	}
	_ = zw.Close()
	if rec := post(t, srv, "many.xlsx", b.Bytes()); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

func TestRealFixturesStillConvert(t *testing.T) {
	srv := newRealServer(t)
	for _, name := range []string{"technical_report.docx", "benchmark_data.xlsx", "research_slides.pptx", "distributed_systems.epub"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "documents", name))
		if err != nil {
			t.Skip("testdata missing")
		}
		if rec := post(t, srv, name, data); rec.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200: %.200s", name, rec.Code, rec.Body.String())
		}
	}
}

// bigEngine returns n bytes of Markdown.
type bigEngine struct {
	mockEngine
	n int
}

func (b *bigEngine) Convert(_ context.Context, _ string) (string, int, error) {
	return strings.Repeat("a", b.n), 1, nil
}

func TestMarkdownOverCapIs413(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_MARKDOWN_BYTES", "")
	srv := New(&bigEngine{n: 65 << 20}, 8765)
	if rec := post(t, srv, "a.txt", []byte("x")); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("65 MiB result: got %d, want 413", rec.Code)
	}
	t.Setenv("GLEANN_DOCS_MAX_MARKDOWN_BYTES", "100")
	srv = New(&bigEngine{n: 101}, 8765)
	if rec := post(t, srv, "a.txt", []byte("x")); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("101 bytes over a 100 byte cap: got %d, want 413", rec.Code)
	}
	srv = New(&bigEngine{n: 100}, 8765)
	if rec := post(t, srv, "a.txt", []byte("x")); rec.Code != http.StatusOK {
		t.Fatalf("exactly at the cap: got %d, want 200", rec.Code)
	}
}
