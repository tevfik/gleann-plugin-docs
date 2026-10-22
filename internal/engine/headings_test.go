package engine

import (
	"strings"
	"testing"
)

func TestPromoteNumberedHeadings(t *testing.T) {
	in := "Title\n\n1. Introduction\n\nBody text of the section\nthat wraps here.\n\n2.1 Test Corpus\n\nMore.\n\n3. Results\n3.1 PDF Extraction\nAll three backends worked.\n"
	got := promoteNumberedHeadings(in)
	for _, want := range []string{"## 1. Introduction", "### 2.1 Test Corpus", "## 3. Results", "### 3.1 PDF Extraction"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "# Title") {
		t.Error("unnumbered title must stay plain")
	}
}

func TestPromoteNumberedHeadings_LeavesListsAndProse(t *testing.T) {
	in := "Steps to follow:\n\n1. Install the package\n2. Run the tool\n3. Check output\n\nThe value was\n4. Large increase over baseline here.\n\nIn 2024 Results improved.\n"
	if got := promoteNumberedHeadings(in); got != in {
		t.Fatalf("unexpected promotion:\n%s", got)
	}
}

func TestPromoteNumberedHeadings_SkipsQuantities(t *testing.T) {
	in := "Toplam PSRAM\n\n14 KB\n\n4 KB\n\n2 MHz\n\n3 V\n\nSon.\n"
	if got := promoteNumberedHeadings(in); got != in {
		t.Fatalf("quantities promoted to headings:\n%s", got)
	}
}

func TestPromoteNumberedHeadings_SkipsTOCEntries(t *testing.T) {
	in := "Contents\n\n1 Overview ........................ 4\n\n2.1 Specs . . . . . . . . 5\n\nBody.\n"
	if got := promoteNumberedHeadings(in); got != in {
		t.Fatalf("TOC entries promoted:\n%s", got)
	}
}
