package engine

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	_ "image/jpeg" // image decoder for .jpg/.jpeg inputs

	"github.com/tevfik/gleann-plugin-docs/internal/docparser"
	"github.com/tevfik/gleann-plugin-docs/internal/indra"
)

// pageParser is what the engine needs from an ONNX page parser. Both
// docparser.Parser (layout model + PP-OCR) and indra.Parser satisfy it.
type pageParser interface {
	ParsePage(ctx context.Context, img image.Image) (string, error)
	TableBoxes(img image.Image) ([]image.Rectangle, error)
	Close() error
}

// The ONNX hybrid page parser (layout model + PP-OCR) does two jobs:
//   - on digital PDF pages it finds tables (the text layer alone flattens them),
//     and the page is then rebuilt from the layout blocks;
//   - on scanned pages it is the fast OCR fallback when the Granite VLM
//     (10-17 s per page on CPU) is unavailable or over its page budget.
//
// The engine picks the route per page; see convertPDF. It starts by itself when
// its models and libonnxruntime are installed.
//
//	GLEANN_DOCLING_ONNX=1            force it on (downloads missing models)
//	GLEANN_DOCLING_ONNX=0            force it off (default: on when installed)
//	GLEANN_DOCLING_OCR_PROFILE       latin (default, includes Turkish) | chinese
//	GLEANN_DOCLING_DPI               rasterisation DPI for PDF pages (default 150)
//	GLEANN_DOCLING_ONNX_DEEP=1       send tables to the VLM (needs GLEANN_DOCLING_VLM=1)
//	GLEANN_DOCPARSER_OFFLINE=1       never download missing models
//	GLEANN_DOCLING_ONNX_ENGINE=indra use Datalab's Indra OCR models instead of layout + PP-OCR
//	GLEANN_INDRA_MODELS              Indra model directory (default ~/.gleann/models/indra)
const (
	envEngine   = "GLEANN_DOCLING_ONNX_ENGINE"
	envIndraDir = "GLEANN_INDRA_MODELS"
	envONNX     = "GLEANN_DOCLING_ONNX"
	envProfile  = "GLEANN_DOCLING_OCR_PROFILE"
	envDPI      = "GLEANN_DOCLING_DPI"
	envDeep     = "GLEANN_DOCLING_ONNX_DEEP"
	envOffline  = "GLEANN_DOCPARSER_OFFLINE"

	defaultHybridDPI = 150
)

// onnxEnabled reports an explicit request for the parser; onnxDisabled an
// explicit refusal. With neither it starts only if already installed.
func onnxEnabled() bool  { return envTrue(envONNX) }
func onnxDisabled() bool { return envExplicitlyDisabled(envONNX) }

// onnxInstalled reports whether libonnxruntime and every model of the profile
// are present, so the parser can start without downloading anything.
func onnxInstalled(profile docparser.OCRProfile) bool {
	if _, err := docparser.FindORTLibrary(); err != nil {
		return false
	}
	for _, m := range docparser.DefaultModels(profile) {
		if docparser.VerifyModel(docparser.ModelDir(), m) != nil {
			return false
		}
	}
	return true
}

func hybridDPI() int {
	if v, err := strconv.Atoi(os.Getenv(envDPI)); err == nil && v >= 72 && v <= 600 {
		return v
	}
	return defaultHybridDPI
}

// initHybrid loads the ONNX parser when it is enabled. A failure is logged and
// leaves the engine without it: text layers, Office files and the VLM keep working.
func (e *EIFDoclingEngine) initHybrid() {
	if onnxDisabled() {
		return
	}
	if strings.EqualFold(os.Getenv(envEngine), "indra") {
		e.initIndra()
		return
	}
	profile, err := docparser.ProfileByName(os.Getenv(envProfile))
	if err != nil {
		if onnxEnabled() {
			log.Printf("[docling-onnx] %v", err)
		}
		return
	}
	if !onnxEnabled() && !onnxInstalled(profile) {
		return // automatic mode: only when it is already installed
	}

	if !envTrue(envOffline) {
		specs := docparser.DefaultModels(profile)
		err := docparser.EnsureModels(context.Background(), docparser.ModelDir(), specs, func(s string) {
			if strings.HasPrefix(s, "downloaded") {
				log.Printf("[docling-onnx] %s", s)
			}
		})
		if err != nil {
			log.Printf("[docling-onnx] model download failed: %v", err)
		}
	}

	opts := docparser.Options{Profile: profile.Name}
	if envTrue(envDeep) && e.vlmUsable() {
		opts.Deep = &eifDeep{e: e}
	}
	p, err := docparser.New(opts)
	if err != nil {
		log.Printf("[docling-onnx] disabled: %v", err)
		return
	}
	e.hybrid = p
	log.Printf("[docling-onnx] ready (profile=%s, %d dpi, deep tables=%v)", profile.Name, hybridDPI(), opts.Deep != nil)
}

