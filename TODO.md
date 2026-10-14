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

## Future Roadmap

- [ ] **Hardware Acceleration**: Enable Metal (macOS) and CUDA/Vulkan (Linux/Windows) inference backends in `eif-runtime`.
- [ ] **Streaming Page Conversion**: Stream extracted sections page-by-page over chunked HTTP for very large (1000+ page) documents.
- [ ] **Native XLSX / PPTX Parsers**: Add pure Go zero-dependency extraction for spreadsheets and slide decks.
- [ ] **Cross-Platform Prebuilt Binaries**: Automated GitHub Actions releases for Linux amd64/arm64, macOS arm64, and Windows amd64.
