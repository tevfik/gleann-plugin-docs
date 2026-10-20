package engine

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "image/jpeg" // image decoder for .jpg/.jpeg inputs

	"github.com/tevfik/gleann-plugin-docs/internal/docparser"
)

// The ONNX hybrid page parser (layout model + PP-OCR) is the default reader for
// scanned pages and images (~0.5-1.2 s per page). It needs the ONNX Runtime
// shared library; without it the engine silently falls back to the Granite VLM
// (GLEANN_DOCLING_VLM=1, 10-17 s per page on CPU) or to "no OCR".
//
//	GLEANN_DOCLING_ONNX=0            disable the hybrid parser (default: on)
//	GLEANN_DOCLING_OCR_PROFILE       latin (default, includes Turkish) | chinese
//	GLEANN_DOCLING_DPI               rasterisation DPI for PDF pages (default 150)
//	GLEANN_DOCLING_ONNX_DEEP=1       send tables to the VLM (needs GLEANN_DOCLING_VLM=1)
//	GLEANN_DOCPARSER_OFFLINE=1       never download missing models
const (
	envONNX    = "GLEANN_DOCLING_ONNX"
	envProfile = "GLEANN_DOCLING_OCR_PROFILE"
	envDPI     = "GLEANN_DOCLING_DPI"
	envDeep    = "GLEANN_DOCLING_ONNX_DEEP"
	envOffline = "GLEANN_DOCPARSER_OFFLINE"

	defaultHybridDPI = 150
)

// onnxEnabled is true unless GLEANN_DOCLING_ONNX is explicitly 0/false/no/off.
func onnxEnabled() bool { return !envExplicitlyDisabled(envONNX) }

func hybridDPI() int {
	if v, err := strconv.Atoi(os.Getenv(envDPI)); err == nil && v >= 72 && v <= 600 {
		return v
	}
	return defaultHybridDPI
}

// initHybrid loads the ONNX parser when it is enabled. A failure is logged and
// leaves the engine without it: text layers, Office files and the VLM keep working.
func (e *EIFDoclingEngine) initHybrid() {
	if !onnxEnabled() {
		return
	}
	// On by default, so a machine without ONNX Runtime must not download models
	// or log errors; an explicit GLEANN_DOCLING_ONNX=1 still reports the problem.
	if _, err := docparser.FindORTLibrary(); err != nil {
		if envTrue(envONNX) {
			log.Printf("[docling-onnx] disabled: %v", err)
		} else {
			log.Printf("[docling-onnx] ONNX Runtime not found, scanned pages need GLEANN_DOCLING_VLM=1 (install libonnxruntime or set GLEANN_ORT_LIB)")
		}
		return
	}
	profile, err := docparser.ProfileByName(os.Getenv(envProfile))
	if err != nil {
		log.Printf("[docling-onnx] %v", err)
		return
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
	if envTrue(envDeep) && e.loaded {
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

// canReadScanned reports whether pages without a text layer can be read at all.
func (e *EIFDoclingEngine) canReadScanned() bool {
	e.mu.Lock()
	loaded := e.loaded
	e.mu.Unlock()
	return loaded || e.hybrid != nil
}

// convertPage reads one rendered page or image: the ONNX parser if enabled,
// else (or when it finds nothing and the VLM is loaded) the VLM.
func (e *EIFDoclingEngine) convertPage(ctx context.Context, imagePath string) (string, error) {
	if e.hybrid != nil {
		md, err := e.convertPageHybrid(ctx, imagePath)
		switch {
		case err != nil:
			log.Printf("[docling-onnx] %s: %v", filepath.Base(imagePath), err)
		case strings.TrimSpace(md) != "":
			return md, nil
		}
		e.mu.Lock()
		loaded := e.loaded
		e.mu.Unlock()
		if !loaded {
			return "", err // blank page (err == nil) or a real failure
		}
	}
	return e.convertImage(imagePath)
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