// indraDir is the directory holding det.onnx, rec.onnx, order.onnx and meta.json.
func indraDir() string {
	if d := os.Getenv(envIndraDir); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".gleann", "models", "indra")
}

// initIndra starts the Indra OCR parser. The models are not downloaded
// automatically (their license differs from the code's: read it first); a
// missing directory or runtime is logged and leaves the engine without it.
func (e *EIFDoclingEngine) initIndra() {
	dir := indraDir()
	threads := min(4, runtime.NumCPU())
	p, err := indra.NewParser(dir, threads)
	if err != nil {
		log.Printf("[docling-indra] disabled: %v (models expected in %s, see %s)", err, dir, envIndraDir)
		return
	}
	e.hybrid = p
	log.Printf("[docling-indra] ready (%s, %d dpi)", dir, hybridDPI())
}

// canReadScanned reports whether pages without a text layer can be read at all.
func (e *EIFDoclingEngine) canReadScanned() bool {
	return e.hybrid != nil || e.vlmUsable()
}

// readScanned reads a page or image that has no text layer. Granite-Docling is
// the primary reader (it also recovers tables and formulas); the ONNX parser
// takes over when the VLM is unavailable, fails, finds nothing, or allowVLM is
// false (page budget spent). usedVLM reports whether the VLM did the work.
func (e *EIFDoclingEngine) readScanned(ctx context.Context, imagePath string, allowVLM bool) (md string, usedVLM bool, err error) {
	if allowVLM && e.vlmUsable() {
		md, err = e.convertImage(imagePath)
		if err == nil && strings.TrimSpace(md) != "" {
			return md, true, nil
		}
		if err != nil {
			log.Printf("[docling-eif] %s: VLM failed: %v", filepath.Base(imagePath), err)
		}
	}
	if e.hybrid != nil {
		md, err = e.convertPageHybrid(ctx, imagePath)
		if err != nil {
			log.Printf("[docling-onnx] %s: %v", filepath.Base(imagePath), err)
		}
		return md, false, err
	}
	if err == nil {
		err = fmt.Errorf("neither OCR nor the VLM is available for %s (install the ONNX models and set GLEANN_DOCLING_ONNX=1, or install the VLM and set GLEANN_DOCLING_VLM=1)", filepath.Base(imagePath))
	}
	return "", false, err
}

// convertPage reads an image file (png/jpg) without a page budget.
func (e *EIFDoclingEngine) convertPage(ctx context.Context, imagePath string) (string, error) {
	md, _, err := e.readScanned(ctx, imagePath, true)
	return md, err
}

func (e *EIFDoclingEngine) convertPageHybrid(ctx context.Context, imagePath string) (string, error) {
	img, err := decodeImage(imagePath)
	if err != nil {
		return "", err
	}
	return e.hybrid.ParsePage(ctx, img)
}

func decodeImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", filepath.Base(path), err)
	}
	return img, nil
}

// eifDeep sends table regions to Granite-Docling through eif-runtime with a
// box-guided prompt. It is only wired in when GLEANN_DOCLING_ONNX_DEEP=1.
type eifDeep struct {
	e *EIFDoclingEngine
}

func (d *eifDeep) Recognize(ctx context.Context, page image.Image, box docparser.BoundingBox) (string, error) {
	prompt, err := deepPrompt(box)
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp("", "gleann_deep_*.png")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := png.Encode(tmp, page); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return d.e.runVision(tmp.Name(), prompt)
}

// pageDPI is the resolution PDF pages are rasterised at. The latin OCR profile
// reads 150 dpi as well as 300 (see ONNX_BACKEND.md), so both paths default to
// 150; GLEANN_DOCLING_DPI raises it for the OCR path, e.g. for the chinese profile.
func (e *EIFDoclingEngine) pageDPI() int {
	if e.hybrid != nil {
		return hybridDPI()
	}
	return 150
}

// deepPrompt builds the box-guided Granite-Docling prompt for a region; the
// coordinates are on the model's 0..500 grid.
func deepPrompt(box docparser.BoundingBox) (string, error) {
	n := box.NormCoords
	loc := fmt.Sprintf("<loc_%d><loc_%d><loc_%d><loc_%d>", n[0], n[1], n[2], n[3])
	switch box.Type {
	case docparser.TypeTable:
		return "Convert table at " + loc + " to OTSL.", nil
	case docparser.TypeChart, docparser.TypeFigure:
		return "Convert chart at " + loc + " to markdown table.", nil
	case docparser.TypeFormula:
		return "Convert formula at " + loc + " to latex.", nil
	}
	return "", fmt.Errorf("deep recogniser does not handle %s blocks", box.Type)
}
