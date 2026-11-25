package httpserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/tevfik/gleann-plugin-docs/internal/config"
)

func authedRequest(t *testing.T, method, path, token, host string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func status(h http.Handler, req *http.Request) int {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code
}

func TestTokenGuardsEveryRouteButHealth(t *testing.T) {
	t.Setenv(config.ConfigEnv, t.TempDir()+"/docs.json")
	t.Setenv("GLEANN_PLUGIN_TOKEN", "s3cret")
	t.Setenv("GLEANN_DOCS_TOKEN", "")
	srv := New(&mockEngine{available: true, name: "docling-eif"}, 8765)
	h := srv.Handler()

	for _, path := range []string{"/info", "/settings", "/models", "/convert"} {
		method := http.MethodGet
		if path == "/convert" {
			method = http.MethodPost
		}
		if got := status(h, authedRequest(t, method, path, "", "127.0.0.1:8765")); got != http.StatusUnauthorized {
			t.Errorf("%s without a token: got %d, want 401", path, got)
		}
		if got := status(h, authedRequest(t, method, path, "wrong", "127.0.0.1:8765")); got != http.StatusUnauthorized {
			t.Errorf("%s with a wrong token: got %d, want 401", path, got)
		}
	}
	if got := status(h, authedRequest(t, "GET", "/info", "s3cret", "127.0.0.1:8765")); got != http.StatusOK {
		t.Errorf("/info with the token: got %d, want 200", got)
	}
	if got := status(h, authedRequest(t, "GET", "/settings", "s3cret", "127.0.0.1:8765")); got != http.StatusOK {
		t.Errorf("/settings with the token: got %d, want 200", got)
	}
	// /convert with the token reaches the handler (a body-less POST is 4xx, not 401).
	if got := status(h, authedRequest(t, "POST", "/convert", "s3cret", "127.0.0.1:8765")); got == http.StatusUnauthorized {
		t.Errorf("/convert with the token still 401")
	}
	if got := status(h, authedRequest(t, "GET", "/health", "", "127.0.0.1:8765")); got != http.StatusOK {
		t.Errorf("/health without a token: got %d, want 200", got)
	}
}

func TestBothTokensAccepted(t *testing.T) {
	t.Setenv("GLEANN_PLUGIN_TOKEN", "host-token")
	t.Setenv("GLEANN_DOCS_TOKEN", "")
	srv := New(&mockEngine{available: true, name: "docling-eif"}, 8765)
	srv.SetToken("flag-token")
	h := srv.Handler()
	for _, tok := range []string{"host-token", "flag-token"} {
		if got := status(h, authedRequest(t, "GET", "/info", tok, "localhost:8765")); got != http.StatusOK {
			t.Errorf("token %q: got %d, want 200", tok, got)
		}
	}
	if got := status(h, authedRequest(t, "GET", "/info", "other", "localhost:8765")); got != http.StatusUnauthorized {
		t.Errorf("unknown token: got %d, want 401", got)
	}
}

func TestLoopbackHostRequired(t *testing.T) {
	t.Setenv("GLEANN_PLUGIN_TOKEN", "s3cret")
	srv := New(&mockEngine{available: true, name: "docling-eif"}, 8765)
	h := srv.Handler()
	if got := status(h, authedRequest(t, "GET", "/info", "s3cret", "evil.example")); got != http.StatusForbidden {
		t.Errorf("foreign Host with a valid token: got %d, want 403", got)
	}
	for _, host := range []string{"127.0.0.1:8765", "localhost:8765", "[::1]:8765"} {
		if got := status(h, authedRequest(t, "GET", "/info", "s3cret", host)); got != http.StatusOK {
			t.Errorf("Host %s: got %d, want 200", host, got)
		}
	}
}

func TestNoTokenKeepsOldBehaviour(t *testing.T) {
	t.Setenv("GLEANN_PLUGIN_TOKEN", "")
	t.Setenv("GLEANN_DOCS_TOKEN", "")
	srv := New(&mockEngine{available: true, name: "docling-eif"}, 8765)
	h := srv.Handler()
	if got := status(h, authedRequest(t, "GET", "/info", "", "127.0.0.1:8765")); got != http.StatusOK {
		t.Errorf("/info without any token configured: got %d, want 200", got)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, uploadRequest(t, "a.txt", []byte("hello")))
	if w.Code != http.StatusOK {
		t.Errorf("/convert without any token configured: got %d: %s", w.Code, w.Body.String())
	}
}

func TestServeSweepsOnlyItsOwnTempDir(t *testing.T) {
	tmp := t.TempDir()
	_ = os.Chmod(tmp, 0o700) // the sweep refuses a group-writable root
	t.Setenv("TMPDIR", tmp)
	port := freePort(t)
	own := engineInstanceDir(port)
	mustWrite(t, own+"/gleann_upload_stale.docx")
	mustWrite(t, tmp+"/gleann_upload_foreign") // the gleann server's own upload name
	mustWrite(t, tmp+"/gleann-img-old.png")
	old := time.Now().Add(-5 * time.Hour)
	_ = osChtimes(tmp+"/gleann-img-old.png", old)
	mustWrite(t, tmp+"/gleann-img-fresh.png")

	s := New(nil, port)
	go func() { _ = s.Serve() }()
	waitListening(t, port)
	defer s.Close()

	// Serve binds the port before it prepares the temp directory and runs the
	// sweep, so a successful dial does not mean the sweep has finished: wait
	// for its (last) effect instead of racing it.
	for i := 0; i < 500 && exists(tmp+"/gleann-img-old.png"); i++ {
		time.Sleep(10 * time.Millisecond)
	}

	if exists(own + "/gleann_upload_stale.docx") {
		t.Error("a leftover in this instance's temp directory survived the startup sweep")
	}
	if !exists(tmp + "/gleann_upload_foreign") {
		t.Error("gleann_upload_* in the system temp directory must not be swept")
	}
	if exists(tmp + "/gleann-img-old.png") {
		t.Error("a stale gleann-img-* of an earlier version was not removed")
	}
	if !exists(tmp + "/gleann-img-fresh.png") {
		t.Error("a fresh gleann-img-* must be kept")
	}
}
