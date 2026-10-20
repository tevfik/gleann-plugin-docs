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

1. **Embedded Granite Docling BitNet 1.58-bit VLM ([`tevfikk/granite-docling-258M-bitnet`](https://huggingface.co/tevfikk/granite-docling-258M-bitnet))**: In-process quantized vision-language model for OCR and OTSL (Open Table Structure Language) table layout parsing. Now compressed from 518 MB down to **264 MB** using BitNet ternary weights and INT8 per-row ViT projections in `bitllm.c`. Download with `gleann-plugin-docs models download --vlm` and enable with `GLEANN_DOCLING_VLM=1`.
2. **Hybrid PDF Fast-Path**: Searchable digital PDFs extract in **~0.05s** via `pdftotext`, while scanned or raster pages are read by the ONNX page parser (default, ~1 s/page) or, when `GLEANN_DOCLING_VLM=1`, by the Docling BitNet VLM.
3. **Pure Go Office parsers**: DOCX, XLSX, PPTX, HTML, EPUB are read in-process without third-party converters. Legacy `.doc` `.xls` `.ppt` are converted to their OOXML twin by a headless LibreOffice when one is installed. Pictures embedded in DOCX/PPTX are read with the page reader and appended as `## Embedded image: <name>` sections (not at their original position).
4. **Hierarchical Graph Generation**: Emits `Document` and nested `Section` nodes with `HAS_SECTION` and `HAS_SUBSECTION` edges ready for Gleann's KùzuDB knowledge graph.
5. **Table-Aware Extraction**: Tables are cleanly extracted in markdown table syntax, preserving tabular data integrity during chunking. OTSL tables with merged cells (`lcel`, `ucel`, `xcel`) are rendered as HTML tables with `colspan`/`rowspan`.
6. **ONNX page parser, on by default (CPU, ~0.5–1.2 s/page)**: layout detection + PaddleOCR through ONNX Runtime for scanned pages and images (~23 MB of models, downloaded on first use). Turn it off with `GLEANN_DOCLING_ONNX=0`. Without `libonnxruntime` it stays out of the way (no download, one log line). See [ONNX_BACKEND.md](ONNX_BACKEND.md) for setup, measured accuracy and limits.

---

## Supported Formats

| Format | Reader | Needs |
|---|---|---|
| `.pdf` | text layer (`pdftotext`); pages without text go to the page reader | `pdftoppm` for scanned pages |
| `.docx` `.xlsx` `.pptx` `.html` `.htm` `.epub` `.csv` `.txt` `.md` `.json` | native Go parsers | nothing |
| `.doc` `.xls` `.ppt` | LibreOffice converts to OOXML, then as above | `soffice` (or `GLEANN_SOFFICE`); otherwise a clear error |
| `.png` `.jpg` `.jpeg` `.tif` `.tiff` `.bmp` `.webp` | page reader (ONNX OCR, or the VLM) | `libonnxruntime` or `GLEANN_DOCLING_VLM=1` |
| pictures inside `.docx` / `.pptx` | page reader, images smaller than 200 px skipped, at most 40 per file | same |

**Which page reader?** The ONNX parser is the default: on the 1-page scanned test report it recovered 133 of 134 distinct words in 1.2 s,
and it reads dense and Turkish pages far better than the VLM (68 % vs 9 % word recall on a bilingual two-column paper). The Granite VLM
(`GLEANN_DOCLING_VLM=1`) takes 12-16 s per page and is mainly useful for table crops and clean English scans; it is weak on dense pages and
Turkish body text. Not supported: scanned PDFs inside Office files, EMF/WMF/SVG pictures, password-protected files.

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
./bin/gleann-plugin-docs convert scan.pdf                 # ONNX is the default
GLEANN_DOCLING_ONNX=0 GLEANN_DOCLING_VLM=1 ./bin/gleann-plugin-docs convert scan.pdf   # VLM only
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

