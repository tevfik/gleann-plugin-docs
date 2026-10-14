package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../../../gleann/ext/eif-runtime/include -O3
#cgo linux LDFLAGS: ${SRCDIR}/../../../../gleann/ext/eif-runtime/build/libeif_runtime.a -Wl,-Bstatic -lgomp -Wl,-Bdynamic -lpthread -lm
#cgo darwin LDFLAGS: ${SRCDIR}/../../../../gleann/ext/eif-runtime/build/libeif_runtime.a -lm
#cgo windows LDFLAGS: ${SRCDIR}/../../../../gleann/ext/eif-runtime/build/libeif_runtime.a -lm
#include "eif_llm.h"
#include <stdlib.h>
#include <string.h>

typedef struct {
    char *buf;
    size_t cap;
    size_t len;
} docling_buf_t;

static void docling_token_collector(const char *piece, int token_id, void *user_data) {
    docling_buf_t *b = (docling_buf_t *)user_data;
    if (!piece || !b) return;
    size_t plen = strlen(piece);
    if (b->len + plen + 1 >= b->cap) {
        size_t new_cap = (b->cap * 2) > (b->len + plen + 65536) ? (b->cap * 2) : (b->len + plen + 65536);
        char *new_buf = (char *)realloc(b->buf, new_cap);
        if (!new_buf) return;
        b->buf = new_buf;
        b->cap = new_cap;
    }
    memcpy(b->buf + b->len, piece, plen);
    b->len += plen;
    b->buf[b->len] = '\0';
}

static char *c_run_docling_vision(eif_llm_t *llm, const char *image_path, const char *prompt, int max_tokens) {
    if (!llm || !image_path) return NULL;

    docling_buf_t b;
    b.cap = 65536;
    b.len = 0;
    b.buf = (char *)malloc(b.cap);
    if (!b.buf) return NULL;
    b.buf[0] = '\0';

    eif_llm_gen_config_t cfg;
    memset(&cfg, 0, sizeof(cfg));
    cfg.max_new_tokens = max_tokens > 0 ? max_tokens : 1024;
    cfg.temperature = 0.0f; // greedy argmax for OCR
    cfg.top_p = 0.90f;
    cfg.repetition_penalty = 1.10f;
    cfg.eos_token_id = -1;

    const char *p = prompt ? prompt : "Convert this page to docling.";
    int res = eif_llm_generate_vision_docling(llm, image_path, p, &cfg, docling_token_collector, &b);
    if (res < 0) {
        free(b.buf);
        return NULL;
    }
    return b.buf;
}
*/
import "C"
import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/tevfik/gleann-plugin-docs/internal/parser"
)

// EIFDoclingEngine runs IBM Docling Granite BitNet vision inference via eif-runtime.
type EIFDoclingEngine struct {
	llm           C.eif_llm_t
	modelPath     string
	tokenizerPath string
	loaded        bool
	mu            sync.Mutex
}

// NewEIFDoclingEngine initializes the CGO eif-runtime model engine.
func NewEIFDoclingEngine(modelPath, tokenizerPath string) (*EIFDoclingEngine, error) {
	resolvedModel, resolvedTok := resolveModelPaths(modelPath, tokenizerPath)
	eng := &EIFDoclingEngine{
		modelPath:     resolvedModel,
		tokenizerPath: resolvedTok,
	}

	if resolvedModel == "" {
		return eng, fmt.Errorf("no docling model found (checked --model, GLEANN_DOCLING_MODEL, and ~/.gleann/models)")
	}

	cModel := C.CString(resolvedModel)
	defer C.free(unsafe.Pointer(cModel))

	var cTok *C.char
	if resolvedTok != "" {
		cTok = C.CString(resolvedTok)
		defer C.free(unsafe.Pointer(cTok))
	}

	ret := C.eif_llm_load(&eng.llm, cModel, cTok, nil, 0)
	if ret != 0 {
		return eng, fmt.Errorf("eif_llm_load failed with code %d for model %s", int(ret), resolvedModel)
	}

	eng.loaded = true
	log.Printf("[docling-eif] successfully loaded model: %s", resolvedModel)
	return eng, nil
}

func (e *EIFDoclingEngine) IsAvailable() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loaded
}

func (e *EIFDoclingEngine) BackendName() string {
	return "docling-eif"
}

func (e *EIFDoclingEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loaded {
		C.eif_llm_free(&e.llm)
		e.loaded = false
	}
	return nil
}

// Convert processes an input file (PDF, image, or text) to Markdown.
func (e *EIFDoclingEngine) Convert(ctx context.Context, filePath string) (string, int, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	// Plain text / Markdown / CSV / DOCX
	switch ext {
	case ".txt", ".md", ".csv", ".json":
		b, err := os.ReadFile(filePath)
		if err != nil {
			return "", 0, err
		}
		return string(b), 1, nil
	case ".docx":
		md, err := ExtractDocx(filePath)
		if err != nil {
			return "", 0, err
		}
		return md, 1, nil
	}

	// Images: PNG, JPG, JPEG
	switch ext {
	case ".png", ".jpg", ".jpeg":
		md, err := e.convertImage(filePath)
		if err != nil {
			return "", 0, err
		}
		return md, 1, nil
	}

	// PDF
	if ext == ".pdf" {
		return e.convertPDF(ctx, filePath)
	}

	return "", 0, fmt.Errorf("unsupported file extension: %s", ext)
}

