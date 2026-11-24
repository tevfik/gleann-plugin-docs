package httpserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tevfik/gleann-plugin-docs/internal/config"
	"github.com/tevfik/gleann-plugin-docs/internal/engine"
	"github.com/tevfik/gleann-plugin-docs/internal/parser"
	"github.com/tevfik/gleann/modules/settings"
)

// PluginVersion is overridden at release build time with
// -ldflags "-X github.com/tevfik/gleann-plugin-docs/internal/httpserver.PluginVersion=<tag without v>".
var PluginVersion = "1.5.5"

const (
	PluginName = "gleann-plugin-docs"

	// TimeoutSec is the per-request HTTP timeout gleann applies to this plugin
	// (written to plugins.json). OCR of a scanned PDF takes about 1.7 s per page
	// on a 20-core ARM box, so gleann's default of 120 s cut off documents of
	// roughly 70 pages.
	TimeoutSec    = 900
	SchemaVersion = 2
)

var (
	Capabilities  = []string{"document-extraction"}
	SupportedExts = []string{
		".pdf", ".docx", ".doc", ".xlsx", ".xls",
		".pptx", ".ppt", ".epub", ".html", ".htm",
		".odt", ".ods", ".odp", ".rtf",
		".png", ".jpg", ".jpeg", ".tif", ".tiff", ".bmp", ".webp",
		".csv", ".txt", ".md",
	}
)

// Server implements the HTTP plugin server for gleann.
type Server struct {
	eng     engine.Converter
	port    int
	mu      sync.RWMutex
	httpSrv *http.Server
	stopped bool // Shutdown/Close ran: Serve must not start (or report an error)

	closeEng sync.Once
	engErr   error

	token       string // --token / GLEANN_DOCS_TOKEN
	pluginToken string // GLEANN_PLUGIN_TOKEN, set by the gleann host that started this process

	maxMarkdown int64  // largest Markdown result returned (413 above)
	tempDir     string // per-instance temp directory, set by Serve

	baseCtx    context.Context // parent of every request context; cancelled to unblock handlers at the shutdown deadline
	cancelBase context.CancelFunc
	inflight   atomic.Int64 // /convert-style requests currently running
}

// drainGrace is how long Shutdown waits, after cancelling the request
// contexts, for handlers to return and remove their temporary files.
const drainGrace = 800 * time.Millisecond

// New creates a new HTTP plugin server.
// maxUploadBytes bounds the request body of /convert.
const maxUploadBytes = 256 << 20

func New(eng engine.Converter, port int) *Server {
	if port <= 0 {
		port = 8765
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		eng:         eng,
		port:        port,
		token:       os.Getenv("GLEANN_DOCS_TOKEN"),
		pluginToken: os.Getenv("GLEANN_PLUGIN_TOKEN"),
		maxMarkdown: engine.MaxMarkdownBytes(),
		baseCtx:     ctx,
		cancelBase:  cancel,
	}
}

// SetToken sets the token that clients must present (the --token flag). It is
// accepted in addition to the GLEANN_PLUGIN_TOKEN of the gleann host.
func (s *Server) SetToken(token string) { s.token = token }

// authRequired reports whether a bearer token is configured.
func (s *Server) authRequired() bool { return s.token != "" || s.pluginToken != "" }

// authorized reports whether the request carries one of the configured tokens
// as "Authorization: Bearer <token>". Every configured token is compared in
// constant time. Without a configured token everything is authorized.
func (s *Server) authorized(r *http.Request) bool {
	if !s.authRequired() {
		return true
	}
	scheme, got, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	gotSum := sha256.Sum256([]byte(strings.TrimSpace(got)))
	match := 0
	for _, want := range []string{s.token, s.pluginToken} {
		if want == "" {
			continue
		}
		wantSum := sha256.Sum256([]byte(want))
		match |= subtle.ConstantTimeCompare(gotSum[:], wantSum[:])
	}
	return match == 1
}

