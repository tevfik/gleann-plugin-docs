package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClassOf(t *testing.T) {
	for ext, want := range map[string]string{
		".pdf": classHeavy, ".png": classHeavy, ".jpg": classHeavy, ".tiff": classHeavy, ".webp": classHeavy,
		".doc": classHeavy, ".ppt": classHeavy, ".odt": classHeavy, ".rtf": classHeavy,
		".xls": classHeavy, ".docx": classLight, ".xlsx": classLight, ".pptx": classLight,
		".html": classLight, ".htm": classLight, ".epub": classLight, ".txt": classLight, ".md": classLight, ".csv": classLight,
	} {
		if got := classOf(ext, false); got != want {
			t.Errorf("classOf(%s) = %s, want %s", ext, got, want)
		}
	}
	// With OCR or the VLM on, formats with embedded pictures are heavy too.
	for ext, want := range map[string]string{
		".docx": classHeavy, ".pptx": classHeavy, ".epub": classHeavy,
		".xlsx": classLight, ".html": classLight, ".txt": classLight, ".pdf": classHeavy,
	} {
		if got := classOf(ext, true); got != want {
			t.Errorf("classOf(%s, imageText) = %s, want %s", ext, got, want)
		}
	}
	// Every supported extension is classified (unlisted ones are light by rule).
	for _, ext := range SupportedExts {
		if c := classOf(ext, false); c != classHeavy && c != classLight {
			t.Errorf("%s: class %q", ext, c)
		}
	}
}

