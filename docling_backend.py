"""Docling backend for gleann-plugin-docs.

Provides high-quality PDF-to-markdown conversion using IBM's Docling library.
Falls back gracefully if Docling is not installed.

Control via environment variable:
    DOCLING_ENABLED=false  → disable even if installed
    DOCLING_ENABLED=true   → enable (default when installed)
"""

import os
import logging

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


def convert_pdf(file_path: str) -> str:
    """Convert a PDF file to markdown using Docling.

    Args:
        file_path: Path to the PDF file.

    Returns:
        Markdown string of the document content.

    Raises:
        Exception: If conversion fails (caller should fallback to MarkItDown).
    """
    converter = _get_converter()
    result = converter.convert(file_path)
    return result.document.export_to_markdown()
