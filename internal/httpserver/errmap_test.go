package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/tevfik/gleann-plugin-docs/internal/engine"
)

type failEngine struct {
	mockEngine
	err error
}

func (f *failEngine) Convert(context.Context, string) (string, int, error) { return "", 0, f.err }

func TestConvertErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"corrupt", fmt.Errorf("x: %w", engine.ErrCorruptInput), http.StatusUnprocessableEntity},
		{"backend", fmt.Errorf("x: %w", engine.ErrBackendUnavailable), http.StatusServiceUnavailable},
		{"too large", fmt.Errorf("x: %w", engine.ErrTooLarge), http.StatusRequestEntityTooLarge},
		{"image too large", engine.ErrImageTooLarge, http.StatusRequestEntityTooLarge},
		{"deadline", fmt.Errorf("x: %w", context.DeadlineExceeded), http.StatusGatewayTimeout},
		{"other", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		srv := New(&failEngine{err: c.err}, 8765)
		if rec := post(t, srv, "a.txt", []byte("x")); rec.Code != c.want {
			t.Errorf("%s: got %d, want %d", c.name, rec.Code, c.want)
		}
	}
	// A client that went away gets no JSON error body.
	srv := New(&failEngine{err: context.Canceled}, 8765)
	if rec := post(t, srv, "a.txt", []byte("x")); rec.Body.Len() != 0 {
		t.Errorf("cancelled request got a body: %q", rec.Body.String())
	}
}

func TestNilEngineIs503(t *testing.T) {
	srv := New(nil, 8765)
	if rec := post(t, srv, "a.txt", []byte("x")); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
}

func TestTruncatedDocxIs422(t *testing.T) {
	srv := newRealServer(t)
	full := zipBytes(t, map[string][]byte{"word/document.xml": []byte("<w:document/>")})
	if rec := post(t, srv, "cut.docx", full[:len(full)/2]); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestNotAnImageIs422(t *testing.T) {
	srv := newRealServer(t)
	if rec := post(t, srv, "x.png", []byte("this is not a png")); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

// Without the anydoc CLI (PATH is empty) an ODT cannot be read: 503, not 500.
func TestODTWithoutAnydocIs503(t *testing.T) {
	srv := newRealServer(t)
	odt := zipBytes(t, map[string][]byte{"content.xml": []byte("<office:document-content/>")})
	if rec := post(t, srv, "a.odt", odt); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503: %s", rec.Code, rec.Body.String())
	}
}

func TestLegacyDocWithoutLibreOfficeIs503(t *testing.T) {
	srv := newRealServer(t)
	if rec := post(t, srv, "a.doc", []byte("\xd0\xcf\x11\xe0 legacy")); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503: %s", rec.Code, rec.Body.String())
	}
}
