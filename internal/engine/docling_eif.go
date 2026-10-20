package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../ext/bitllm.c/include -O3
#cgo linux CFLAGS: -fopenmp
#cgo windows CFLAGS: -fopenmp
#cgo linux LDFLAGS: ${SRCDIR}/../../ext/bitllm.c/build/libbitllm.a -fopenmp -lpthread -lm
#cgo darwin LDFLAGS: ${SRCDIR}/../../ext/bitllm.c/build/libbitllm.a -lm
#cgo windows LDFLAGS: ${SRCDIR}/../../ext/bitllm.c/build/libbitllm.a -lm
#include "bitllm.h"
#include "eif_llm.h"
#ifdef _OPENMP
#include <omp.h>
#endif
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

static void docling_set_threads(int n) {
#ifdef _OPENMP
    if (n > 0) omp_set_num_threads(n);
#endif
}

// nthr is applied inside the call: the OpenMP team size is per OS thread and cgo calls may land on any thread.
static char *c_run_docling_vision(eif_llm_t *llm, const char *image_path, const char *prompt, int max_tokens, float rep_penalty, int nthr) {
    if (!llm || !image_path) return NULL;
    docling_set_threads(nthr);

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
    cfg.repetition_penalty = rep_penalty;
    cfg.eos_token_id = -1;

    const char *p = prompt ? prompt : "Convert this page to docling.";
    int res = eif_llm_generate_vision_docling(llm, image_path, p, &cfg, docling_token_collector, &b);
    if (res < 0) {
        free(b.buf);
        return NULL;
    }
    return b.buf;
}

// Page pipelining: the encoder of page i+1 and the decoder of page i run on different threads; each helper
// sets the OpenMP team size of its own OS thread inside the same C call.
static int c_encode_page(eif_llm_t *llm, const char *image_path, int nthr, eif_llm_vision_input_t *out) {
    docling_set_threads(nthr);
    return eif_llm_encode_vision_docling(llm, image_path, out);
}

static char *c_generate_page(eif_llm_t *llm, const eif_llm_vision_input_t *vin, const char *prompt, int max_tokens,
                             float rep_penalty, int nthr) {
    docling_set_threads(nthr);
    docling_buf_t b;
    b.cap = 65536;
    b.len = 0;
    b.buf = (char *)malloc(b.cap);
    if (!b.buf) return NULL;
    b.buf[0] = '\0';

    eif_llm_gen_config_t cfg;
    memset(&cfg, 0, sizeof(cfg));
    cfg.max_new_tokens = max_tokens > 0 ? max_tokens : 1024;
    cfg.temperature = 0.0f;
    cfg.top_p = 0.90f;
    cfg.repetition_penalty = rep_penalty;
    cfg.eos_token_id = -1;

    int res = eif_llm_generate_vision_docling_from(llm, vin, prompt ? prompt : "Convert this page to docling.", &cfg,
                                                   docling_token_collector, &b);
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
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/tevfik/gleann-plugin-docs/internal/docparser"
	"github.com/tevfik/gleann-plugin-docs/internal/parser"
)

// EIFDoclingEngine runs IBM Docling Granite BitNet vision inference via eif-runtime.
type EIFDoclingEngine struct {
	llm           C.eif_llm_t
	modelPath     string
	tokenizerPath string
	loaded        bool
	mu            sync.Mutex
	hybrid        *docparser.Parser // optional ONNX page parser (GLEANN_DOCLING_ONNX=1)
}

// NewEIFDoclingEngine initializes the CGO eif-runtime model engine.
func NewEIFDoclingEngine(modelPath, tokenizerPath string) (*EIFDoclingEngine, error) {
	resolvedModel, resolvedTok := resolveModelPaths(modelPath, tokenizerPath)
	eng := &EIFDoclingEngine{
		modelPath:     resolvedModel,
		tokenizerPath: resolvedTok,
	}

	defer eng.initHybrid() // after the VLM (if any) is loaded, so the deep path can use it

	if !vlmEnabled() {
		log.Printf("[docling-eif] VLM disabled (text layer + native Office extractors only); set GLEANN_DOCLING_VLM=1 to enable OCR of scanned pages and images")
		return eng, nil
	}

	if err := eng.loadModelLocked(); err != nil {
		return eng, err
	}
	return eng, nil
}

