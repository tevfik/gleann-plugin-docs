package httpserver

import (
	"context"
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

	"github.com/tevfik/gleann-plugin-docs/internal/config"
	"github.com/tevfik/gleann-plugin-docs/internal/engine"
	"github.com/tevfik/gleann-plugin-docs/internal/parser"
	"github.com/tevfik/gleann/modules/settings"
)

// PluginVersion is overridden at release build time with
// -ldflags "-X github.com/tevfik/gleann-plugin-docs/internal/httpserver.PluginVersion=<tag without v>".
var PluginVersion = "1.5.3"

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
}

// New creates a new HTTP plugin server.
// maxUploadBytes bounds the request body of /convert.
const maxUploadBytes = 256 << 20

func New(eng engine.Converter, port int) *Server {
	if port <= 0 {
		port = 8765
	}
	return &Server{
		eng:  eng,
		port: port,
	}
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
	return mux
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
	log.Printf("[%s] HTTP plugin listening on http://%s", PluginName, addr)

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	srv := &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
	}
	s.httpSrv = srv
	s.mu.Unlock()

	// A requested shutdown is not an error: ErrServerClosed means Shutdown or
	// Close ran, so the process can exit 0.
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
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
			_ = srv.Close() // deadline: drop the remaining connections
		}
	}
	if cerr := s.closeEngine(); err == nil {
		err = cerr
	}
	return err
}

// Close shuts down the HTTP server and frees engine resources.
func (s *Server) Close() error {
	s.mu.Lock()
	s.stopped = true
	srv := s.httpSrv
	s.mu.Unlock()

	if srv != nil {
		_ = srv.Close()
	}
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":         "ok",
		"plugin":         PluginName,
		"version":        PluginVersion,
		"schema_version": SchemaVersion,
		"capabilities":   Capabilities,
		"timeout":        TimeoutSec,
		"backends": map[string]bool{
			"docling-eif": s.eng != nil && s.eng.IsAvailable(),
			"bitllm.c":    s.eng != nil && s.eng.IsAvailable(),
			"markitdown":  true,
		},
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
	tmpFile, err := os.CreateTemp("", "gleann_upload_*"+ext)
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

	if s.eng == nil {
		writeError(w, http.StatusInternalServerError, "engine is not configured")
		return
	}

	markdown, pageCount, err := s.eng.Convert(r.Context(), tmpPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("conversion failed: %v", err))
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

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg})
}
