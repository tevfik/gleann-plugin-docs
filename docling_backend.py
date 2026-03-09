"""Docling backend for gleann-plugin-docs.

Provides high-quality PDF-to-markdown conversion using IBM's Docling library.
Falls back gracefully if Docling is not installed.

Control via environment variable:
    DOCLING_ENABLED=false  → disable even if installed
    DOCLING_ENABLED=true   → enable (default when installed)
"""

import os
import re
import logging
from typing import Optional

logger = logging.getLogger("gleann-plugin-docs.docling")

_converter = None
_docling_available = None


def is_available() -> bool:
    """Check if Docling is installed and enabled."""
    global _docling_available

    if os.environ.get("DOCLING_ENABLED", "").lower() == "false":
        return False

    if _docling_available is None:
        try:
            import docling  # noqa: F401
            _docling_available = True
        except ImportError:
            _docling_available = False

    return _docling_available


def _get_converter():
    """Lazy-initialize the DocumentConverter (first call takes ~2-3s for model loading)."""
    global _converter
    if _converter is None:
        from docling.document_converter import DocumentConverter
        logger.info("Initializing Docling DocumentConverter (first-time model load)...")
        _converter = DocumentConverter()
        logger.info("Docling DocumentConverter ready.")
    return _converter


def linkify_urls(markdown: str) -> str:
    """Convert bare URL patterns in text to proper markdown links.

    Handles:
      - http:// and https:// URLs
      - www. prefixed URLs (adds https://)
      - Skips URLs already inside markdown links [text](url)
    """
    # First, convert www. URLs that aren't already in a markdown link or preceded by ://
    # Negative lookbehind for ( or :// to avoid double-wrapping
    markdown = re.sub(
        r'(?<!\(|/)(?<!/)(www\.[a-zA-Z0-9._/~:?#\[\]@!$&\'()*+,;=-]+[a-zA-Z0-9/])',
        lambda m: f'[{m.group(1)}](https://{m.group(1)})',
        markdown,
    )

    # Convert bare http(s):// URLs not already inside markdown link syntax
    # Negative lookbehind for ]( to avoid re-wrapping
    markdown = re.sub(
        r'(?<!\]\()(?<!\()(https?://[a-zA-Z0-9._/~:?#\[\]@!$&\'()*+,;=-]+[a-zA-Z0-9/])',
        lambda m: f'[{m.group(1)}]({m.group(1)})',
        markdown,
    )

    return markdown


def extract_pdf_links(file_path: str) -> list[dict]:
    """Extract external hyperlinks from a PDF file using pdfplumber.

    Returns a list of dicts with keys: page, uri, x0, y0, x1, y1
    """
    links = []
    try:
        import pdfplumber
        with pdfplumber.open(file_path) as pdf:
            for page in pdf.pages:
                if not page.annots:
                    continue
                for annot in page.annots:
                    uri = annot.get("uri")
                    if uri:
                        links.append({
                            "page": page.page_number,
                            "uri": uri,
                        })
    except ImportError:
        logger.warning("pdfplumber not available for hyperlink extraction")
    except Exception as e:
        logger.warning("Failed to extract PDF hyperlinks: %s", e)
    return links


def convert_pdf(file_path: str, linkify: bool = True) -> dict:
    """Convert a PDF file to markdown using Docling.

    Args:
        file_path: Path to the PDF file.
        linkify: Whether to convert bare URLs to markdown links.

    Returns:
        Dict with keys:
          - markdown: The markdown content.
          - links: List of external hyperlinks extracted from PDF annotations.
          - meta: Document metadata (name, pages, tables, pictures count).

    Raises:
        Exception: If conversion fails (caller should fallback to MarkItDown).
    """
    converter = _get_converter()
    result = converter.convert(file_path)
    doc = result.document

    markdown = doc.export_to_markdown()
    if linkify:
        markdown = linkify_urls(markdown)

    # Extract metadata
    doc_dict = doc.export_to_dict()
    meta = {
        "name": doc_dict.get("name", ""),
        "pages": len(doc_dict.get("pages", {})),
        "texts": len(doc_dict.get("texts", [])),
        "tables": len(doc_dict.get("tables", [])),
        "pictures": len(doc_dict.get("pictures", [])),
    }

    # Extract external hyperlinks via pdfplumber
    links = extract_pdf_links(file_path)

    return {
        "markdown": markdown,
        "links": links,
        "meta": meta,
    }
