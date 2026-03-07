import os
import sys
import json
import argparse
import logging
import tempfile
from fastapi import FastAPI, File, UploadFile
from fastapi.responses import JSONResponse
import uvicorn
from markitdown import MarkItDown
import docling_backend
import section_parser

# Define plugin identity
PLUGIN_NAME = "gleann-plugin-docs"
PLUGIN_URL = "http://localhost:8765"
CAPABILITIES = ["document-extraction"]
SUPPORTED_EXTENSIONS = [
    ".pdf", ".docx", ".doc", ".xlsx", ".xls", 
    ".pptx", ".ppt", ".png", ".jpg", ".jpeg", ".csv"
]

logger = logging.getLogger("gleann-plugin-docs")

app = FastAPI(title="Gleann Document Parser Plugin")
md = MarkItDown()

@app.get("/health")
def health():
    return {
        "status": "ok",
        "plugin": PLUGIN_NAME,
        "capabilities": CAPABILITIES,
        "timeout": 120,
        "backends": {
            "markitdown": True,
            "docling": docling_backend.is_available(),
        },
    }

@app.post("/convert")
async def convert_document(file: UploadFile = File(...)):
    """
    Accepts a multipart file upload, converts it to markdown, then
    returns graph-ready nodes and edges (like the AST code indexer).

    Response format:
    {
      "nodes": [
        {"_type": "Document", "path": "...", "title": "...", ...},
        {"_type": "Section", "id": "doc:...:s0", "heading": "...", "content": "...", ...},
        ...
      ],
      "edges": [
        {"_type": "HAS_SECTION", "from": "...", "to": "..."},
        {"_type": "HAS_SUBSECTION", "from": "...", "to": "..."},
        ...
      ]
    }
    """
    ext = os.path.splitext(file.filename)[1].lower()
    if ext not in SUPPORTED_EXTENSIONS:
        return JSONResponse(status_code=400, content={"error": f"Unsupported extension: {ext}"})

    try:
        tmp = tempfile.NamedTemporaryFile(delete=False, suffix=ext)
        content = await file.read()
        tmp.write(content)
        tmp.close()

        # Smart routing: PDF → Docling (if available), everything else → MarkItDown
        markdown = None
        if ext == ".pdf" and docling_backend.is_available():
            try:
                markdown = docling_backend.convert_pdf(tmp.name)
            except Exception as e:
                logger.warning("Docling failed, falling back to MarkItDown: %s", e)

        if markdown is None:
            result = md.convert(tmp.name)
            markdown = result.text_content

        os.unlink(tmp.name)

        # Parse into graph-ready structure
        graph = section_parser.parse_document(
            markdown,
            source_path=file.filename,
            doc_format=ext.lstrip("."),
        )
        return graph.to_dict()

    except Exception as e:
        if 'tmp' in locals() and os.path.exists(tmp.name):
            os.unlink(tmp.name)
        return JSONResponse(status_code=500, content={"error": str(e)})

def install_plugin():
    """
    Registers this plugin to ~/.gleann/plugins.json
    """
    home = os.path.expanduser("~")
    plugins_file = os.path.join(home, ".gleann", "plugins.json")
    
    # Read existing registry
    registry = {"plugins": []}
    if os.path.exists(plugins_file):
        try:
            with open(plugins_file, "r") as f:
                content = f.read()
                if content.strip():
                    registry = json.loads(content)
        except Exception as e:
            print(f"Error reading {plugins_file}: {e}")
            registry = {"plugins": []}
    
    # Remove old entry if exists (update).
    # Also remove legacy "gleann-docs" entries and any plugin on the same port to avoid conflicts.
    old_names = {PLUGIN_NAME, "gleann-docs"}
    registry["plugins"] = [
        p for p in registry.get("plugins", [])
        if p.get("name") not in old_names and p.get("url") != PLUGIN_URL
    ]
    
    # Add our plugin
    plugin_entry = {
        "name": PLUGIN_NAME,
        "url": PLUGIN_URL,
        "command": [sys.executable, os.path.abspath(__file__), "--serve", "--port", str(args.port)]
                   + (["--no-docling"] if args.no_docling else []),
        "capabilities": CAPABILITIES,
        "extensions": SUPPORTED_EXTENSIONS,
        "timeout": 120,
    }
    registry["plugins"].append(plugin_entry)
    
    # Make sure ~/.gleann exists
    os.makedirs(os.path.dirname(plugins_file), exist_ok=True)
    
    # Write back
    with open(plugins_file, "w") as f:
        json.dump(registry, f, indent=2)
        
    print(f"✅ Plugin '{PLUGIN_NAME}' registered successfully to {plugins_file}")
    print(f"   Capabilities: {CAPABILITIES}")
    print(f"   Supported Extensions: {len(SUPPORTED_EXTENSIONS)}")

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Gleann Document Parser Plugin")
    parser.add_argument("--install", action="store_true", help="Register this plugin with Gleann")
    parser.add_argument("--serve", action="store_true", help="Start the FastAPI server")
    parser.add_argument("--port", type=int, default=8765, help="Port to listen on (default: 8765)")
    parser.add_argument("--no-docling", action="store_true", help="Disable Docling backend, always use MarkItDown")
    args = parser.parse_args()

    if args.no_docling:
        os.environ["DOCLING_ENABLED"] = "false"

    if args.install:
        install_plugin()
    
    # If starting via `python main.py` or `python main.py --serve`
    if args.serve or not sys.argv[1:]:
        print(f"Starting {PLUGIN_NAME} server on port {args.port}...")
        # Update URL based on user port dynamically before registering if they want? 
        # We'll stick to 8765 in registry for simplicity of this script.
        uvicorn.run(app, host="127.0.0.1", port=args.port)
