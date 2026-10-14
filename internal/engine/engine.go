package engine

import (
	"context"
)

// Converter is the unified interface for converting files to Markdown.
type Converter interface {
	// Convert converts a document (image, PDF, text, table) to Markdown.
	// Returns the markdown content, page count, and any error encountered.
	Convert(ctx context.Context, filePath string) (markdown string, pageCount int, err error)

	// IsAvailable returns true if the underlying neural model or engine is loaded/ready.
	IsAvailable() bool

	// BackendName returns the human-readable identifier of this engine (e.g. "docling-eif").
	BackendName() string

	// Close releases any allocated resources (memory buffers, model weights).
	Close() error
}
