package httpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tevfik/gleann-plugin-docs/internal/engine"
	"github.com/tevfik/gleann-plugin-docs/internal/parser"
)

const (
	PluginName    = "gleann-plugin-docs"
	PluginVersion = "1.1.0"
	SchemaVersion = 2
)

var (
	Capabilities = []string{"document-extraction"}
	SupportedExts = []string{
		".pdf", ".docx", ".doc", ".xlsx", ".xls",
		".pptx", ".ppt", ".epub", ".html", ".htm",
		".png", ".jpg", ".jpeg", ".csv", ".txt", ".md",
	}
)

// Server implements the HTTP plugin server for gleann.
type Server struct {
	eng     engine.Converter
	port    int
	mu      sync.RWMutex
	httpSrv *http.Server
}

// New creates a new HTTP plugin server.
func New(eng engine.Converter, port int) *Server {
	if port <= 0 {
		port = 8765
	}
	return &Server{
		eng:  eng,
		port: port,
	}
}

// Serve starts the HTTP server.
func (s *Server) Serve() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/info", s.handleInfo)
	mux.HandleFunc("/convert", s.handleConvert)

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	log.Printf("[%s] HTTP plugin listening on http://%s", PluginName, addr)

	s.mu.Lock()
	s.httpSrv = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	s.mu.Unlock()

	return s.httpSrv.ListenAndServe()
}

// Close shuts down the HTTP server and frees engine resources.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.httpSrv != nil {
		_ = s.httpSrv.Close()
	}
	if s.eng != nil {
		return s.eng.Close()
	}
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":       "ok",
		"plugin":       PluginName,
		"version":      PluginVersion,
		"schema_version": SchemaVersion,
		"capabilities": Capabilities,
		"timeout":      120,
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
	})
}

func (s *Server) handleConvert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST is supported")
		return
	}

	// 256 MB max upload
	if err := r.ParseMultipartForm(256 << 20); err != nil {
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
