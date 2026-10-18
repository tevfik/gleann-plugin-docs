package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEIFDoclingEngine_TextAndCSV(t *testing.T) {
	tmpDir := t.TempDir()

	// Text file
	txtFile := filepath.Join(tmpDir, "test.txt")
	txtContent := "Hello world\n\nThis is a test document."
	if err := os.WriteFile(txtFile, []byte(txtContent), 0644); err != nil {
		t.Fatal(err)
	}

	eng := &EIFDoclingEngine{loaded: false}
	ctx := context.Background()

	md, pages, err := eng.Convert(ctx, txtFile)
	if err != nil {
		t.Fatalf("failed to convert txt: %v", err)
	}
	if pages != 1 {
		t.Errorf("expected 1 page, got %d", pages)
	}
	if md != txtContent {
		t.Errorf("expected %q, got %q", txtContent, md)
	}

	// CSV file
	csvFile := filepath.Join(tmpDir, "data.csv")
	csvContent := "item,price\nEdge TPU,$79\n"
	if err := os.WriteFile(csvFile, []byte(csvContent), 0644); err != nil {
		t.Fatal(err)
	}

	md, pages, err = eng.Convert(ctx, csvFile)
	if err != nil {
		t.Fatalf("failed to convert csv: %v", err)
	}
	if pages != 1 || !strings.Contains(md, "Edge TPU") {
		t.Errorf("unexpected csv output: %q", md)
	}
}

func TestEIFDoclingEngine_LiveInference(t *testing.T) {
	t.Setenv("GLEANN_DOCLING_VLM", "1")
	modelPath, tokPath := resolveModelPaths("", "")
	if modelPath == "" {
		t.Skip("skipping live inference test: model not found")
	}

	eng, err := NewEIFDoclingEngine(modelPath, tokPath)
	if err != nil {
		t.Fatalf("failed to load engine: %v", err)
	}
	defer eng.Close()

	if !eng.IsAvailable() {
		t.Fatalf("engine should be available")
	}
	if eng.BackendName() != "docling-eif" {
		t.Errorf("expected docling-eif backend, got %s", eng.BackendName())
	}

	// Test with testdata image
	testImg := "../../testdata/documents/scanned_table.png"
	if _, err := os.Stat(testImg); err == nil {
		ctx := context.Background()
		md, pages, err := eng.Convert(ctx, testImg)
		if err != nil {
			t.Fatalf("failed to convert test image: %v", err)
		}
		if pages != 1 {
			t.Errorf("expected 1 page, got %d", pages)
		}
		t.Logf("Inference result markdown:\n%s", md)
		if len(md) == 0 {
			t.Errorf("expected non-empty markdown output")
		}
	}
}
