package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deepHTML returns HTML nested depth levels deep (one <div> per level).
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

// The x/net/html parser refuses nesting deeper than 512 open elements (it
// returns an error; older versions panicked). Either way the refusal must be
// ErrCorruptInput (HTTP 422), not an untyped error (HTTP 500).
func TestHTMLDeeperThan512IsCorruptInput(t *testing.T) {
	for _, depth := range []int{513, 600, 2000} {
		_, err := ExtractHTML(strings.NewReader(deepHTML(depth)))
		if !errors.Is(err, ErrCorruptInput) {
			t.Fatalf("depth %d: got %v, want ErrCorruptInput", depth, err)
		}
	}
}

func TestHTMLFileDeeperThan512IsCorruptInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deep.html")
	if err := os.WriteFile(path, []byte(deepHTML(600)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ExtractHTMLFile(path)
	if !errors.Is(err, ErrCorruptInput) {
		t.Fatalf("got %v, want ErrCorruptInput", err)
	}
}

// Nesting below the parser limit still converts (the implicit html/body
// elements count towards the 512 open elements too).
func TestHTMLBelowTheLimitStillConverts(t *testing.T) {
	md, err := ExtractHTML(strings.NewReader(deepHTML(400)))
	if err != nil {
		t.Fatalf("depth 400: %v", err)
	}
	if !strings.Contains(md, "x") {
		t.Fatalf("depth 400: content lost: %q", md)
	}
}
