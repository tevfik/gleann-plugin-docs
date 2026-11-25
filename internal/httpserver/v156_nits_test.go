package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann-plugin-docs/internal/engine"
)

// deepHTML is HTML nested depth levels deep; above 512 open elements the
// x/net/html parser panics, which must surface as 422, not 500.
func deepHTML(depth int) string {
	var b strings.Builder
	for i := 0; i < depth; i++ {
		b.WriteString("<div>")
	}
	b.WriteString("x")
	for i := 0; i < depth; i++ {
		b.WriteString("</div>")
	}
	return b.String()
}

func TestDeepHTMLIs422(t *testing.T) {
	srv := newRealServer(t)
	rec := post(t, srv, "deep.html", []byte(deepHTML(600)))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestDocxHeadingBeyond6ClampedOverHTTP(t *testing.T) {
	srv := newRealServer(t)
	doc := `<w:document><w:body><w:p><w:pPr><w:pStyle w:val="Heading9"/></w:pPr>` +
		`<w:r><w:t>Deep heading</w:t></w:r></w:p></w:body></w:document>`
	data := zipBytes(t, map[string][]byte{"word/document.xml": []byte(doc)})
	rec := post(t, srv, "h.docx", data)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "#######") {
		t.Fatalf("invalid 7+ level heading in response: %s", body)
	}
	if !strings.Contains(body, "###### Deep heading") {
		t.Fatalf("want clamped ###### heading: %s", body)
	}
}

// pathLeakEngine reproduces the anydoc error shape: the message embeds the
// full temporary upload path it was handed.
type pathLeakEngine struct {
	mockEngine
}

func (e *pathLeakEngine) Convert(_ context.Context, path string) (string, int, error) {
	return "", 0, fmt.Errorf("anydoc conversion failed for %s", path)
}

// setEngineTempDir points the engine at dir for the duration of the test.
func setEngineTempDir(t *testing.T, dir string) {
	t.Helper()
	old := engine.TempDir()
	engine.SetTempDir(dir)
	t.Cleanup(func() { engine.SetTempDir(old) })
}

func TestConvertErrorMasksTempPath(t *testing.T) {
	dir := t.TempDir()
	setEngineTempDir(t, dir)
	srv := New(&pathLeakEngine{}, 8765)
	rec := post(t, srv, "a.odt", []byte("anything"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500 (untyped engine error): %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, dir) {
		t.Fatalf("response leaks the temp dir %q: %s", dir, body)
	}
	if !strings.Contains(body, "anydoc conversion failed") {
		t.Fatalf("error text lost: %s", body)
	}
}

// A failing CreateTemp must not reveal the temp directory either.
func TestCreateTempErrorMasksTempPath(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	setEngineTempDir(t, filepath.Join(file, "sub")) // MkdirAll/CreateTemp cannot work here
	srv := New(&mockEngine{}, 8765)
	rec := post(t, srv, "a.txt", []byte("x"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, file) {
		t.Fatalf("response leaks the temp path %q: %s", file, body)
	}
}

// The multipart parser spools to the system temp dir; its errors are masked
// too (writeError is the single choke point).
func TestWriteErrorMasksSystemTempDir(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "parse multipart: open "+os.TempDir()+"/multipart-123: no such file")
	var data map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	msg, _ := data["error"].(string)
	if strings.Contains(msg, os.TempDir()) {
		t.Fatalf("error leaks the system temp dir: %s", msg)
	}
}
