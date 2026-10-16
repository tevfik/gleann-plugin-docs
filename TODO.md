# gleann-plugin-docs — Roadmap & Architecture Notes

## Completed Milestones (v2.0)

- [x] **Go + C Rewrite**: Replaced entire Python/PyTorch stack with standalone Go binary + `eif-runtime` C library.
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

- [ ] **ONNX backend on ARM64**: not tested yet; run `make bench-docparser` there and decide whether to enable it by default.
- [ ] **Granite deep path**: measure `GLEANN_DOCLING_ONNX_DEEP=1`; check that the `.eifm` model accepts box-guided prompts.
- [ ] **Better tables and layout for the ONNX path**: a table-structure model instead of the OCR grid, a layout model that knows references, headers and formulas.
- [ ] **Turkish ı/İ**: normalise or correct the recogniser's dotted/dotless confusion.

- [ ] **Hardware Acceleration**: Enable Metal (macOS) and CUDA/Vulkan (Linux/Windows) inference backends in `eif-runtime`.
- [ ] **Streaming Page Conversion**: Stream extracted sections page-by-page over chunked HTTP for very large (1000+ page) documents.
- [ ] **Native XLSX / PPTX Parsers**: Add pure Go zero-dependency extraction for spreadsheets and slide decks.
- [ ] **Cross-Platform Prebuilt Binaries**: Automated GitHub Actions releases for Linux amd64/arm64, macOS arm64, and Windows amd64.
