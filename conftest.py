import pytest
from fastapi.testclient import TestClient
from main import app


@pytest.fixture
def client():
    """FastAPI TestClient fixture for endpoint testing."""
    return TestClient(app)
