package httpserver

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A client that sends headers and part of the body, then stalls, must lose its
// ticket within the upload idle timeout, and the next request must not be 429.
func TestStalledUploadReleasesTicket(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_CONCURRENCY", "1")
	t.Setenv("GLEANN_DOCS_MAX_QUEUE", "0")
	t.Setenv("GLEANN_DOCS_UPLOAD_IDLE_TIMEOUT", "1")
	srv := New(&mockEngine{}, 8765)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	addr := strings.TrimPrefix(ts.URL, "http://")

	body, ct := multipartBody("a.txt", bytes.Repeat([]byte("x"), 1000))
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /convert HTTP/1.1\r\nHost: %s\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n%s",
		addr, ct, body.Len(), body.String()[:100])

	deadline := time.Now().Add(500 * time.Millisecond)
	for len(srv.admitCh) != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(srv.admitCh) != 1 {
		t.Fatal("stalled upload never took a ticket")
	}
	// Idle timeout 1 s: the ticket must be back well within 4 s.
	deadline = time.Now().Add(4 * time.Second)
	for len(srv.admitCh) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("stalled upload still holds its ticket")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rec := post(t, srv, "b.txt", []byte("x")); rec.Code != http.StatusOK {
		t.Fatalf("next request got %d, want 200", rec.Code)
	}
	// The stalled client was answered 408 and its spool file is gone.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, _ := conn.Read(buf)
	if !strings.Contains(string(buf[:n]), " 408 ") {
		t.Fatalf("stalled client got %q, want a 408", buf[:n])
	}
}

// Upload spools once, into the engine's temp root only: during a 40 MiB
// upload the disk holds about 1x, and nothing is left in os.TempDir().
func TestUploadSpoolsOnceIntoRoot(t *testing.T) {
	sys := t.TempDir()
	t.Setenv("TMPDIR", sys)
	root := filepath.Join(sys, "root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	setEngineTempDir(t, root)

	t.Setenv("GLEANN_DOCS_MAX_CONCURRENCY", "1")
	t.Setenv("GLEANN_DOCS_MAX_QUEUE", "1")
	eng := newBlockEngine()
	srv := New(eng, 8765)

	data := bytes.Repeat([]byte("y"), 40<<20)
	r1done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		body, ct := multipartBody("big.txt", data)
		req := httptest.NewRequest("POST", "/convert", body)
		req.Host = "127.0.0.1:8765"
		req.Header.Set("Content-Type", ct)
		srv.Handler().ServeHTTP(w, req)
		r1done <- w
	}()
	waitStarted(t, eng, 1) // converting: the upload is fully spooled

	var total int64
	var outside []string
	_ = filepath.Walk(sys, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		if !strings.HasPrefix(p, root+string(filepath.Separator)) {
			outside = append(outside, p)
		}
		return nil
	})
	if len(outside) != 0 {
		t.Fatalf("files outside the temp root: %v", outside)
	}
	if total > 45<<20 {
		t.Fatalf("disk holds %d bytes for one 40 MiB upload, want about 1x", total)
	}
	close(eng.release)
	if rec := <-r1done; rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var left []string
	_ = filepath.Walk(sys, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			left = append(left, p)
		}
		return nil
	})
	if len(left) != 0 {
		t.Fatalf("spool files left behind: %v", left)
	}
}

// A slow-drip client (one byte every 400 ms, always inside the idle limit)
// still loses its ticket once the total upload time is over.
func TestSlowDripUploadHitsTotalDeadline(t *testing.T) {
	t.Setenv("GLEANN_DOCS_MAX_CONCURRENCY", "1")
	t.Setenv("GLEANN_DOCS_MAX_QUEUE", "0")
	t.Setenv("GLEANN_DOCS_UPLOAD_IDLE_TIMEOUT", "1")
	t.Setenv("GLEANN_DOCS_UPLOAD_MAX_TIME", "2")
	srv := New(&mockEngine{}, 8765)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	addr := strings.TrimPrefix(ts.URL, "http://")

	body, ct := multipartBody("a.txt", bytes.Repeat([]byte("x"), 1000))
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /convert HTTP/1.1\r\nHost: %s\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n", addr, ct, body.Len())
	go func() {
		for _, b := range body.Bytes() {
			if _, err := conn.Write([]byte{b}); err != nil {
				return
			}
			time.Sleep(400 * time.Millisecond)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	time.Sleep(300 * time.Millisecond)
	for len(srv.admitCh) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("slow-drip upload still holds its ticket")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rec := post(t, srv, "b.txt", []byte("x")); rec.Code != http.StatusOK {
		t.Fatalf("next request got %d, want 200", rec.Code)
	}
}
