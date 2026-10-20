# gleann-plugin-docs

High-performance, self-contained native document extraction plugin for [gleann](https://github.com/tevfik/gleann). Converts PDF, DOCX, XLSX, PPTX, CSV, and image documents into **graph-ready** structured data (`Document`, `Section`, `HAS_SECTION`, `HAS_SUBSECTION`) that gleann ingests directly into KùzuDB + HNSW.

Written in **pure Go** with an embedded **C inference runtime** ([`bitllm.c`](https://github.com/tevfik/bitllm.c), formerly `eif-runtime`). **Zero Python or PyTorch runtime dependencies** — compiles to a single, portable ~9.6MB binary.

## Architecture

The plugin acts as a **document structure expert** — like the AST code indexer understands code symbols and call relationships, `gleann-plugin-docs` understands document hierarchy: titles, sections, subsections, and tables.

```
                 Plugin                                       gleann
   ┌──────────────────────────────────────────┐    ┌───────────────────────────┐
   │  PDF / DOCX / XLSX / PPTX / Images       │    │                           │
   │       │                                  │    │  nodes + edges            │
   │       ├─→ Digital PDF: pdftotext (fast)  │    │       ↓                   │
   │       ├─→ Scanned/Raster: Docling BitNet │    │  KùzuDB (Document Graph)  │
   │       │   (1.58b VLM + OTSL table parser)│    │                           │
   │       └─→ DOCX: native Go XML parser     │    │  section content          │
   │       ↓                                  │    │       ↓                   │
   │  Parser & Hierarchy Builder              │    │  Table-Aware Chunker      │
   │       ↓                                  │    │       ↓                   │
   │  { nodes, edges }                   ─────────→│  HNSW (Vector Index)      │
   └──────────────────────────────────────────┘    └───────────────────────────┘
```

### Key Capabilities

1. **Embedded Granite Docling BitNet 1.58-bit VLM ([`tevfikk/granite-docling-258M-bitnet`](https://huggingface.co/tevfikk/granite-docling-258M-bitnet))**: In-process quantized vision-language model for OCR and OTSL (Open Table Structure Language) table layout parsing. Now compressed from 518 MB down to **264 MB** using BitNet ternary weights and INT8 per-row ViT projections in `bitllm.c`. Download with `gleann-plugin-docs models download --vlm`; it is used automatically when installed (`GLEANN_DOCLING_VLM=1` loads it at startup, `=0` disables it).
2. **Per-page PDF routing (automatic)**: every page is classified on its own.
   - *Digital page (has a text layer)*: text comes from `pdftotext` (**~0.05s**, exact). If the ONNX parser is installed, its layout model also looks for tables; each table is rebuilt as a Markdown table from the positions of the text-layer words (no OCR, so no recognition errors) and spliced into the page. Without the parser, tables stay flat text.
   - *Scanned page (no text layer)*: read by the Granite-Docling VLM (loaded on first use) for up to `GLEANN_DOCLING_VLM_MAX_PAGES` pages per document (default 20); beyond that, or when the VLM is not installed or fails, by the ONNX OCR parser. If neither is available the page is skipped with a warning.
3. **Pure Go Office parsers**: DOCX, XLSX, PPTX, HTML, EPUB are read in-process without third-party converters. Legacy `.xls` is read natively; `.doc` and `.ppt` are converted to their OOXML twin by a headless LibreOffice when one is installed. Pictures embedded in DOCX/PPTX are read with the page reader and appended as `## Embedded image: <name>` sections (not at their original position).
4. **Hierarchical Graph Generation**: Emits `Document` and nested `Section` nodes with `HAS_SECTION` and `HAS_SUBSECTION` edges ready for Gleann's KùzuDB knowledge graph.
5. **Table-Aware Extraction**: Tables are cleanly extracted in markdown table syntax, preserving tabular data integrity during chunking. OTSL tables with merged cells (`lcel`, `ucel`, `xcel`) are rendered as HTML tables with `colspan`/`rowspan`.
6. **Optional ONNX page parser (CPU, ~0.5–1 s/page)**: layout detection + PaddleOCR through ONNX Runtime for scanned pages and images (~23 MB total). Starts automatically when its models and `libonnxruntime` are installed (`GLEANN_DOCLING_ONNX=1` forces it on and downloads missing models, `=0` turns it off). See [ONNX_BACKEND.md](ONNX_BACKEND.md) for setup, measured accuracy and limits. `gleann-plugin-docs tui` is an interactive setup for it (engine, models, VLM, plugin registration); an optional Indra OCR engine is described there too.

---

## Supported Formats

| Format | Reader | Needs |
|---|---|---|
| `.pdf` | text layer (`pdftotext`); pages without text go to the page reader | `pdftoppm` for scanned pages |
| `.docx` `.xlsx` `.pptx` `.html` `.htm` `.epub` `.csv` `.txt` `.md` `.json` | native Go parsers | nothing |
| `.xls` | native Go BIFF8 reader (values, cached formula results, ISO dates); LibreOffice only as fallback for files it rejects | nothing |
| `.doc` `.ppt` | LibreOffice converts to OOXML, then as above | `soffice` (or `GLEANN_SOFFICE`); otherwise a clear error |
| `.png` `.jpg` `.jpeg` `.tif` `.tiff` `.bmp` `.webp` | page reader (ONNX OCR, or the VLM) | `libonnxruntime` or `GLEANN_DOCLING_VLM=1` |
| pictures inside `.docx` / `.pptx` | page reader, images smaller than 200 px skipped, at most 40 per file | same |

**Which page reader?** Scanned pages go to the Granite VLM first (up to `GLEANN_DOCLING_VLM_MAX_PAGES`, default 20), then to the ONNX parser; see the
per-page routing above. Measured on a bilingual two-column paper: the ONNX parser reaches ~68 % word recall in ~1 s/page, the VLM 12 % in 12-27 s/page
(it is weak on dense pages and on Turkish body text), while on clean English scans both read the page well. Set `GLEANN_DOCLING_VLM=0` to keep the
ONNX parser as the only reader. Not supported: scanned PDFs inside Office files, EMF/WMF/SVG pictures, password-protected files.

---

## Installation & Build

**Prerequisites:** Go 1.22+ and a standard C compiler (for `bitllm.c`). Optional: `pdftotext` and `pdftoppm` (poppler-utils) for PDFs, LibreOffice for `.doc/.xls/.ppt`, `libonnxruntime` for OCR.

```bash
# 1. Build the standalone binary
make build

# 2. Register with gleann (registers with priority 10 in ~/.gleann/plugins.json)
make install
```

Done! gleann will automatically detect and manage the plugin whenever document files are encountered.

---

## Updating

```bash
gleann-plugin-docs update --check   # is a newer GitHub release available?
gleann-plugin-docs update           # download, verify against checksums.txt, replace this binary
gleann plugin update docs           # the same, driven from gleann
```

Restart gleann (or `--serve`) afterwards; `update` reports missing OCR files and
`gleann-plugin-docs models download` fetches them.

## Usage

### 1. Automatic with Gleann
No manual server management needed. When `gleann index build` encounters a PDF/DOCX/etc., it automatically launches the registered plugin if not already running:

```bash
# Index a directory containing documents with GraphRAG support
gleann index build my_docs --docs ./documents --graph
```

### 2. Standalone CLI Conversion
You can inspect extracted markdown or graph structures directly from the command line:

```bash
# Convert a PDF to markdown
./bin/gleann-plugin-docs convert report.pdf

# Output JSON graph structure (nodes & edges)
./bin/gleann-plugin-docs convert report.pdf --format json
```

### 3. Running as a Daemon
Run the HTTP server manually for testing or microservice deployment:

```bash
./bin/gleann-plugin-docs --serve --port 8765
```

---

## HTTP API

The plugin implements Gleann's standard plugin protocol:

### `GET /health`
Returns `{"status": "ok", "plugin": "gleann-plugin-docs"}`.

### `GET /info`
Returns plugin capabilities and supported extensions:
```json
{
  "name": "gleann-plugin-docs",
  "version": "2.0.0",
  "extensions": [".pdf", ".docx", ".doc", ".xlsx", ".xls", ".pptx", ".ppt", ".epub", ".html", ".htm", ".png", ".jpg", ".jpeg", ".tif", ".tiff", ".bmp", ".webp", ".csv", ".txt", ".md"],
  "capabilities": ["document_extraction", "table_detection", "ocr"]
}
```

### `POST /convert`
Extracts structured hierarchy from a document.

**Request:** `multipart/form-data` with the document in the `file` field:
```bash
curl -F "file=@/path/to/report.pdf" http://localhost:<port>/convert
```

**Response:**
```json
{
  "nodes": [
    {"_type": "Document", "path": "report.pdf", "title": "Quarterly Report", "format": "pdf"},
    {"_type": "Section", "id": "doc:report.pdf:s0", "heading": "Executive Summary", "level": 1, "content": "..."},
    {"_type": "Section", "id": "doc:report.pdf:s0.0", "heading": "Key Financials", "level": 2, "content": "..."}
  ],
  "edges": [
    {"_type": "HAS_SECTION", "from": "report.pdf", "to": "doc:report.pdf:s0"},
    {"_type": "HAS_SUBSECTION", "from": "doc:report.pdf:s0", "to": "doc:report.pdf:s0.0"}
  ]
}
```

---

## ONNX page parser

```bash
make download-doc-models                                   # ~20 MB, SHA-256 verified
./bin/gleann-plugin-docs convert scan.pdf   # ONNX parser starts by itself once the models are installed
make bench-docparser PDF=paper.pdf PAGES=1-3               # accuracy/latency against the PDF's text layer
```

Needs `libonnxruntime` (set `GLEANN_ORT_LIB` or place it in `~/.gleann/lib`). Details, results and known
limits: [ONNX_BACKEND.md](ONNX_BACKEND.md).

## Running Tests & Benchmarks

```bash
# Run unit tests (parser, HTTP server, ONNX page parser)
make test

# Benchmark document extraction across the sample corpus (testdata/documents):
make bench

# Standard Go engine microbenchmarks:
make bench-engine

# The ONNX integration tests need the models and libonnxruntime; without them they skip
make download-doc-models

# Run all tests including E2E and VLM integration
make test-all
```

