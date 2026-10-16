# gleann-plugin-docs

High-performance, self-contained native document extraction plugin for [gleann](https://github.com/tevfik/gleann). Converts PDF, DOCX, XLSX, PPTX, CSV, and image documents into **graph-ready** structured data (`Document`, `Section`, `HAS_SECTION`, `HAS_SUBSECTION`) that gleann ingests directly into KùzuDB + HNSW.

Written in **pure Go** with an embedded **C inference runtime** (`eif-runtime`). **Zero Python or PyTorch runtime dependencies** — compiles to a single, portable ~9.6MB binary.

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

1. **Embedded Granite Docling BitNet 1.58-bit VLM**: In-process quantized vision-language model for OCR and OTSL (Open Table Structure Language) table layout parsing. No external Python server required.
2. **Hybrid PDF Fast-Path**: Searchable digital PDFs extract in **~0.05s** via `pdftotext`, while scanned, image-heavy, or raster pages automatically route through the Docling BitNet VLM.
3. **Pure Go DOCX/Office Parser**: Fast in-process XML unzipping and paragraph/heading extraction without third-party converters.
4. **Hierarchical Graph Generation**: Emits `Document` and nested `Section` nodes with `HAS_SECTION` and `HAS_SUBSECTION` edges ready for Gleann's KùzuDB knowledge graph.
5. **Table-Aware Extraction**: Tables are cleanly extracted in markdown table syntax, preserving tabular data integrity during chunking. OTSL tables with merged cells (`lcel`, `ucel`, `xcel`) are rendered as HTML tables with `colspan`/`rowspan`.
6. **Optional ONNX page parser (CPU, ~1–2 s/page)**: layout detection + PaddleOCR through ONNX Runtime for scanned pages and images, including Turkish. Off by default; enable with `GLEANN_DOCLING_ONNX=1`. See [ONNX_BACKEND.md](ONNX_BACKEND.md) for setup, measured accuracy and limits.

---

## Supported Formats

`.pdf` `.docx` `.doc` `.xlsx` `.xls` `.pptx` `.ppt` `.png` `.jpg` `.jpeg` `.csv` `.txt` `.md`

---

## Installation & Build

**Prerequisites:** Go 1.22+ and a standard C compiler (for `eif-runtime`). Optional: `pdftotext` (poppler-utils) for the PDF hybrid fast-path.

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
  "extensions": [".pdf", ".docx", ".doc", ".xlsx", ".xls", ".pptx", ".ppt", ".png", ".jpg", ".jpeg", ".csv", ".txt", ".md"],
  "capabilities": ["document_extraction", "table_detection", "ocr"]
}
```

### `POST /convert`
Extracts structured hierarchy from a document.

**Request:**
```json
{
  "path": "/absolute/path/to/report.pdf"
}
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
GLEANN_DOCLING_ONNX=1 ./bin/gleann-plugin-docs convert scan.pdf
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

