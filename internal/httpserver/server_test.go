package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann-plugin-docs/internal/config"
)

type mockEngine struct {
	available bool
	name      string
}

func (m *mockEngine) Convert(ctx context.Context, filePath string) (string, int, error) {
	if strings.HasSuffix(filePath, ".txt") {
		return "Mock document title\n\nSome paragraph text here.", 1, nil
	}
	return "# Mock Title\n\nMocked conversion content.", 1, nil
}

func (m *mockEngine) IsAvailable() bool {
	return m.available
}

func (m *mockEngine) BackendName() string {
	return m.name
}

func (m *mockEngine) Close() error {
	return nil
}

func TestHealthEndpoint(t *testing.T) {
	srv := New(&mockEngine{available: true, name: "docling-eif"}, 8765)
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	srv.handleHealth(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var data map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}

	if data["status"] != "ok" {
		t.Errorf("expected status ok, got %v", data["status"])
	}
	if data["plugin"] != PluginName {
		t.Errorf("expected plugin name %s, got %v", PluginName, data["plugin"])
	}
	backends, ok := data["backends"].(map[string]any)
	if !ok || backends["docling-eif"] != true {
		t.Errorf("expected docling-eif backend to be true")
	}
}

func TestInfoEndpoint(t *testing.T) {
	srv := New(&mockEngine{available: true, name: "docling-eif"}, 8765)
	req := httptest.NewRequest("GET", "/info", nil)
	w := httptest.NewRecorder()

	srv.handleInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var data map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}

	if data["name"] != PluginName {
		t.Errorf("expected %s, got %v", PluginName, data["name"])
	}
	if int(data["schema_version"].(float64)) != 2 {
		t.Errorf("expected schema_version 2, got %v", data["schema_version"])
	}
}

func TestConvertEndpoint_Text(t *testing.T) {
	srv := New(&mockEngine{available: true, name: "docling-eif"}, 8765)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "sample.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("Hello world from unit test"))
	_ = writer.Close()

	req := httptest.NewRequest("POST", "/convert", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	srv.handleConvert(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var res map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}

	nodes, ok := res["nodes"].([]any)
	if !ok || len(nodes) == 0 {
		t.Errorf("expected nodes in response, got %v", res)
	}
	if res["backend"] != "docling-eif" {
		t.Errorf("expected docling-eif backend, got %v", res["backend"])
	}
}

func TestConvertEndpoint_UnsupportedExtension(t *testing.T) {
	srv := New(&mockEngine{available: true, name: "docling-eif"}, 8765)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "test.xyz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("raw data"))
	_ = writer.Close()

	req := httptest.NewRequest("POST", "/convert", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	srv.handleConvert(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", w.Code)
	}
}

func TestConvertEndpoint_MissingFile(t *testing.T) {
	srv := New(&mockEngine{available: true, name: "docling-eif"}, 8765)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.Close()

	req := httptest.NewRequest("POST", "/convert", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	srv.handleConvert(w, req)
	if w.Code != 422 {
		t.Fatalf("expected 422 Unprocessable Entity, got %d", w.Code)
	}
}

func TestConvertEndpoint_HTML(t *testing.T) {
	srv := New(&mockEngine{available: true, name: "docling-eif"}, 8765)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "document.html")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("<h1>Hello HTML</h1><p>Test paragraph with https://example.com link.</p>"))
	_ = writer.Close()

	req := httptest.NewRequest("POST", "/convert", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	srv.handleConvert(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var res map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res["backend"] != "docling-eif" {
		t.Errorf("expected docling-eif backend, got %v", res["backend"])
	}
}


func TestSettingsEndpoint(t *testing.T) {
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "docs.json"))
	ts := httptest.NewServer(New(nil, 0).Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/settings")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET /settings: %v %v", resp, err)
	}
	var form struct {
		Revision string
		Fields   []struct{ Key string }
	}
	_ = json.NewDecoder(resp.Body).Decode(&form)
	resp.Body.Close()
	if len(form.Fields) != len(config.Settings().Fields) || form.Revision == "" {
		t.Fatalf("form: %+v", form)
	}

	body := strings.NewReader(`{"values":{"engine":"paddle","dpi":200,"vlm":true,"deep_tables":true},"revision":"` + form.Revision + `"}`)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/settings", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("PUT /settings: %v %v", resp, err)
	}
	resp.Body.Close()
	if c := config.Load(); c.Engine != "paddle" || c.DPI != 200 || !c.Deep || !c.Completed {
		t.Errorf("saved config: %+v", c)
	}

	// A request addressed to another host name (DNS rebinding) is refused.
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/settings", nil)
	req.Host = "evil.example:8765"
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("foreign Host: %v %v", resp, err)
	}
}
