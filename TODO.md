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
- [x] **ONNX default, legacy Office, TIFF/BMP/WebP, embedded images** (2026-10-19): ONNX page parser on by default (`GLEANN_DOCLING_ONNX=0` to disable), `.doc/.ppt` via LibreOffice, `.xls` native (2026-10-20, pure-Go BIFF8 reader `xls.go`, no LibreOffice), extra raster formats, text from pictures inside DOCX/PPTX. Open: embedded images are appended, not placed in reading order; EMF/WMF unsupported.
- [x] **Granite deep path measured** (2026-10-20, bitllm.c cef6d8a): `GLEANN_DOCLING_ONNX_DEEP=1` does not pay off. On the 2-page benchmark report (as images) word recall 0.984 -> 0.989 but precision 0.945 -> 0.654 and 1.5 s -> 10 s: the VLM re-emits the whole page for the table box (duplicated sections), so the box-guided prompt is not honoured. On the dense paper no table is detected, result identical. Stays opt-in, not recommended; fix needs a crop-to-box before the VLM call.
- [ ] **Better tables and layout for the ONNX path**: a table-structure model instead of the OCR grid, a layout model that knows references, headers and formulas.
- [ ] **Turkish ı/İ**: normalise or correct the recogniser's dotted/dotless confusion.

- [~] **CPU speed (2026-10-19, bitllm.c `cef6d8a`)**: parallel decode attention (decode 11 -> 4.2 s/page), loop guard (looping Turkish page 40 -> 16 s), page pipelining (`GLEANN_DOCLING_PIPELINE`, ~1.1-1.3x on multi-page), INT8 i8mm ViT (ViT 2.3x quiet), OpenMP team size now set per cgo call (was lost: 2 pages 130 -> 42 s). Page budget on an idle 20-core ARM is now roughly ViT 3.6-5 s + prefill 2.6 s + decode 4 s.
- [ ] **Hardware Acceleration**: Vulkan in `bitllm.c` works on NVIDIA GB10 (`-DBITLLM_ENABLE_VULKAN=ON`, needs Vulkan headers), output identical to CPU, but the current shader copies operands per call and reaches only ~320 GFLOPS (CPU-level). Needs device-resident weights, the whole ViT layer on the GPU and a tiled / cooperative-matrix shader before it pays off. Metal (macOS) not started.
- [ ] **Streaming Page Conversion**: Stream extracted sections page-by-page over chunked HTTP for very large (1000+ page) documents.
- [x] **Native XLSX / PPTX Parsers**: pure Go `internal/engine/xlsx.go`, `pptx.go` (tests in `office_extract_test.go`).
- [~] **Cross-Platform Prebuilt Binaries**: `release.yml` builds Linux amd64/arm64 and macOS arm64; Windows amd64 is missing.