func waitLaneQueued(t *testing.T, l *lane, n int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for l.queued.Load() != n {
		if time.Now().After(deadline) {
			t.Fatalf("%s queued = %d, want %d", l.class, l.queued.Load(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// A full heavy queue does not block light requests, and each class answers 429
// on its own when full, with its own Retry-After.
func TestClassesAreIndependent(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_HEAVY", "1")
	t.Setenv("GLEANN_DOCS_HEAVY_QUEUE", "1")
	t.Setenv("GLEANN_DOCS_MAX_LIGHT", "1")
	t.Setenv("GLEANN_DOCS_LIGHT_QUEUE", "1")
	eng := newBlockEngine()
	srv := New(eng, 8765)

	h1 := asyncPost(srv, "a.pdf")
	waitStarted(t, eng, 1)
	h2 := asyncPost(srv, "b.png") // the single heavy queue seat
	waitLaneQueued(t, srv.heavy, 1)

	// Heavy is full: 429, Retry-After from the heavy lane (3 s samples, 2 ahead).
	for i := 0; i < 3; i++ {
		srv.heavy.recordDuration(3 * time.Second)
	}
	rec := post(t, srv, "c.pdf", []byte("x"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("heavy overflow: got %d, want 429", rec.Code)
	}
	if ra, _ := strconv.Atoi(rec.Header().Get("Retry-After")); ra != 6 {
		t.Fatalf("heavy Retry-After = %q, want 6 (own queue: position 2 / 1 slot x 3 s)", rec.Header().Get("Retry-After"))
	}

	// A light request still gets a slot at once.
	l1 := asyncPost(srv, "d.txt")
	waitStarted(t, eng, 1)
	if got := srv.heavy.queued.Load(); got != 1 {
		t.Fatalf("heavy queue = %d, want 1", got)
	}
	l2 := asyncPost(srv, "e.docx") // the single light queue seat
	waitLaneQueued(t, srv.light, 1)

	rec = post(t, srv, "f.xlsx", []byte("x"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("light overflow: got %d, want 429", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra != "2" {
		t.Fatalf("light Retry-After = %q, want 2 (light has no samples: 1 s, position 2); the heavy samples must not leak in", ra)
	}

	close(eng.release)
	for i, ch := range [](<-chan *httptest.ResponseRecorder){h1, h2, l1, l2} {
		select {
		case r := <-ch:
			if r.Code != http.StatusOK {
				t.Fatalf("request %d: got %d", i, r.Code)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("request %d did not finish", i)
		}
	}
}

// Disconnected and stalled clients release the slot of their own class.
func TestHeavyDisconnectFreesHeavySeat(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_HEAVY", "1")
	t.Setenv("GLEANN_DOCS_HEAVY_QUEUE", "0")
	srv := New(&mockEngine{}, 8765)
	for i := 0; i < 2; i++ {
		if rec := post(t, srv, "a.pdf", []byte("x")); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d: seat of the previous request was not freed", i)
		}
	}
	if len(srv.heavy.admitCh) != 0 || len(srv.heavy.runCh) != 0 || len(srv.light.admitCh) != 0 {
		t.Fatalf("leaked: heavy tickets %d slots %d, light tickets %d", len(srv.heavy.admitCh), len(srv.heavy.runCh), len(srv.light.admitCh))
	}
}

// A heavy client that closes its socket while queued frees its heavy seat, and
// the light class is untouched throughout.
func TestHeavyDisconnectedWaiterFreesSeatOverTCP(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_HEAVY", "1")
	t.Setenv("GLEANN_DOCS_HEAVY_QUEUE", "1")
	eng := newBlockEngine()
	srv := New(eng, 8765)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	addr := strings.TrimPrefix(ts.URL, "http://")

	c1 := rawUpload(t, addr, "a.pdf")
	defer c1.Close()
	waitStarted(t, eng, 1)
	c2 := rawUpload(t, addr, "b.pdf")
	waitLaneQueued(t, srv.heavy, 1)
	c2.Close()

	deadline := time.Now().Add(3 * time.Second)
	for srv.heavy.queued.Load() != 0 || len(srv.heavy.admitCh) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("disconnected heavy waiter still holds its seat: queued=%d tickets=%d", srv.heavy.queued.Load(), len(srv.heavy.admitCh))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(srv.light.admitCh) != 0 {
		t.Fatalf("light tickets = %d, want 0", len(srv.light.admitCh))
	}
	close(eng.release)
}

type ocrEngine struct {
	mockEngine
	ocr bool
}

func (e *ocrEngine) ImageTextAvailable() bool { return e.ocr }

// The class of a docx follows the server's OCR/VLM configuration.
func TestClassForFollowsEngineConfig(t *testing.T) {
	off := New(&ocrEngine{}, 8765)
	on := New(&ocrEngine{ocr: true}, 8765)
	plain := New(&mockEngine{}, 8765) // engine that cannot say: light
	for _, c := range []struct {
		srv  *Server
		ext  string
		want string
	}{
		{off, ".docx", classLight}, {on, ".docx", classHeavy}, {on, ".pptx", classHeavy},
		{on, ".epub", classHeavy}, {on, ".xlsx", classLight}, {plain, ".docx", classLight},
		{off, ".xls", classHeavy}, {plain, ".xls", classHeavy},
	} {
		if got := c.srv.classFor(c.ext); got != c.want {
			t.Errorf("%T ocr config, %s: %s, want %s", c.srv.eng, c.ext, got, c.want)
		}
	}
}

// With OCR on, a docx takes a heavy ticket (and answers 429 with the heavy queue full).
func TestDocxUsesHeavyLaneWhenOCRIsOn(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_HEAVY", "1")
	t.Setenv("GLEANN_DOCS_HEAVY_QUEUE", "0")
	eng := &blockOCR{ocrEngine: ocrEngine{ocr: true}, blockEngine: newBlockEngine()}
	srv := New(eng, 8765)
	r1 := asyncPost(srv, "a.docx")
	waitStarted(t, eng.blockEngine, 1)
	if len(srv.heavy.admitCh) != 1 || len(srv.light.admitCh) != 0 {
		t.Fatalf("tickets heavy %d light %d, want 1 and 0", len(srv.heavy.admitCh), len(srv.light.admitCh))
	}
	if rec := post(t, srv, "b.docx", []byte("x")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429", rec.Code)
	}
	close(eng.release)
	<-r1
}

type blockOCR struct {
	ocrEngine
	*blockEngine
}

func (e *blockOCR) Convert(ctx context.Context, p string) (string, int, error) {
	return e.blockEngine.Convert(ctx, p)
}
func (e *blockOCR) IsAvailable() bool   { return true }
func (e *blockOCR) BackendName() string { return "x" }
func (e *blockOCR) Close() error        { return nil }
