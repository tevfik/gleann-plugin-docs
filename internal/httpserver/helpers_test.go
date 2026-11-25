package httpserver

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tevfik/gleann-plugin-docs/internal/engine"
)

// multipartBody builds the multipart form body of one file upload.
func multipartBody(name string, data []byte) (*bytes.Buffer, string) {
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		panic(err) // bytes.Buffer writes cannot fail
	}
	_, _ = part.Write(data)
	_ = w.Close()
	return body, w.FormDataContentType()
}

// uploadRequest builds a multipart POST /convert request for one file.
func uploadRequest(t *testing.T, name string, data []byte) *http.Request {
	t.Helper()
	body, contentType := multipartBody(name, data)
	req := httptest.NewRequest("POST", "/convert", body)
	req.Host = "127.0.0.1:8765"
	req.Header.Set("Content-Type", contentType)
	return req
}

// bigPNG returns a PNG whose header declares w x h pixels but whose data is a
// few bytes: decoding it in full would allocate w*h*4 bytes.
func bigPNG(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte("\x89PNG\r\n\x1a\n"))
	chunk := func(kind string, data []byte) {
		_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
		b.WriteString(kind)
		b.Write(data)
		crc := crc32.NewIEEE()
		crc.Write([]byte(kind))
		crc.Write(data)
		_ = binary.Write(&b, binary.BigEndian, crc.Sum32())
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA
	chunk("IHDR", ihdr)
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	_, _ = zw.Write([]byte{0, 0, 0, 0, 0})
	_ = zw.Close()
	chunk("IDAT", z.Bytes())
	chunk("IEND", nil)
	return b.Bytes()
}

// zipBytes builds a zip archive from name/content pairs.
func zipBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// isolatedEngine is the real engine with no VLM or OCR and no external tools,
// and HOME, TMPDIR and PATH pointing at empty directories.
func isolatedEngine(t *testing.T) *engine.EIFDoclingEngine {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GLEANN_SOFFICE", filepath.Join(home, "none"))
	t.Setenv("GLEANN_DOCLING_VLM", "0")
	t.Setenv("GLEANN_DOCLING_ONNX", "0")
	eng := &engine.EIFDoclingEngine{}
	t.Cleanup(func() { _ = eng.Close() })
	return eng
}

// post sends the upload through the full handler chain and returns the recorder.
func post(t *testing.T, srv *Server, name string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, uploadRequest(t, name, data))
	return w
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitListening(t *testing.T, port int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server did not start listening")
}

func engineInstanceDir(port int) string { return engine.InstanceTempDir(port) }

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func osChtimes(path string, t time.Time) error { return os.Chtimes(path, t, t) }

// loopbackReq is a body-less request addressed to the loopback host the gleann
// host uses.
func loopbackReq(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Host = "127.0.0.1:8765"
	return req
}
