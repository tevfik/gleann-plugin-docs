#!/usr/bin/env bash
# ==============================================================================
# perf_benchmark.sh — End-to-End Performance & Accuracy Benchmark Runner
# Measures document extraction, layout detection, OCR, and graph generation
# across all supported formats (PDF, Scanned PDF, Images, DOCX, XLSX, PPTX, EPUB, HTML, CSV, MD).
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Defaults
ITER=3
DIR="testdata/documents"
JSON_OUTPUT=false
ENABLE_ONNX=1

usage() {
    cat <<EOF
Usage: ./perf_benchmark.sh [OPTIONS]

Options:
  -n, --iter <N>       Number of iterations per document (default: 3)
  -d, --dir <DIR>      Path to document directory (default: testdata/documents)
      --json           Output results in JSON format
      --no-ocr         Disable ONNX OCR (fast-path text extraction only)
  -h, --help           Show this help message

Examples:
  ./perf_benchmark.sh                   # Run full benchmark with ONNX layout & OCR
  ./perf_benchmark.sh -n 5              # Average over 5 runs
  ./perf_benchmark.sh --no-ocr          # Run digital document extraction only
  ./perf_benchmark.sh --json > report.json
EOF
    exit 0
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        -n|--iter)
            ITER="$2"
            shift 2
            ;;
        -d|--dir)
            DIR="$2"
            shift 2
            ;;
        --json)
            JSON_OUTPUT=true
            shift
            ;;
        --no-ocr)
            ENABLE_ONNX=0
            shift
            ;;
        -h|--help)
            usage
            ;;
        *)
            echo "Unknown option: $1" >&2
            usage
            ;;
    esac
done

if [ "$JSON_OUTPUT" = false ]; then
    echo "========================================================================"
    echo "  gleann-plugin-docs: Document Extraction & Graph Benchmark"
    echo "========================================================================"
    echo "Target Directory: $DIR"
    echo "Iterations      : $ITER"
fi

# 1. Ensure bitllm.c and binary are built
if [ ! -f "bin/gleann-plugin-docs" ]; then
    if [ "$JSON_OUTPUT" = false ]; then
        echo "Building gleann-plugin-docs binary..."
    fi
    make build >/dev/null 2>&1
fi

# 2. Check ONNX runtime & models if OCR is enabled
if [ "$ENABLE_ONNX" -eq 1 ]; then
    export GLEANN_DOCLING_ONNX=1

    # Check ONNX shared library
    if [ -z "${GLEANN_ORT_LIB:-}" ]; then
        if [ -f "$HOME/.gleann/lib/libonnxruntime.so" ] || [ -f "$HOME/.gleann/lib/libonnxruntime.so.1.26.0" ]; then
            export GLEANN_ORT_LIB="$HOME/.gleann/lib/libonnxruntime.so.1.26.0"
        fi
    fi

    # Check models directory
    MODEL_DIR="${GLEANN_DOCPARSER_MODELS:-$HOME/.gleann/models/docparser}"
    if [ ! -f "$MODEL_DIR/layout_publaynet.onnx" ]; then
        if [ "$JSON_OUTPUT" = false ]; then
            echo "Downloading ONNX layout and OCR models to $MODEL_DIR..."
        fi
        make download-doc-models >/dev/null 2>&1
    fi
    if [ "$JSON_OUTPUT" = false ]; then
        echo "ONNX Layout & OCR : ENABLED (CPU, PaddleOCR PP-OCRv5 + PicoDet)"
    fi
else
    export GLEANN_DOCLING_ONNX=0
    if [ "$JSON_OUTPUT" = false ]; then
        echo "ONNX Layout & OCR : DISABLED (Digital text-layer extraction only)"
    fi
fi

if [ "$JSON_OUTPUT" = false ]; then
    echo "Running benchmark..."
    echo "------------------------------------------------------------------------"
fi

# 3. Execute docs-bench
if [ "$JSON_OUTPUT" = true ]; then
    go run ./cmd/docs-bench -dir "$DIR" -iter "$ITER" -json
else
    go run ./cmd/docs-bench -dir "$DIR" -iter "$ITER"
fi
