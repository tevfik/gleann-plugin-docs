package engine

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestAnyDocSupported(t *testing.T) {
	for _, ext := range []string{".odt", ".ods", ".odp", ".rtf", ".docx", ".pptx", ".xlsx", ".epub"} {
		if !IsAnyDocSupported(ext) {
			t.Errorf("expected IsAnyDocSupported(%s) = true", ext)
		}
	}
}

func TestAnyDocConvert(t *testing.T) {
	ctx := context.Background()

	if HasInProcessAnyDoc {
		docxPath := "/home/tevfik/WORKSPACE/gleann/tests/e2e/fixtures/binary/technical_report.docx"
		if _, err := os.Stat(docxPath); err == nil {
			md, err := convertAnyDoc(ctx, docxPath)
			if err != nil {
				t.Fatalf("convertAnyDoc failed: %v", err)
			}
			if !strings.Contains(md, "Vector Database Performance Report") {
				t.Errorf("expected markdown to contain report title, got: %s", md[:min(len(md), 100)])
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
