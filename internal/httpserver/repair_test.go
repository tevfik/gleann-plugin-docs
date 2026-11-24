package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/tevfik/gleann-plugin-docs/internal/engine"
)

// F5: /health is subject to the loopback Host check, but needs no token.
func TestHealthRequiresLoopbackHostButNoToken(t *testing.T) {
	t.Setenv("GLEANN_PLUGIN_TOKEN", "host-token")
	srv := New(&mockEngine{available: true, name: "docling-eif"}, 8765)
	h := srv.Handler()
	for host, want := range map[string]int{
		"127.0.0.1:8765": http.StatusOK,
		"localhost:8765": http.StatusOK,
		"[::1]:8765":     http.StatusOK,
		"evil.example":   http.StatusForbidden,
	} {
		req := httptest.NewRequest("GET", "/health", nil)
		req.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != want {
			t.Errorf("/health with Host %q: got %d, want %d", host, w.Code, want)
		}
	}
}

// F4: a cancelled or timed-out conversion maps to 503/504 even when the
// engine also reports the child's exit status.
func TestCancelledConversionIsNeither422Nor500(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"cancelled child", fmt.Errorf("pdftotext: %w: %w", context.Canceled, engine.ErrCorruptInput), http.StatusServiceUnavailable},
		{"timed out child", fmt.Errorf("soffice: %w: %w", context.DeadlineExceeded, engine.ErrCorruptInput), http.StatusGatewayTimeout},
	}
	for _, c := range cases {
		srv := New(&failEngine{err: c.err}, 8765)
		rec := post(t, srv, "a.txt", []byte("x"))
		if rec.Code != c.want {
			t.Errorf("%s: got %d, want %d", c.name, rec.Code, c.want)
		}
	}
	// the cancelled case answers with no body
	srv := New(&failEngine{err: cases[0].err}, 8765)
	if rec := post(t, srv, "a.txt", []byte("x")); rec.Body.Len() != 0 {
		t.Errorf("cancelled request got a body: %q", rec.Body.String())
	}
}

// F3: a symlinked temp root is neither reset nor swept.
func TestPrepareTempRefusesSymlinkedRootAndSkipsSweep(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	victim := filepath.Join(t.TempDir(), "victim")
	port := 8765
	keep := filepath.Join(victim, strconv.Itoa(port), "keep")
	mustWrite(t, keep)
	stale := filepath.Join(tmp, "gleann-img-old.png")
	mustWrite(t, stale)
	if err := osChtimes(stale, time.Now().Add(-5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(engineInstanceDir(port))
	if err := os.Symlink(victim, root); err != nil {
		t.Skip("no symlinks:", err)
	}
	srv := New(&mockEngine{}, port)
	srv.prepareTemp()
	if !exists(keep) {
		t.Error("a file behind the symlinked temp root was deleted")
	}
	if srv.tempDir != "" {
		t.Errorf("tempDir = %q, want the system default after a refusal", srv.tempDir)
	}
	if !exists(stale) {
		t.Error("the legacy sweep ran although the temp root was refused")
	}
}
