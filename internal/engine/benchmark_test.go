package engine

import (
	"context"
	"path/filepath"
	"testing"
)

func BenchmarkExtractHTML(b *testing.B) {
	docPath := filepath.Join("..", "..", "testdata", "documents", "quantum_primer.html")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ExtractHTMLFile(docPath)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExtractEPUB(b *testing.B) {
	docPath := filepath.Join("..", "..", "testdata", "documents", "distributed_systems.epub")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := ExtractEPUB(docPath)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExtractDOCX(b *testing.B) {
	docPath := filepath.Join("..", "..", "testdata", "documents", "technical_report.docx")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ExtractDocx(docPath)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExtractXLSX(b *testing.B) {
	docPath := filepath.Join("..", "..", "testdata", "documents", "benchmark_data.xlsx")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := ExtractXLSX(docPath)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExtractPPTX(b *testing.B) {
	docPath := filepath.Join("..", "..", "testdata", "documents", "research_slides.pptx")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := ExtractPPTX(docPath)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConvertPDF(b *testing.B) {
	docPath := filepath.Join("..", "..", "testdata", "documents", "benchmark_report.pdf")
	eng, err := NewEIFDoclingEngine("", "")
	if err != nil {
		b.Fatal(err)
	}
	defer eng.Close()

	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := eng.Convert(ctx, docPath)
		if err != nil {
			b.Fatal(err)
		}
	}
}