// guard protects every route: the request must be addressed to a loopback host
// (the server only listens on loopback, so anything else is a DNS-rebinding
// attempt), then, except for /health, carry the bearer token when one is
// configured. /health stays token-free because the gleann host probes it
// before it has a token to send; it only reports state. The guard also counts
// running requests so Shutdown can wait for them.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r) {
			writeError(w, http.StatusForbidden, "requests must be addressed to a loopback host")
			return
		}
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		if !s.authorized(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gleann-plugin-docs"`)
			writeError(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}
		s.inflight.Add(1)
		defer s.inflight.Add(-1)
		next.ServeHTTP(w, r)
	})
}

// Handler routes the plugin's endpoints.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/info", s.handleInfo)
	mux.HandleFunc("/convert", s.handleConvert)
	// Generic settings form for clients such as the gleann web UI. The server
	// listens on loopback only and PUT is preflighted by browsers; the Host
	// check keeps a DNS-rebound web page out too.
	mux.Handle("/settings", config.Settings().Handler(loopbackHost))
	// Model downloads for the same clients; they outlive the request.
	mux.Handle("/models", config.Catalog().Handler(context.Background(), loopbackHost))
	return s.guard(mux)
}

// loopbackHost reports whether the request was addressed to this machine by a
// loopback name or address.
func loopbackHost(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// Serve starts the HTTP server.
func (s *Server) Serve() error {
	addr := fmt.Sprintf("127.0.0.1:%d", s.port)

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	// Listen first: a successful bind proves no other instance owns this port,
	// so the per-port temp directory below can only hold leftovers of a
	// previous run that was killed.
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if tcp, ok := l.Addr().(*net.TCPAddr); ok {
		s.port = tcp.Port
	}
	s.prepareTemp()
	log.Printf("[%s] HTTP plugin listening on http://%s", PluginName, l.Addr())
	if !s.authRequired() {
		log.Printf("[%s] warning: no token configured (GLEANN_PLUGIN_TOKEN, --token); any local process can use this server", PluginName)
	}

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		_ = l.Close()
		return nil
	}
	srv := &http.Server{
		Handler:     s.Handler(),
		BaseContext: func(net.Listener) context.Context { return s.baseCtx },
	}
	s.httpSrv = srv
	s.mu.Unlock()

	// A requested shutdown is not an error: ErrServerClosed means Shutdown or
	// Close ran, so the process can exit 0.
	if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// staleTempAge is how old a leftover of an earlier version must be before the
// startup sweep removes it from the system temp directory.
const staleTempAge = 2 * time.Hour

// prepareTemp empties this instance's temp directory, points the engine at it
// and removes old leftovers of earlier versions that used the system temp
// directory directly (only names this plugin creates, only when stale).
func (s *Server) prepareTemp() {
	dir := engine.InstanceTempDir(s.port)
	if err := engine.ResetTempDir(dir); err != nil {
		log.Printf("[%s] temp directory %s: %v (using the system temp directory)", PluginName, dir, err)
		return
	}
	s.tempDir = dir
	engine.SetTempDir(dir)
	if n := engine.SweepLegacyTemp(os.TempDir(), staleTempAge); n > 0 {
		log.Printf("[%s] removed %d stale temporary file(s) of earlier runs", PluginName, n)
	}
}

// Shutdown stops accepting requests, waits for running conversions to finish
// (so their temporary files are removed by the handlers) until ctx ends, then
// frees the engine. Serve returns nil once it has been called.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.stopped = true
	srv := s.httpSrv
	s.mu.Unlock()

	var err error
	if srv != nil {
		if err = srv.Shutdown(ctx); err != nil {
			// Deadline: cancel the running requests so their handlers return
			// and remove their temporary files, give them a moment, then drop
			// the remaining connections.
			s.cancelBase()
			s.waitIdle(drainGrace)
			_ = srv.Close()
		}
	}
	s.cancelBase()
	s.removeTemp()
	if cerr := s.closeEngine(); err == nil {
		err = cerr
	}
	return err
}

// waitIdle waits until no request is running, at most d.
func (s *Server) waitIdle(d time.Duration) {
	deadline := time.Now().Add(d)
	for s.inflight.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}

// removeTemp deletes this instance's temp directory.
func (s *Server) removeTemp() {
	if s.tempDir != "" {
		_ = os.RemoveAll(s.tempDir)
	}
}

