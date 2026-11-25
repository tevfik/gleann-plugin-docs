package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockEngine holds every conversion until release is closed, and reports
// each entry on started.
type blockEngine struct {
	mockEngine
	started  chan struct{}
	release  chan struct{}
	inflight atomic.Int64
}

func (e *blockEngine) Convert(ctx context.Context, _ string) (string, int, error) {
	e.inflight.Add(1)
	e.started <- struct{}{}
	select {
	case <-e.release:
	case <-ctx.Done():
		e.inflight.Add(-1)
		return "", 0, ctx.Err()
	}
	e.inflight.Add(-1)
	return "# T\n\nbody", 1, nil
}

func newBlockEngine() *blockEngine {
	return &blockEngine{started: make(chan struct{}, 32), release: make(chan struct{})}
}

func waitStarted(t *testing.T, e *blockEngine, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-e.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d conversions started", i, n)
		}
	}
}

func waitQueued(t *testing.T, srv *Server, n int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for srv.queued.Load() != n {
		if time.Now().After(deadline) {
			t.Fatalf("queued = %d, want %d", srv.queued.Load(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// asyncPost runs one upload through the handler chain in its own goroutine.
func asyncPost(srv *Server, name string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, uploadRequestFor(name))
		done <- w
	}()
	return done
}

// uploadRequestFor is uploadRequest without the *testing.T (goroutine-safe).
func uploadRequestFor(name string) *http.Request {
	body, contentType := multipartBody(name, []byte("x"))
	req := httptest.NewRequest("POST", "/convert", body)
	req.Host = "127.0.0.1:8765"
	req.Header.Set("Content-Type", contentType)
	return req
}

// With max_concurrency=2 and queue=1, four parallel requests give two running
// conversions, one waiter and exactly one 429 with Retry-After.
func TestBoundedQueueReturns429WithRetryAfter(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_CONCURRENCY", "2")
	t.Setenv("GLEANN_DOCS_MAX_QUEUE", "1")
	eng := newBlockEngine()
	srv := New(eng, 8765)

	r1 := asyncPost(srv, "a.txt")
	r2 := asyncPost(srv, "b.txt")
	waitStarted(t, eng, 2)

	r3 := asyncPost(srv, "c.txt") // takes the single queue seat
	waitQueued(t, srv, 1)

	rec4 := post(t, srv, "d.txt", []byte("x")) // queue full
	if rec4.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429: %s", rec4.Code, rec4.Body.String())
	}
	ra := rec4.Header().Get("Retry-After")
	secs, err := strconv.Atoi(ra)
	if err != nil || secs < 1 {
		t.Fatalf("Retry-After = %q, want a positive integer number of seconds", ra)
	}

	close(eng.release)
	for i, ch := range []<-chan *httptest.ResponseRecorder{r1, r2, r3} {
		select {
		case rec := <-ch:
			if rec.Code != http.StatusOK {
				t.Fatalf("request %d: got %d, want 200: %s", i+1, rec.Code, rec.Body.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("request %d did not finish", i+1)
		}
	}
	if n := eng.inflight.Load(); n != 0 {
		t.Fatalf("inflight = %d after release", n)
	}
}

// A cancelled waiter leaves the queue, freeing its seat for the next request.
func TestCancelledWaiterLeavesTheQueue(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_CONCURRENCY", "1")
	t.Setenv("GLEANN_DOCS_MAX_QUEUE", "1")
	eng := newBlockEngine()
	srv := New(eng, 8765)

	r1 := asyncPost(srv, "a.txt")
	waitStarted(t, eng, 1)

	ctx, cancel := context.WithCancel(context.Background())
	req := uploadRequestFor("b.txt").WithContext(ctx)
	r2 := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		r2 <- w
	}()
	waitQueued(t, srv, 1)
	cancel()
	select {
	case rec := <-r2:
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("cancelled waiter: got %d, want 503", rec.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled waiter did not return")
	}
	waitQueued(t, srv, 0)

	r3 := asyncPost(srv, "c.txt") // the freed seat is usable again
	waitQueued(t, srv, 1)
	close(eng.release)
	for _, ch := range []<-chan *httptest.ResponseRecorder{r1, r3} {
		select {
		case rec := <-ch:
			if rec.Code != http.StatusOK {
				t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("request did not finish")
		}
	}
}

// /health keeps answering while every conversion slot is busy.
func TestHealthResponsiveWhileBusy(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_CONCURRENCY", "1")
	t.Setenv("GLEANN_DOCS_MAX_QUEUE", "0")
	eng := newBlockEngine()
	srv := New(eng, 8765)

	r1 := asyncPost(srv, "a.txt")
	waitStarted(t, eng, 1)

	start := time.Now()
	data := healthOf(t, srv)
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("/health took %v with all slots busy", d)
	}
	if data["status"] != "ok" {
		t.Fatalf("status = %v, want ok", data["status"])
	}
	if data["max_concurrency"] != float64(1) {
		t.Fatalf("max_concurrency = %v, want 1", data["max_concurrency"])
	}
	// queue=0: the second request is refused immediately.
	if rec := post(t, srv, "b.txt", []byte("x")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429 with an empty queue", rec.Code)
	}
	close(eng.release)
	if rec := <-r1; rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
}

func TestHealthAdvertisesMaxConcurrency(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_CONCURRENCY", "3")
	t.Setenv("GLEANN_DOCS_MAX_QUEUE", "2")
	srv := New(&mockEngine{}, 8765)
	data := healthOf(t, srv)
	if data["status"] != "ok" {
		t.Fatalf("status = %v, want ok", data["status"])
	}
	if data["max_concurrency"] != float64(3) {
		t.Fatalf("max_concurrency = %v, want 3", data["max_concurrency"])
	}
}

func TestDefaultAdmissionLimits(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_CONCURRENCY", "")
	t.Setenv("GLEANN_DOCS_MAX_QUEUE", "")
	srv := New(&mockEngine{}, 8765)
	if srv.maxConcurrent != defaultMaxConcurrent() {
		t.Fatalf("maxConcurrent = %d, want default %d", srv.maxConcurrent, defaultMaxConcurrent())
	}
	if srv.queueBound != defaultQueueBound {
		t.Fatalf("queueBound = %d, want %d", srv.queueBound, defaultQueueBound)
	}
	data := healthOf(t, srv)
	if data["max_concurrency"] != float64(srv.maxConcurrent) {
		t.Fatalf("max_concurrency = %v, want %d", data["max_concurrency"], srv.maxConcurrent)
	}
}

// Retry-After grows with the recent conversion duration.
func TestRetryAfterFromRecentDurations(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_CONCURRENCY", "2")
	t.Setenv("GLEANN_DOCS_MAX_QUEUE", "4")
	srv := New(&mockEngine{}, 8765)
	if got := srv.retryAfterSeconds(); got != 1 {
		t.Fatalf("no samples: Retry-After = %d, want 1", got)
	}
	for i := 0; i < 3; i++ {
		srv.recordDuration(2 * time.Second)
	}
	srv.queued.Store(3) // position 4 / 2 slots x 2 s
	if got := srv.retryAfterSeconds(); got != 4 {
		t.Fatalf("Retry-After = %d, want 4", got)
	}
}

// Concurrent conversions stay within the configured cap (run with -race).
func TestConcurrentConvertsRespectTheCap(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_CONCURRENCY", "3")
	t.Setenv("GLEANN_DOCS_MAX_QUEUE", "16")
	eng := newBlockEngine()
	srv := New(eng, 8765)

	const n = 12
	var wg sync.WaitGroup
	results := make(chan int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, uploadRequestFor("a.txt"))
			results <- w.Code
		}()
	}
	// Let the first wave settle, then release everyone.
	waitStarted(t, eng, 3)
	waitQueued(t, srv, 9)
	close(eng.release)
	wg.Wait()
	close(results)
	ok := 0
	for code := range results {
		if code != http.StatusOK {
			t.Fatalf("got %d, want 200 (12 requests fit in 3 slots + 16 queue seats)", code)
		}
		ok++
	}
	if ok != n {
		t.Fatalf("only %d of %d requests finished", ok, n)
	}
}
