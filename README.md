# Gleann-Plugin-Docs

This is a standalone Python HTTP service that acts as a **Plugin** for [Gleann](https://github.com/tevfik/gleann). It parses and extracts text from complex document formats (PDF, DOCX, XLSX, etc.) so that Gleann can index them into its RAG system.

**Backends:**
- **MarkItDown** (default) — Microsoft's fast document-to-markdown converter. Handles all supported formats.
- **Docling** (optional) — IBM's AI-powered document understanding. Provides superior PDF processing with table extraction (%97.9 accuracy), OCR, and layout analysis.

## Supported Formats
`.pdf`, `.docx`, `.doc`, `.xlsx`, `.xls`, `.pptx`, `.ppt`, `.png`, `.jpg`, `.jpeg`, `.csv`

## Installation

1. Make sure you have Python 3.10+ installed.
2. Clone this repository to its permanent location and install dependencies:
```bash
python3 -m venv venv
source venv/bin/activate
pip install -r requirements.txt

# Optional: Install Docling for high-quality PDF processing
pip install -r requirements-docling.txt
```

3. **Register the Plugin with Gleann:**
```bash
python main.py --install
```
*This command updates `~/.gleann/plugins.json` and tells Gleann that this plugin exists, and importantly, registers its exact installation path (the `Command` instruction).*

> [!WARNING]
> Because `--install` saves the absolute paths of your current Python virtual environment and the `main.py` script, **you should not move or rename this folder after installing**. If you decide to move it, you must run `python main.py --install` again from the new location to update the paths in Gleann's registry.

## How it works

Because `gleann-plugin-docs` registers its execution command during the `--install` step, **you do not need to keep it running.** 

When you run `gleann build` on a directory containing PDFs or Word documents, Gleann will try to reach the plugin's port. If it is offline, Gleann will **automatically spawn the Python process in the background** and route the document through it seamlessly!

*(Note: If you want to run the server manually for debugging, you can use `python main.py --serve`)*

## Docling (Advanced PDF Processing)

When Docling is installed, PDF files are automatically routed to Docling for higher-quality extraction. All other formats continue to use MarkItDown. If Docling fails on a particular PDF, it falls back to MarkItDown automatically.

**Smart Routing:**
| Format | Backend |
|---|---|
| `.pdf` | Docling (if installed) → fallback MarkItDown |
| `.docx`, `.xlsx`, `.pptx`, etc. | MarkItDown |
| `.png`, `.jpg`, `.csv` | MarkItDown |

**Disabling Docling:**

If Docling is installed but you want to use only MarkItDown, register the plugin with `--no-docling`:
```bash
# Register without Docling (gleann will always use MarkItDown for PDFs)
python main.py --install --no-docling

# Re-enable Docling later
python main.py --install
```

The `--no-docling` flag is saved into the plugin's auto-start command in `~/.gleann/plugins.json`, so it persists across `gleann build` runs. You can also set the `DOCLING_ENABLED=false` environment variable for manual runs.

**Performance Notes:**
- Docling uses ~3.1s/page on CPU (vs MarkItDown's ~0.01s/page)
- First request takes extra ~2-3s for model initialization
- Requires 4-8 GB RAM when processing PDFs
- No GPU required (CPU-only is fully supported)