func (e *EIFDoclingEngine) loadModelLocked() error {
	if e.loaded {
		return nil
	}
	if e.modelPath == "" {
		return fmt.Errorf("no docling model found (checked --model, GLEANN_DOCLING_MODEL, and ~/.gleann/models)")
	}

	configureThreads()

	cModel := C.CString(e.modelPath)
	defer C.free(unsafe.Pointer(cModel))

	var cTok *C.char
	if e.tokenizerPath != "" {
		cTok = C.CString(e.tokenizerPath)
		defer C.free(unsafe.Pointer(cTok))
	}

	ret := C.eif_llm_load(&e.llm, cModel, cTok, nil, 0)
	if ret != 0 {
		return fmt.Errorf("eif_llm_load failed with code %d for model %s", int(ret), e.modelPath)
	}

	e.loaded = true
	log.Printf("[docling-eif] successfully loaded model: %s", e.modelPath)
	return nil
}

func (e *EIFDoclingEngine) IsAvailable() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loaded || e.hybrid != nil
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
	if e.hybrid != nil {
		err := e.hybrid.Close()
		e.hybrid = nil
		return err
	}
	return nil
}

// Convert processes an input file (PDF, image, or text) to Markdown.
func (e *EIFDoclingEngine) Convert(ctx context.Context, filePath string) (string, int, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	// Legacy binary Office: convert to OOXML, then recurse into the native extractors.
	if _, legacy := legacyTarget[ext]; legacy {
		converted, cleanup, err := convertLegacyOffice(ctx, filePath)
		if err != nil {
			return "", 0, err
		}
		defer cleanup()
		return e.Convert(ctx, converted)
	}

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
		return e.appendEmbeddedImageText(ctx, md, filePath, "word/media"), 1, nil
	case ".pptx":
		md, pages, err := ExtractPPTX(filePath)
		if err != nil {
			return "", 0, err
		}
		return e.appendEmbeddedImageText(ctx, md, filePath, "ppt/media"), pages, nil
	case ".xlsx":
		md, sheets, err := ExtractXLSX(filePath)
		if err != nil {
			return "", 0, err
		}
		return md, sheets, nil
	case ".html", ".htm":
		md, err := ExtractHTMLFile(filePath)
		if err != nil {
			return "", 0, err
		}
		return md, 1, nil
	case ".epub":
		md, chapters, err := ExtractEPUB(filePath)
		if err != nil {
			return "", 0, err
		}
		return md, chapters, nil
	}

	// TIFF / BMP / WebP: decode in Go, read as PNG.
	if extraImageExts[ext] {
		tmp, cleanup, err := toTempPNG(filePath)
		if err != nil {
			return "", 0, err
		}
		defer cleanup()
		md, err := e.convertPage(ctx, tmp)
		if err != nil {
			return "", 0, err
		}
		return md, 1, nil
	}

	// Images: PNG, JPG, JPEG
	switch ext {
	case ".png", ".jpg", ".jpeg":
		md, err := e.convertPage(ctx, filePath)
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

// configureThreads caps the OpenMP team size. The runtime's static scheduling
// and spin barriers degrade badly when all logical cores are used on a busy or
// hybrid (P/E core) machine: a 6s page took 270s with 20 threads under load.
// OMP_NUM_THREADS or GLEANN_DOCLING_THREADS override the default of NumCPU/2.
func configureThreads() {
	if os.Getenv("OMP_NUM_THREADS") != "" && os.Getenv("GLEANN_DOCLING_THREADS") == "" {
		return
	}
	C.docling_set_threads(C.int(threadBudget()))
}

// threadBudget is the OpenMP team size used for the VLM (see configureThreads).
func threadBudget() int {
	n := runtime.NumCPU() / 2
	if v, err := strconv.Atoi(os.Getenv("OMP_NUM_THREADS")); err == nil && v > 0 && os.Getenv("GLEANN_DOCLING_THREADS") == "" {
		n = v
	}
	if v, err := strconv.Atoi(os.Getenv("GLEANN_DOCLING_THREADS")); err == nil && v > 0 {
		n = v
	}
	if n < 4 {
		n = 4
	}
	return n
}

// pipelineThreads splits the thread budget between the vision encoder of the next page and the decoder of
// the current page. Decoding scales poorly past a few threads, the ViT is compute bound (measured on a
// 20-core ARM: 14 / 6 of 20 threads gave 1.33x on 3 pages over running the pages one after another).
func pipelineThreads(budget int) (vit, dec int) {
	dec = budget * 3 / 10
	if dec < 2 {
		dec = 2
	}
	vit = budget - dec
	if vit < 2 {
		vit = 2
	}
	return vit, dec
}

// pipelineEnabled: GLEANN_DOCLING_PIPELINE=0 turns page pipelining off.
func pipelineEnabled() bool { return os.Getenv("GLEANN_DOCLING_PIPELINE") != "0" }

func repetitionPenalty() float64 {
	if v, err := strconv.ParseFloat(os.Getenv("GLEANN_DOCLING_REP_PENALTY"), 64); err == nil && v > 0 {
		return v
	}
	return 1.0
}

func (e *EIFDoclingEngine) convertImage(imagePath string) (string, error) {
	raw, err := e.runVision(imagePath, "Convert this page to docling.")
	if err != nil {
		return "", err
	}
	return parser.DocTagsToMarkdown(raw), nil
}

// runVision runs the VLM on an image file with the given prompt and returns the
// raw DocTags it generated.
func (e *EIFDoclingEngine) runVision(imagePath, prompt string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.ensureLoadedLocked(); err != nil {
		return "", err
	}

	cImg := C.CString(imagePath)
	defer C.free(unsafe.Pointer(cImg))

	cPrompt := C.CString(prompt)
	defer C.free(unsafe.Pointer(cPrompt))

	cRes := C.c_run_docling_vision(&e.llm, cImg, cPrompt, 1024, C.float(repetitionPenalty()), C.int(threadBudget()))
	if cRes == nil {
		return "", fmt.Errorf("vision inference returned null")
	}
	defer C.free(unsafe.Pointer(cRes))

	rawDocTags := cleanVLMText(C.GoString(cRes))
	if os.Getenv("GLEANN_DOCLING_DEBUG") == "1" {
		log.Printf("[docling-eif] raw doctags (%d bytes) for %s:\n%s", len(rawDocTags), imagePath, rawDocTags)
	}
	return rawDocTags, nil
}

// minPageTextChars is the number of non-space characters a page's text layer
// needs before the page is considered "digital" and not in need of OCR/VLM.
const minPageTextChars = 20

// vlmEnabled reports whether the slow Docling VLM may be used. It is opt-in:
// ~10-17s/page on CPU and unreliable output on Turkish and dense pages, so by
// default only the text layer and the native Office extractors are used.
func vlmEnabled() bool {
	return envTrue("GLEANN_DOCLING_VLM") || envTrue("GLEANN_DOCLING_FORCE_VLM")
}

func envExplicitlyDisabled(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

func envTrue(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// pdfPageTexts returns the text layer of every page (pdftotext separates pages
// with a form feed).
func pdfPageTexts(ctx context.Context, pdfPath string) ([]string, error) {
	out, err := exec.CommandContext(ctx, "pdftotext", pdfPath, "-").Output()
	if err != nil {
		return nil, err
	}
	pages := strings.Split(string(out), "\f")
	if n := len(pages); n > 1 && strings.TrimSpace(pages[n-1]) == "" {
		pages = pages[:n-1] // trailing separator after the last page
	}
	return pages, nil
}

func nonSpaceChars(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

// renderPDFPages rasterizes pages first..last (1-based, 0 = all) and returns the
// PNG paths in page order plus a cleanup func.
func renderPDFPages(ctx context.Context, pdfPath string, first, last int) ([]string, func(), error) {
	return renderPDFPagesDPI(ctx, pdfPath, first, last, 150)
}

// renderPDFPagesDPI is renderPDFPages at an explicit resolution.
func renderPDFPagesDPI(ctx context.Context, pdfPath string, first, last, dpi int) ([]string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "gleann_docling_pdf_*")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	args := []string{"-png", "-r", strconv.Itoa(dpi)}
	if first > 0 {
		args = append(args, "-f", strconv.Itoa(first), "-l", strconv.Itoa(last))
	}
	args = append(args, pdfPath, filepath.Join(tmpDir, "page"))
	if err := exec.CommandContext(ctx, "pdftoppm", args...).Run(); err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("pdf rendering failed: %w", err)
	}

	matches, _ := filepath.Glob(filepath.Join(tmpDir, "page-*.png"))
	if len(matches) == 0 {
		cleanup()
		return nil, func() {}, fmt.Errorf("no page images generated by pdftoppm")
	}
	sort.Slice(matches, func(i, j int) bool { return extractPageNumber(matches[i]) < extractPageNumber(matches[j]) })
	return matches, cleanup, nil
}

// convertPDF decides per page: pages with a text layer are taken from
// pdftotext (milliseconds); only pages without one need OCR/VLM. Without the
// opt-in VLM those pages are skipped with a warning.
func (e *EIFDoclingEngine) convertPDF(ctx context.Context, pdfPath string) (string, int, error) {
	force := envTrue("GLEANN_DOCLING_FORCE_VLM")

	pages, err := pdfPageTexts(ctx, pdfPath)
	if err != nil {
		log.Printf("[docling-eif] pdftotext failed (%v)", err)
		pages = nil
	}

	if pages == nil || force {
		// No usable text layer information: rasterize everything.
		return e.convertPDFAllVLM(ctx, pdfPath)
	}

	result := make([]string, len(pages))
	var scanned []int // 0-based indexes of pages without text
	for i, t := range pages {
		if nonSpaceChars(t) >= minPageTextChars {
			result[i] = strings.TrimSpace(t)
		} else {
			scanned = append(scanned, i)
		}
	}

	if len(scanned) > 0 {
		if !e.canReadScanned() {
			log.Printf("[docling-eif] %s: %d of %d page(s) have no text layer and were skipped (OCR/VLM not enabled; set GLEANN_DOCLING_ONNX=1 or GLEANN_DOCLING_VLM=1)", filepath.Base(pdfPath), len(scanned), len(pages))
		} else if e.hybrid == nil && len(scanned) > 1 {
			// VLM only: render the scanned pages, then pipeline them through the model.
			var imgs []string
			var pageIdx []int
			for _, idx := range scanned {
				if ctx.Err() != nil {
					return "", 0, ctx.Err()
				}
				r, cleanup, err := renderPDFPagesDPI(ctx, pdfPath, idx+1, idx+1, e.pageDPI())
				if err != nil {
					log.Printf("[docling-eif] warning: page %d: %v", idx+1, err)
					continue
				}
				defer cleanup()
				imgs = append(imgs, r[0])
				pageIdx = append(pageIdx, idx)
			}
			mds, errs := e.convertImagesVLM(ctx, imgs)
			for k, idx := range pageIdx {
				if errs[k] != nil {
					if ctx.Err() != nil {
						return "", 0, ctx.Err()
					}
					log.Printf("[docling-eif] warning: failed page %d: %v", idx+1, errs[k])
					continue
				}
				result[idx] = mds[k]
			}
		} else {
			for _, idx := range scanned {
				if ctx.Err() != nil {
					return "", 0, ctx.Err()
				}
				imgs, cleanup, err := renderPDFPagesDPI(ctx, pdfPath, idx+1, idx+1, e.pageDPI())
				if err != nil {
					log.Printf("[docling-eif] warning: page %d: %v", idx+1, err)
					continue
				}
				md, err := e.convertPage(ctx, imgs[0])
				cleanup()
				if err != nil {
					log.Printf("[docling-eif] warning: failed page %d: %v", idx+1, err)
					continue
				}
				result[idx] = md
			}
		}
	}

	var kept []string
	for _, r := range result {
		if strings.TrimSpace(r) != "" {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		return "", 0, fmt.Errorf("no extractable text in %s: scanned PDF and OCR/VLM is not enabled (set GLEANN_DOCLING_ONNX=1 or GLEANN_DOCLING_VLM=1)", filepath.Base(pdfPath))
	}
	return strings.Join(kept, "\n\n"), len(pages), nil
}

func (e *EIFDoclingEngine) convertPDFAllVLM(ctx context.Context, pdfPath string) (string, int, error) {
	if !e.canReadScanned() {
		return "", 0, fmt.Errorf("cannot read %s: no text layer available and neither OCR nor the VLM is enabled (set GLEANN_DOCLING_ONNX=1 or GLEANN_DOCLING_VLM=1)", filepath.Base(pdfPath))
	}

	imgs, cleanup, err := renderPDFPagesDPI(ctx, pdfPath, 0, 0, e.pageDPI())
	if err != nil {
		return "", 0, err
	}
	defer cleanup()

	var pageMarkdowns []string
	if e.hybrid == nil {
		mds, errs := e.convertImagesVLM(ctx, imgs)
		for i, md := range mds {
			if errs[i] != nil {
				if ctx.Err() != nil {
					return "", 0, ctx.Err()
				}
				log.Printf("[docling-eif] warning: failed page %s: %v", imgs[i], errs[i])
				continue
			}
			pageMarkdowns = append(pageMarkdowns, md)
		}
	}
	for _, img := range imgs {
		if e.hybrid == nil {
			break
		}
		if ctx.Err() != nil {
			return "", 0, ctx.Err()
		}
		md, err := e.convertPage(ctx, img)
		if err != nil {
			log.Printf("[docling-eif] warning: failed page %s: %v", img, err)
			continue
		}
		pageMarkdowns = append(pageMarkdowns, md)
	}
	if len(pageMarkdowns) == 0 {
		return "", 0, fmt.Errorf("no content extracted from pdf pages")
	}
	return strings.Join(pageMarkdowns, "\n\n---\n\n"), len(imgs), nil
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

	execPath, _ := os.Executable()
	execDir := filepath.Dir(execPath)

	candidateDirs := []string{
		execDir,
		filepath.Join(execDir, "models"),
		filepath.Join(os.Getenv("HOME"), ".gleann", "models", "granite-docling"),
		filepath.Join(os.Getenv("HOME"), ".gleann", "models"),
		"/tmp/hf_test_model",
		"artifacts/granite_docling",
		"../../gleann/ext/eif-runtime/artifacts/granite_docling",
	}

	if env := os.Getenv("GLEANN_DOCLING_MODEL"); env != "" {
		if _, err := os.Stat(env); err == nil {
			tokCand := filepath.Join(filepath.Dir(env), "tokenizer.bin")
			if tokenizerPath == "" {
				if _, err := os.Stat(tokCand); err == nil {
					tokenizerPath = tokCand
				}
			}
			return env, tokenizerPath
		}
		candidateDirs = append([]string{filepath.Dir(env)}, candidateDirs...)
	}

	for _, dir := range candidateDirs {
		if dir == "" {
			continue
		}
		// Check exact name first
		exact := filepath.Join(dir, "granite_docling_bitnet_dense.eifm")
		if _, err := os.Stat(exact); err == nil {
			tokCand := filepath.Join(dir, "tokenizer.bin")
			if tokenizerPath == "" {
				if _, err := os.Stat(tokCand); err == nil {
					tokenizerPath = tokCand
				}
			}
			return exact, tokenizerPath
		}
		// Check glob with date suffix e.g. granite_docling_bitnet_dense-*.eifm
		if matches, _ := filepath.Glob(filepath.Join(dir, "granite_docling_bitnet_dense*.eifm")); len(matches) > 0 {
			match := matches[len(matches)-1] // latest match
			tokCand := filepath.Join(dir, "tokenizer.bin")
			if tokenizerPath == "" {
				if _, err := os.Stat(tokCand); err == nil {
					tokenizerPath = tokCand
				}
			}
			return match, tokenizerPath
		}
	}

	return "", ""
}

// cleanVLMText drops invalid UTF-8 from the VLM output. The decoder occasionally emits a lone
// continuation byte or a truncated multi-byte sequence (mostly on Turkish diacritics); passed on
// unchanged, that breaks JSON encoding and strict UTF-8 consumers of the converted markdown.
func cleanVLMText(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "")
}

func (e *EIFDoclingEngine) ensureLoadedLocked() error {
	if e.loaded {
		return nil
	}
	if vlmEnabled() && e.modelPath != "" {
		if err := e.loadModelLocked(); err != nil {
			return fmt.Errorf("failed to lazy-load VLM: %w", err)
		}
		return nil
	}
	return fmt.Errorf("image/scanned-page conversion needs OCR or the VLM, and neither is available (set GLEANN_DOCLING_ONNX=1 after `gleann-plugin-docs models download`, or GLEANN_DOCLING_VLM=1)")
}

type encodedPage struct {
	v  C.eif_llm_vision_input_t
	rc C.int
}

func (e *EIFDoclingEngine) encodePage(path string, threads int) *encodedPage {
	cImg := C.CString(path)
	defer C.free(unsafe.Pointer(cImg))
	p := &encodedPage{}
	p.rc = C.c_encode_page(&e.llm, cImg, C.int(threads), &p.v)
	return p
}

// convertImagesVLM converts page images with the VLM. With two or more pages it pipelines them: page i+1
// is encoded on another goroutine while page i is decoded. Results and errors are per page.
func (e *EIFDoclingEngine) convertImagesVLM(ctx context.Context, imgs []string) ([]string, []error) {
	out := make([]string, len(imgs))
	errs := make([]error, len(imgs))
	if len(imgs) < 2 || !pipelineEnabled() {
		for i, img := range imgs {
			if err := ctx.Err(); err != nil {
				errs[i] = err
				continue
			}
			out[i], errs[i] = e.convertImage(img)
		}
		return out, errs
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.ensureLoadedLocked(); err != nil {
		for i := range errs {
			errs[i] = err
		}
		return out, errs
	}

	budget := threadBudget()
	vitThreads, decThreads := pipelineThreads(budget)
	prompt := C.CString("Convert this page to docling.")
	defer C.free(unsafe.Pointer(prompt))

	cur := e.encodePage(imgs[0], budget)
	for i := range imgs {
		var next chan *encodedPage
		if i+1 < len(imgs) && ctx.Err() == nil {
			next = make(chan *encodedPage, 1)
			go func(path string) { next <- e.encodePage(path, vitThreads) }(imgs[i+1])
		}
		threads := budget
		if next != nil {
			threads = decThreads
		}
		switch {
		case ctx.Err() != nil:
			errs[i] = ctx.Err()
		case cur.rc != 0:
			errs[i] = fmt.Errorf("vision encoding of %s failed (code %d)", filepath.Base(imgs[i]), int(cur.rc))
		default:
			cRes := C.c_generate_page(&e.llm, &cur.v, prompt, 1024, C.float(repetitionPenalty()), C.int(threads))
			if cRes == nil {
				errs[i] = fmt.Errorf("vision inference returned null")
			} else {
				raw := cleanVLMText(C.GoString(cRes))
				C.free(unsafe.Pointer(cRes))
				if os.Getenv("GLEANN_DOCLING_DEBUG") == "1" {
					log.Printf("[docling-eif] raw doctags (%d bytes) for %s:\n%s", len(raw), imgs[i], raw)
				}
				out[i] = parser.DocTagsToMarkdown(raw)
			}
		}
		if cur.rc == 0 {
			C.eif_llm_vision_input_free(&cur.v)
		}
		if next != nil {
			cur = <-next
		}
	}
	return out, errs
}
