package httpserver

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"testing"
	"time"
)

// blockingEngine waits for its context, like a long conversion that honours
// cancellation.
type blockingEngine struct {
	mockEngine
	started chan struct{}
}

func (b *blockingEngine) Convert(ctx context.Context, _ string) (string, int, error) {
	close(b.started)
	<-ctx.Done()
	return "", 0, ctx.Err()
}

func TestShutdownCancelsInFlightAtDeadline(t *testing.T) {
	t.Setenv("GLEANN_PLUGIN_TOKEN", "")
	t.Setenv("TMPDIR", t.TempDir())
	port := freePort(t)
	eng := &blockingEngine{started: make(chan struct{})}
	s := New(eng, port)
	serveDone := make(chan error, 1)
	go func() { serveDone <- s.Serve() }()
	waitListening(t, port)

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, _ := mw.CreateFormFile("file", "slow.txt")
	_, _ = part.Write([]byte("x"))
	_ = mw.Close()
	reqDone := make(chan struct{})
	go func() {
		defer close(reqDone)
		resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/convert", port), mw.FormDataContentType(), body)
		if err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-eng.started:
	case <-time.After(5 * time.Second):
		t.Fatal("conversion did not start")
	}
	own := engineInstanceDir(port)
	if entries, _ := os.ReadDir(own); len(entries) == 0 {
		t.Fatal("the upload should be in this instance's temp directory while converting")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_ = s.Shutdown(ctx)
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("Shutdown took %v with a 200ms deadline", d)
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return")
	}
	if exists(own) {
		entries, _ := os.ReadDir(own)
		t.Fatalf("temp directory not cleaned: %v", entries)
	}
	<-reqDone
}