// Close shuts down the HTTP server and frees engine resources.
func (s *Server) Close() error {
	s.mu.Lock()
	s.stopped = true
	srv := s.httpSrv
	s.mu.Unlock()

	s.cancelBase()
	if srv != nil {
		_ = srv.Close()
	}
	s.removeTemp()
	return s.closeEngine()
}

// closeEngine closes the engine exactly once (Shutdown and a deferred Close
// may both run).
func (s *Server) closeEngine() error {
	s.closeEng.Do(func() {
		if s.eng != nil {
			s.engErr = s.eng.Close()
		}
	})
	return s.engErr
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	backends := map[string]bool{}
	missing := []string{}
	switch eng := s.eng.(type) {
	case nil:
	case engine.BackendReporter:
		backends = eng.BackendStatus()
		missing = eng.MissingFormats()
	default:
		backends["docling-eif"] = eng.IsAvailable()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":         "ok",
		"plugin":         PluginName,
		"version":        PluginVersion,
		"schema_version": SchemaVersion,
		"capabilities":   Capabilities,
		"timeout":        TimeoutSec,
		"backends":       backends,
		"missing":        missing,
	})
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name":           PluginName,
		"version":        PluginVersion,
		"schema_version": SchemaVersion,
		"capabilities":   Capabilities,
		"extensions":     SupportedExts,
		"settings":       map[string]any{"path": "/settings", "schema_version": settings.SchemaVersion},
	})
}

func (s *Server) handleConvert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is supported")
		return
	}

	// The 256 MB argument of ParseMultipartForm is only the in-memory
	// threshold; the body itself is capped so an oversized upload is refused
	// instead of being spooled to disk and decoded (P-05).
	if s.eng == nil {
		writeError(w, http.StatusServiceUnavailable, "engine is not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("upload exceeds %d MB", maxUploadBytes>>20))
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("parse multipart: %v", err))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, 422, fmt.Sprintf("missing 'file' field: %v", err))
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !isExtensionSupported(ext) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Unsupported extension: %s", ext))
		return
	}

	// Save upload to temporary file
	tmpFile, err := os.CreateTemp(engine.TempDir(), "gleann_upload_*"+ext)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create temp file: %v", err))
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, file); err != nil {
		tmpFile.Close()
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("write temp file: %v", err))
		return
	}
	tmpFile.Close()

	markdown, pageCount, err := s.eng.Convert(r.Context(), tmpPath)
	if err != nil {
		writeConvertError(w, err)
		return
	}
	if int64(len(markdown)) > s.maxMarkdown {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("converted text is %d bytes, over the %d byte limit (GLEANN_DOCS_MAX_MARKDOWN_BYTES)", len(markdown), s.maxMarkdown))
		return
	}

	docFormat := strings.TrimPrefix(ext, ".")
	parsed := parser.ParseDocument(markdown, header.Filename, docFormat, pageCount)
	parsed.Backend = s.eng.BackendName()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(parsed)
}

func isExtensionSupported(ext string) bool {
	for _, supported := range SupportedExts {
		if strings.EqualFold(supported, ext) {
			return true
		}
	}
	return false
}

// writeConvertError maps a conversion failure to a status code: a client that
// went away gets no body, corrupt input 422, a missing backend 503, an input
// over a limit 413, a deadline 504; anything else stays 500.
func writeConvertError(w http.ResponseWriter, err error) {
	msg := fmt.Sprintf("conversion failed: %v", err)
	switch {
	case errors.Is(err, context.Canceled):
		// The client is gone (or the server is shutting down): nobody reads a body.
		w.WriteHeader(http.StatusServiceUnavailable)
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, msg)
	case errors.Is(err, engine.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, msg)
	case errors.Is(err, engine.ErrCorruptInput):
		writeError(w, http.StatusUnprocessableEntity, msg)
	case errors.Is(err, engine.ErrBackendUnavailable):
		writeError(w, http.StatusServiceUnavailable, msg)
	default:
		writeError(w, http.StatusInternalServerError, msg)
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg})
}
