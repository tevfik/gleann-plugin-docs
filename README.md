# Gleann-Docs (MarkItDown Plugin)

This is a standalone Python HTTP service that acts as a **Plugin** for [Gleann](https://github.com/tevfik/gleann). It uses Microsoft's `markitdown` tool to parse and extract text from complex document formats (like PDF, DOCX, XLSX, etc) so that Gleann can index them into its RAG system.

## Supported Formats
`.pdf`, `.docx`, `.doc`, `.xlsx`, `.xls`, `.pptx`, `.ppt`, `.png`, `.jpg`, `.jpeg`, `.csv`

## Installation

1. Make sure you have Python 3.10+ installed.
2. Clone this repository to its permanent location and install dependencies:
```bash
python3 -m venv venv
source venv/bin/activate
pip install -r requirements.txt
```

3. **Register the Plugin with Gleann:**
```bash
python main.py --install
```
*This command updates `~/.gleann/plugins.json` and tells Gleann that this plugin exists, and importantly, registers its exact installation path (the `Command` instruction).*

> [!WARNING]
> Because `--install` saves the absolute paths of your current Python virtual environment and the `main.py` script, **you should not move or rename this folder after installing**. If you decide to move it, you must run `python main.py --install` again from the new location to update the paths in Gleann's registry.

## How it works

Because `gleann-docs` registers its execution command during the `--install` step, **you do not need to keep it running.** 

When you run `gleann build` on a directory containing PDFs or Word documents, Gleann will try to reach the plugin's port. If it is offline, Gleann will **automatically spawn the Python process in the background** and route the document through it seamlessly!

*(Note: If you want to run the server manually for debugging, you can use `python main.py --serve`)*
