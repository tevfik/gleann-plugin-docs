package httpserver

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"
)

func healthOf(t *testing.T, srv *Server) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, loopbackReq("GET", "/health"))
	var data map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestHealthReportsRealBackends(t *testing.T) {
	srv := newRealServer(t) // empty PATH: no pdftotext, pdftoppm, soffice, anydoc; no VLM, no OCR
	data := healthOf(t, srv)
	backends, _ := data["backends"].(map[string]any)
	if _, stale := backends["markitdown"]; stale {
		t.Error("/health still claims markitdown")
	}
	if _, dup := backends["bitllm.c"]; dup {
		t.Error("/health still reports the duplicate bitllm.c entry")
	}
	for _, name := range []string{"docling-vlm", "onnx-docparser", "pdftotext", "pdftoppm", "libreoffice", "anydoc"} {
		v, ok := backends[name]
		if !ok {
			t.Errorf("backend %q missing from /health", name)
		} else if v != false {
			t.Errorf("backend %q = %v, want false in an empty environment", name, v)
		}
	}
	missing, _ := data["missing"].([]any)
	var got []string
	for _, m := range missing {
		got = append(got, m.(string))
	}
	for _, want := range []string{".pdf", ".odt", ".doc"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing = %v, want it to list %s", got, want)
		}
	}
	if data["status"] != "ok" {
		t.Errorf("status = %v, want ok (the host treats any answer as healthy)", data["status"])
	}
}
