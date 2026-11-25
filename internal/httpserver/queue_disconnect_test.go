package httpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann-plugin-docs/internal/engine"
)

// rawUpload sends a complete HTTP/1.1 multipart upload over a real TCP
// connection and returns the connection (the caller closes it).
func rawUpload(t *testing.T, addr, name string) net.Conn {
	t.Helper()
	body, ct := multipartBody(name, []byte("x"))
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(conn, "POST /convert HTTP/1.1\r\nHost: %s\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n%s",
		addr, ct, body.Len(), body.String())
	return conn
}

// A client that really closes its socket while queued must give its seat back
// and its conversion must never start. (net/http only notices a closed peer
// once the body has been read, so the body is spooled before the wait.)
func TestDisconnectedWaiterFreesSeatOverTCP(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_LIGHT", "1")
	t.Setenv("GLEANN_DOCS_LIGHT_QUEUE", "1")
	eng := newBlockEngine()
	srv := New(eng, 8765)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	addr := strings.TrimPrefix(ts.URL, "http://")

	c1 := rawUpload(t, addr, "a.txt")
	defer c1.Close()
	waitStarted(t, eng, 1)

	c2 := rawUpload(t, addr, "b.txt")
	waitQueued(t, srv, 1)
	c2.Close() // the client goes away

	deadline := time.Now().Add(3 * time.Second)
	for srv.light.queued.Load() != 0 || len(srv.light.admitCh) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("disconnected waiter still holds its seat: queued=%d tickets=%d", srv.light.queued.Load(), len(srv.light.admitCh))
		}
		time.Sleep(5 * time.Millisecond)
	}

	close(eng.release) // free the running slot: the ghost must not start
	select {
	case <-eng.started:
		t.Fatal("conversion of a disconnected client started")
	case <-time.After(300 * time.Millisecond):
	}
}

// Every 503 carries Retry-After.
func TestEvery503CarriesRetryAfter(t *testing.T) {
	check := func(name string, h http.Header, code int) {
		t.Helper()
		if code != http.StatusServiceUnavailable {
			t.Fatalf("%s: got %d, want 503", name, code)
		}
		if v, err := strconv.Atoi(h.Get("Retry-After")); err != nil || v < 1 || v > 60 {
			t.Fatalf("%s: Retry-After = %q, want 1..60", name, h.Get("Retry-After"))
		}
	}
	// nil engine
	srv := New(nil, 8765)
	rec := post(t, srv, "a.txt", []byte("x"))
	check("nil engine", rec.Header(), rec.Code)

	// backend unavailable, cancelled conversion
	for name, err := range map[string]error{
		"backend":   fmt.Errorf("x: %w", engine.ErrBackendUnavailable),
		"cancelled": context.Canceled,
	} {
		srv := New(&failEngine{err: err}, 8765)
		rec := post(t, srv, "a.txt", []byte("x"))
		check(name, rec.Header(), rec.Code)
	}
}

// Once shutdown starts, a queued request that has not begun is refused with
// 503 + Retry-After and never starts a conversion; the running one drains.
func TestShutdownRefusesQueuedWaiters(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_LIGHT", "1")
	t.Setenv("GLEANN_DOCS_LIGHT_QUEUE", "2")
	eng := newBlockEngine()
	srv := New(eng, 8765)

	r1 := asyncPost(srv, "a.txt")
	waitStarted(t, eng, 1)
	r2 := asyncPost(srv, "b.txt")
	waitQueued(t, srv, 1)

	go func() { _ = srv.Shutdown(context.Background()) }()

	select {
	case rec := <-r2:
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
			t.Fatalf("queued waiter: got %d Retry-After=%q, want 503 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued waiter was not refused at shutdown")
	}
	// A request arriving during the grace period is refused too.
	if rec := post(t, srv, "c.txt", []byte("x")); rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("late request: got %d, want 503 with Retry-After", rec.Code)
	}
	close(eng.release)
	if rec := <-r1; rec.Code != http.StatusOK {
		t.Fatalf("in-flight conversion: got %d, want 200", rec.Code)
	}
	select {
	case <-eng.started:
		t.Fatal("a conversion started after shutdown began")
	default:
	}
}

// Retry-After = queue position / concurrency x typical duration, 1..60 s.
func TestRetryAfterIsParallelismAwareAndClamped(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_LIGHT", "4")
	t.Setenv("GLEANN_DOCS_LIGHT_QUEUE", "8")
	srv := New(&mockEngine{}, 8765)
	for i := 0; i < 3; i++ {
		srv.light.recordDuration(8 * time.Second)
	}
	srv.light.queued.Store(7) // position 8 -> 8/4 x 8 s
	if got := srv.light.retryAfterSeconds(); got != 16 {
		t.Fatalf("Retry-After = %d, want 16", got)
	}
	srv.light.recordDuration(900 * time.Second)
	for i := 0; i < 16; i++ {
		srv.light.recordDuration(900 * time.Second)
	}
	if got := srv.light.retryAfterSeconds(); got != 60 {
		t.Fatalf("Retry-After = %d, want clamp 60", got)
	}
}

// The mask matches on a path boundary: /var/tmp is not /tmp.
func TestMaskTempPathsRespectsBoundary(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	setEngineTempDir(t, "/tmp/gleann-docs-1")
	got := maskTempPaths("open /tmp/gleann-docs-1/x: a; open /var/tmp/y: b; open /tmpfoo/z: c; stat /tmp/m: d; dir /tmp")
	for _, want := range []string{"/var/tmp/y", "/tmpfoo/z"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q was rewritten: %s", want, got)
		}
	}
	if strings.Contains(got, "/tmp/gleann-docs-1") || strings.Contains(got, "/tmp/m") {
		t.Fatalf("temp path not masked: %s", got)
	}
}

// A context that is already cancelled never gets a slot, even when one is free.
func TestAcquireRunRechecksContext(t *testing.T) {
	srv := New(&mockEngine{}, 8765)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := srv.light.acquire(ctx, srv); err == nil {
		t.Fatal("acquireRun succeeded with a cancelled context")
	}
	if len(srv.light.runCh) != 0 {
		t.Fatalf("slot leaked: %d held", len(srv.light.runCh))
	}
}
