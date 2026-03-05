# gleann-plugin-docs

Document extraction plugin for [gleann](https://github.com/tevfik/gleann). Converts PDF, DOCX, XLSX, PPTX and other binary document formats into **graph-ready** structured data that gleann ingests directly into KuzuDB + HNSW.

## How It Works

The plugin acts as a **document structure expert** — like the AST code indexer understands code symbols and call relationships, gleann-plugin-docs understands document structure: titles, sections, subsections, and their hierarchy.

```
                 Plugin                          gleann
   ┌──────────────────────────────┐    ┌───────────────────────────┐
   │  PDF/DOCX/XLSX/...          │    │                           │
   │       ↓                     │    │  nodes + edges            │
   │  MarkItDown / Docling       │    │       ↓                   │
   │       ↓                     │    │  KuzuDB (Document Graph)  │
   │  section_parser.py          │    │                           │
   │       ↓                     │    │  section content          │
   │  { nodes, edges }      ────────→│       ↓                   │
   │                             │    │  MarkdownChunker          │
   └──────────────────────────────┘    │       ↓                   │
                                       │  HNSW (Vector Index)     │
                                       └───────────────────────────┘
```

**Plugin response (`POST /convert`):**
```json
{
  "nodes": [
    {"_type": "Document", "path": "report.pdf", "title": "Q4 Report", "format": "pdf", ...},
    {"_type": "Section", "id": "doc:report.pdf:s0", "heading": "Introduction", "level": 1, "content": "...", ...},
    {"_type": "Section", "id": "doc:report.pdf:s0.0", "heading": "Background", "level": 2, "content": "...", ...}
  ],
  "edges": [
    {"_type": "HAS_SECTION", "from": "report.pdf", "to": "doc:report.pdf:s0"},
    {"_type": "HAS_SUBSECTION", "from": "doc:report.pdf:s0", "to": "doc:report.pdf:s0.0"}
  ]
}
```

## Supported Formats

`.pdf` `.docx` `.doc` `.xlsx` `.xls` `.pptx` `.ppt` `.png` `.jpg` `.jpeg` `.csv`

## Installation

**Requirements:** Python 3.10+

```bash
# 1. Clone and set up
git clone <this-repo> ~/.gleann/plugins/gleann-docs
cd ~/.gleann/plugins/gleann-docs
python3 -m venv venv
source venv/bin/activate
pip install -r requirements.txt

# 2. Register with gleann
python main.py --install

# Done! gleann will auto-start the plugin when needed.
```

**Optional — high-quality PDF processing with Docling:**
```bash
pip install -r requirements-docling.txt
```

> **Note:** After `--install`, **do not move this folder** — gleann saves the absolute path to auto-start the plugin. If you relocate it, run `python main.py --install` again.

## Usage

No manual server management needed. When `gleann build` encounters a PDF/DOCX/etc., it:

1. Checks if the plugin is running (`:8765/health`)
2. If not, auto-starts it using the registered command
3. Sends the file → receives graph-ready nodes/edges
4. Writes `Document` + `Section` nodes to KuzuDB (with `--graph`)
5. Chunks section content via `MarkdownChunker` → embeds to HNSW

```bash
# Index a directory with PDFs
gleann build myindex ./docs --graph

# The plugin starts automatically — no manual intervention
```

**Manual server (for debugging):**
```bash
python main.py --serve --port 8765
```

## Backends

| Format | Backend |
|--------|---------|
| `.pdf` | Docling (if installed) → fallback MarkItDown |
| Everything else | MarkItDown |

**Disabling Docling:**
```bash
python main.py --install --no-docling   # permanent
DOCLING_ENABLED=false python main.py    # one-time
```

**Performance:**
- MarkItDown: ~0.01s/page (fast, good enough for most documents)
- Docling: ~3.1s/page on CPU (better tables, OCR, layout analysis; 4-8 GB RAM)

