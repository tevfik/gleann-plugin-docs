# gleann-plugin-docs — Roadmap & Architecture Notes

## Completed Milestones (v2.0)

- [x] **Go + C Rewrite**: Replaced entire Python/PyTorch stack with standalone Go binary + `bitllm.c` C library (formerly `eif-runtime`).
- [x] **Granite Docling BitNet 1.58b VLM**: In-process quantized vision-language model for OCR and OTSL table parsing.
- [x] **Pure Go DOCX/Office Support**: Native zip/XML parser for `.docx` without external dependencies.
- [x] **Hybrid PDF Extraction**: Fast-path for searchable digital PDFs via `pdftotext` (~0.05s) with automatic fallback to Docling BitNet VLM for scanned/raster pages.
- [x] **Priority Registration**: Automated `--install` command registering with priority 10 in `~/.gleann/plugins.json`.
- [x] **HTTP Daemon & CLI**: Dual-mode binary (`--serve --port 8765` and `convert <file>`).
- [x] **Hierarchical Graph Export**: Emits `Document` and `Section` nodes with `HAS_SECTION` / `HAS_SUBSECTION` edges.

---

## Added: ONNX page parser (opt-in)

- [x] **Layout + OCR through ONNX Runtime** (`internal/docparser`): PicoDet layout model, PP-OCRv5 latin / PP-OCRv4 chinese, coverage safety net, XY-cut reading order, OCR-grid table fallback. See `ONNX_BACKEND.md`.
- [x] **OTSL state machine** with merged cells (`internal/parser/otsl.go`); the old converter dropped `lcel`/`ucel`/`xcel` cells and shifted columns.
- [x] **Model downloader** (`models download|verify|list`, `make download-doc-models`), `-tags embedmodels` build.

## Future Roadmap

- [~] **ONNX backend on ARM64**: works (2026-10-19, 20-core ARM, onnxruntime 1.31 via `GLEANN_ORT_LIB`): ~0.55 s/page on `benchmark_report.pdf` (word recall 98.3%), 1.2 s/page on `academic_paper.pdf` (68.2%, 76.9% after Turkish ASCII folding). Granite-Docling VLM on the same academic paper: ~0.09 recall, ~40 s/page. Decision on default still open. `scanned_report.pdf` scores 0% only because it has no text layer to compare against.
- [ ] **Granite deep path**: measure `GLEANN_DOCLING_ONNX_DEEP=1`. Before bitllm.c `b94202e` the runtime ignored the prompt (always the page prompt) and forced a 4x3 tile grid, so table crops sent by `eifDeep` were mis-read (FinTabNet cell F1 0.39 -> 0.78 after the fix); re-measure with the new submodule.
- [ ] **Better tables and layout for the ONNX path**: a table-structure model instead of the OCR grid, a layout model that knows references, headers and formulas.
- [ ] **Turkish ı/İ**: normalise or correct the recogniser's dotted/dotless confusion.

- [ ] **Hardware Acceleration**: Enable Metal (macOS) and CUDA/Vulkan (Linux/Windows) inference backends in `bitllm.c` (it has an experimental Vulkan GEMM behind `--gpu`; the ViT dominates page latency).
- [ ] **Streaming Page Conversion**: Stream extracted sections page-by-page over chunked HTTP for very large (1000+ page) documents.
- [x] **Native XLSX / PPTX Parsers**: pure Go `internal/engine/xlsx.go`, `pptx.go` (tests in `office_extract_test.go`).
- [~] **Cross-Platform Prebuilt Binaries**: `release.yml` builds Linux amd64/arm64 and macOS arm64; Windows amd64 is missing.
