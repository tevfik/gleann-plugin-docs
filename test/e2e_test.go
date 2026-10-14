package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann-plugin-docs/internal/httpserver"
	"github.com/tevfik/gleann/modules/chunking"
)

func TestEndToEnd_DocumentExtractionAndTableChunking(t *testing.T) {
	port := 8799
	// Start server with mock engine to test complete HTTP + Pipeline + Chunker flow
	srv := httpserver.New(&e2eEngine{}, port)

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- srv.Serve()
	}()
	defer srv.Close()

	// Wait for server ready
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 3 * time.Second}
	ready := false
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		resp, err := client.Get(baseURL + "/health")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			ready = true
			break
		}
	}
	if !ready {
		t.Fatalf("plugin server failed to start on %s", baseURL)
	}

	// 1. Verify /health
	resp, err := client.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	var health map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&health)
	resp.Body.Close()

	if health["status"] != "ok" || health["plugin"] != "gleann-plugin-docs" {
		t.Fatalf("unexpected health response: %v", health)
	}

	// 2. Verify /info
	resp, err = client.Get(baseURL + "/info")
	if err != nil {
		t.Fatalf("GET /info failed: %v", err)
	}
	var info map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&info)
	resp.Body.Close()

	if int(info["schema_version"].(float64)) != 2 {
		t.Fatalf("expected schema_version 2, got %v", info["schema_version"])
	}

	// 3. Test /convert with a multi-section document containing a Markdown Table
	docContent := `# Technical Specification

This document details hardware parameters.

## Performance Metrics
Below are benchmark measurements:

| Metric | Baseline | EIF BitNet | Speedup |
| --- | --- | --- | --- |
| ViT Prefill | 12.4s | 6.4s | 1.94x |
| LLM Decode | 18 tok/s | 572 tok/s | 31.7x |
| Memory (RAM) | 3500MB | 250MB | 14.0x |

## Architecture Notes
The system utilizes BitNet b1.58 ternary weights.`

	tmpFile := filepath.Join(t.TempDir(), "benchmark_report.md")
	if err := os.WriteFile(tmpFile, []byte(docContent), 0644); err != nil {
		t.Fatal(err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "benchmark_report.md")
	if err != nil {
		t.Fatal(err)
	}
	fileBytes, _ := os.ReadFile(tmpFile)
	_, _ = part.Write(fileBytes)
	_ = writer.Close()

	req, _ := http.NewRequest("POST", baseURL+"/convert", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	convertResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /convert failed: %v", err)
	}
	if convertResp.StatusCode != 200 {
		b, _ := io.ReadAll(convertResp.Body)
		t.Fatalf("POST /convert returned %d: %s", convertResp.StatusCode, string(b))
	}

	var result struct {
		Nodes    []map[string]any `json:"nodes"`
		Edges    []map[string]any `json:"edges"`
		Markdown string           `json:"markdown"`
		Backend  string           `json:"backend"`
	}
	_ = json.NewDecoder(convertResp.Body).Decode(&result)
	convertResp.Body.Close()

	// Verify Nodes: Document (1) + Sections (3)
	if len(result.Nodes) < 3 {
		t.Fatalf("expected at least 3 nodes, got %d", len(result.Nodes))
	}
	var docTitle string
	for _, n := range result.Nodes {
		if n["_type"] == "Document" {
			docTitle = n["title"].(string)
		}
	}
	if docTitle != "Technical Specification" {
		t.Errorf("expected doc title 'Technical Specification', got %q", docTitle)
	}

	// 4. Feed the extracted Sections into gleann's Table-Aware Chunker!
	var sections []chunking.MarkdownSection
	for _, n := range result.Nodes {
		if n["_type"] == "Section" {
			sections = append(sections, chunking.MarkdownSection{
				ID:      n["id"].(string),
				Heading: n["heading"].(string),
				Level:   int(n["level"].(float64)),
				Content: n["content"].(string),
			})
		}
	}

	doc := &chunking.StructuredDocument{
		Document: chunking.DocumentMeta{
			Title: docTitle,
		},
		Sections: sections,
	}

	chunker := chunking.NewMarkdownChunker(100, 20)
	chunks := chunker.ChunkDocument(doc)

	// Verify that table chunks retain table metadata and columns
	foundTableChunk := false
	for _, ch := range chunks {
		if isTable, ok := ch.Metadata["is_table"].(bool); ok && isTable {
			foundTableChunk = true
			if !strings.Contains(ch.Text, "| Metric |") {
				t.Errorf("table chunk lost header row: %s", ch.Text)
			}
			if !strings.Contains(ch.Text, "ViT Prefill") {
				t.Errorf("table chunk missing rows: %s", ch.Text)
			}
		}
	}

	if !foundTableChunk {
		t.Errorf("expected table chunk with 'is_table: true' metadata")
	}

	t.Logf("E2E Test Success: Processed %d graph nodes, %d edges, and produced %d table-aware chunks!",
		len(result.Nodes), len(result.Edges), len(chunks))
}

type e2eEngine struct{}

func (e *e2eEngine) Convert(ctx context.Context, filePath string) (string, int, error) {
	b, err := os.ReadFile(filePath)
	return string(b), 1, err
}

func (e *e2eEngine) IsAvailable() bool { return true }
func (e *e2eEngine) BackendName() string { return "docling-eif" }
func (e *e2eEngine) Close() error { return nil }
