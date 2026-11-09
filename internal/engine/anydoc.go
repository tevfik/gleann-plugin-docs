package engine

import (
	"errors"
	"strings"
)

var (
	errAnyDocNotAvailable = errors.New("anydoc not available")
)

// AnyDocSupportedExtensions lists the document formats handled by anydoc.
var anyDocSupportedExts = map[string]bool{
	".odt":  true,
	".ods":  true,
	".odp":  true,
	".rtf":  true,
	".docx": true,
	".pptx": true,
	".xlsx": true,
	".epub": true,
	".html": true,
	".htm":  true,
	".csv":  true,
}

// IsAnyDocSupported reports whether anydoc supports this file extension.
func IsAnyDocSupported(ext string) bool {
	return anyDocSupportedExts[strings.ToLower(ext)]
}
