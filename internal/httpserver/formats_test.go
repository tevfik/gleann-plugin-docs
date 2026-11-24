package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// engineExts is every extension EIFDoclingEngine.Convert handles
// (internal/engine/docling_eif.go). The server must accept all of them.
var engineExts = []string{
	".pdf", ".docx", ".doc", ".xlsx", ".xls", ".pptx", ".ppt", ".epub",
	".html", ".htm", ".png", ".jpg", ".jpeg", ".tif", ".tiff", ".bmp", ".webp",
	".csv", ".txt", ".md", ".odt", ".ods", ".odp", ".rtf",
}

func TestSupportedExtsListEngineFormats(t *testing.T) {
	for _, ext := range engineExts {
		if !isExtensionSupported(ext) {
			t.Errorf("%s is converted by the engine but refused by the server", ext)
		}
	}
}

func TestConvertEndpoint_OpenDocumentAndRTF(t *testing.T) {
	for _, name := range []string{"a.odt", "a.ods", "a.odp", "a.rtf"} {
		srv := New(&mockEngine{available: true, name: "docling-eif"}, 8765)
		w := httptest.NewRecorder()
		srv.handleConvert(w, uploadRequest(t, name, []byte("data")))
		if w.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d: %s", name, w.Code, w.Body.String())
		}
	}
}