func (e *EIFDoclingEngine) convertImage(imagePath string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.loaded {
		return "", fmt.Errorf("docling model is not loaded")
	}

	cImg := C.CString(imagePath)
	defer C.free(unsafe.Pointer(cImg))

	cPrompt := C.CString("Convert this page to docling.")
	defer C.free(unsafe.Pointer(cPrompt))

	cRes := C.c_run_docling_vision(&e.llm, cImg, cPrompt, 1024)
	if cRes == nil {
		return "", fmt.Errorf("vision inference returned null")
	}
	defer C.free(unsafe.Pointer(cRes))

	rawDocTags := C.GoString(cRes)
	markdown := parser.DocTagsToMarkdown(rawDocTags)
	return markdown, nil
}

func (e *EIFDoclingEngine) convertPDF(ctx context.Context, pdfPath string) (string, int, error) {
	// Fast path: If PDF has digital text, extract it in milliseconds
	forceVLM := os.Getenv("GLEANN_DOCLING_FORCE_VLM") == "1"
	if !forceVLM {
		txtCmd := exec.CommandContext(ctx, "pdftotext", pdfPath, "-")
		if out, err := txtCmd.Output(); err == nil && len(strings.TrimSpace(string(out))) > 100 {
			text := strings.TrimSpace(string(out))
			return text, 1, nil
		}
	}

	// Scanned or image-based PDF: Route to Docling VLM
	tmpDir, err := os.MkdirTemp("", "gleann_docling_pdf_*")
	if err != nil {
		return "", 0, err
	}
	defer os.RemoveAll(tmpDir)

	pagePrefix := filepath.Join(tmpDir, "page")
	cmd := exec.CommandContext(ctx, "pdftoppm", "-png", "-r", "150", pdfPath, pagePrefix)
	if err := cmd.Run(); err != nil {
		log.Printf("[docling-eif] pdftoppm failed (%v)", err)
		return "", 0, fmt.Errorf("pdf rendering failed: %w", err)
	}

	// Find generated page images
	matches, err := filepath.Glob(filepath.Join(tmpDir, "page-*.png"))
	if err != nil || len(matches) == 0 {
		return "", 0, fmt.Errorf("no page images generated by pdftoppm")
	}

	// Sort pages numerically: page-1.png, page-2.png, ..., page-10.png
	sort.Slice(matches, func(i, j int) bool {
		numI := extractPageNumber(matches[i])
		numJ := extractPageNumber(matches[j])
		return numI < numJ
	})

	var pageMarkdowns []string
	for _, pageImg := range matches {
		if ctx.Err() != nil {
			return "", 0, ctx.Err()
		}
		pageMD, err := e.convertImage(pageImg)
		if err != nil {
			log.Printf("[docling-eif] warning: failed page %s: %v", pageImg, err)
			continue
		}
		pageMarkdowns = append(pageMarkdowns, pageMD)
	}

	if len(pageMarkdowns) == 0 {
		return "", 0, fmt.Errorf("no content extracted from pdf pages")
	}

	joined := strings.Join(pageMarkdowns, "\n\n---\n\n")
	return joined, len(matches), nil
}

func extractPageNumber(path string) int {
	base := filepath.Base(path)
	base = strings.TrimPrefix(base, "page-")
	base = strings.TrimSuffix(base, ".png")
	n, _ := strconv.Atoi(base)
	return n
}

func resolveModelPaths(modelPath, tokenizerPath string) (string, string) {
	if modelPath != "" {
		if _, err := os.Stat(modelPath); err == nil {
			if tokenizerPath == "" {
				tokCand := filepath.Join(filepath.Dir(modelPath), "tokenizer.bin")
				if _, err := os.Stat(tokCand); err == nil {
					tokenizerPath = tokCand
				}
			}
			return modelPath, tokenizerPath
		}
	}

	candidates := []string{
		os.Getenv("GLEANN_DOCLING_MODEL"),
		"/tmp/hf_test_model/granite_docling_bitnet_dense.eifm",
		filepath.Join(os.Getenv("HOME"), ".gleann", "models", "granite-docling", "granite_docling_bitnet_dense.eifm"),
		"artifacts/granite_docling/granite_docling_bitnet_dense.eifm",
		"../../gleann/ext/eif-runtime/artifacts/granite_docling/granite_docling_bitnet_dense.eifm",
	}

	for _, cand := range candidates {
		if cand == "" {
			continue
		}
		if _, err := os.Stat(cand); err == nil {
			tokCand := filepath.Join(filepath.Dir(cand), "tokenizer.bin")
			if tokenizerPath == "" {
				if _, err := os.Stat(tokCand); err == nil {
					tokenizerPath = tokCand
				}
			}
			return cand, tokenizerPath
		}
	}

	return "", ""
}
