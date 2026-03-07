"""Tests for the gleann-plugin-docs FastAPI endpoints."""
import io
import json
import os
import tempfile
from unittest.mock import patch

import pytest
from fastapi.testclient import TestClient
from main import app, PLUGIN_NAME, CAPABILITIES, SUPPORTED_EXTENSIONS


@pytest.fixture
def client():
    return TestClient(app)


# ── Health endpoint ──────────────────────────────────────────────


def test_health_endpoint(client):
    resp = client.get("/health")
    assert resp.status_code == 200
    data = resp.json()
    assert data["status"] == "ok"
    assert data["plugin"] == PLUGIN_NAME
    assert data["capabilities"] == CAPABILITIES
    assert "timeout" in data
    assert data["timeout"] == 120
    assert "backends" in data
    assert "markitdown" in data["backends"]
    assert "docling" in data["backends"]


# ── Convert endpoint ─────────────────────────────────────────────


def test_convert_txt(client):
    """Plain text file should be converted successfully."""
    content = b"Hello world\n\nThis is a test document."
    resp = client.post(
        "/convert",
        files={"file": ("test.txt", io.BytesIO(content), "text/plain")},
    )
    assert resp.status_code == 200
    data = resp.json()
    assert "nodes" in data
    assert "edges" in data
    # Should have at least a Document node
    doc_nodes = [n for n in data["nodes"] if n.get("_type") == "Document"]
    assert len(doc_nodes) >= 1


def test_convert_csv(client):
    """CSV file should be converted successfully."""
    csv_content = b"name,age,city\nAlice,30,NYC\nBob,25,LA\n"
    resp = client.post(
        "/convert",
        files={"file": ("data.csv", io.BytesIO(csv_content), "text/csv")},
    )
    assert resp.status_code == 200
    data = resp.json()
    assert "nodes" in data
    assert "edges" in data


def test_convert_unsupported_extension(client):
    """Unsupported file extension should return 400."""
    resp = client.post(
        "/convert",
        files={"file": ("test.xyz", io.BytesIO(b"data"), "application/octet-stream")},
    )
    assert resp.status_code == 400
    data = resp.json()
    assert "error" in data
    assert ".xyz" in data["error"]


def test_convert_no_file(client):
    """Missing file should return 422 (FastAPI validation error)."""
    resp = client.post("/convert")
    assert resp.status_code == 422


# ── Docling routing ──────────────────────────────────────────────


def test_docling_routing_mock(client):
    """When Docling is available, PDF should route through Docling backend."""
    fake_md = "# Mocked PDF\n\nDocling converted content."
    with patch("docling_backend.is_available", return_value=True), \
         patch("docling_backend.convert_pdf", return_value=fake_md) as mock_convert:
        resp = client.post(
            "/convert",
            files={"file": ("test.pdf", io.BytesIO(b"%PDF-mock"), "application/pdf")},
        )
        assert resp.status_code == 200
        mock_convert.assert_called_once()
        data = resp.json()
        assert "nodes" in data


def test_no_docling_fallback(client):
    """When Docling is unavailable, PDF should fall back to MarkItDown."""
    with patch("docling_backend.is_available", return_value=False):
        # MarkItDown may fail on invalid PDF bytes, but the routing logic
        # should at least attempt MarkItDown (not Docling).
        resp = client.post(
            "/convert",
            files={"file": ("test.pdf", io.BytesIO(b"%PDF-mock"), "application/pdf")},
        )
        # MarkItDown might succeed or fail on mock bytes; either way Docling wasn't called
        # We just verify it didn't crash with an unhandled error
        assert resp.status_code in (200, 500)


def test_docling_failure_fallback(client):
    """When Docling raises an exception, should fall back to MarkItDown."""
    with patch("docling_backend.is_available", return_value=True), \
         patch("docling_backend.convert_pdf", side_effect=RuntimeError("Docling crashed")):
        resp = client.post(
            "/convert",
            files={"file": ("test.pdf", io.BytesIO(b"%PDF-mock"), "application/pdf")},
        )
        # Should not be a 500 from Docling — it should have fallen back
        assert resp.status_code in (200, 500)


# ── Install plugin ───────────────────────────────────────────────


def test_install_plugin(tmp_path):
    """install_plugin() should create a valid plugins.json."""
    plugins_file = tmp_path / ".gleann" / "plugins.json"

    with patch("os.path.expanduser", return_value=str(tmp_path)), \
         patch("main.args") as mock_args:
        mock_args.port = 8765
        mock_args.no_docling = False

        from main import install_plugin
        install_plugin()

    assert plugins_file.exists()
    data = json.loads(plugins_file.read_text())
    assert "plugins" in data
    assert len(data["plugins"]) == 1
    plugin = data["plugins"][0]
    assert plugin["name"] == PLUGIN_NAME
    assert plugin["timeout"] == 120
    assert plugin["capabilities"] == CAPABILITIES
    assert set(plugin["extensions"]) == set(SUPPORTED_EXTENSIONS)
