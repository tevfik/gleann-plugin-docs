package engine

import (
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"
)

// backendRecheck is how long the result of the PATH lookups is reused.
const backendRecheck = 60 * time.Second

// BackendReporter is implemented by engines that can say which of their
// backends are usable. The HTTP /health endpoint reports it; engines that do
// not implement it are reported by IsAvailable only.
type BackendReporter interface {
	// BackendStatus maps a backend name to whether it is usable now.
	BackendStatus() map[string]bool
	// MissingFormats lists the formats that cannot be converted because a
	// backend is absent, for example ".pdf (scanned)" or ".odt".
	MissingFormats() []string
}

type toolCache struct {
	mu      sync.Mutex
	at      time.Time
	have    map[string]bool
	soffice bool
	anydoc  bool
}

// probeTools looks up the external tools, reusing the answer for backendRecheck.
func (e *EIFDoclingEngine) probeTools() (have map[string]bool, soffice, anydoc bool) {
	c := &e.tools
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.have != nil && time.Since(c.at) < backendRecheck {
		return c.have, c.soffice, c.anydoc
	}
	look := e.lookPath
	if look == nil {
		look = exec.LookPath
	}
	c.have = map[string]bool{}
	for _, tool := range []string{"pdftotext", "pdftoppm"} {
		_, err := look(tool)
		c.have[tool] = err == nil
	}
	c.soffice = false
	if p := os.Getenv("GLEANN_SOFFICE"); p != "" {
		_, err := os.Stat(p)
		c.soffice = err == nil
	} else {
		for _, n := range []string{"soffice", "libreoffice"} {
			if _, err := look(n); err == nil {
				c.soffice = true
				break
			}
		}
	}
	c.anydoc = anyDocAvailable(look)
	c.at = time.Now()
	return c.have, c.soffice, c.anydoc
}

// BackendStatus reports the backends this engine actually has.
func (e *EIFDoclingEngine) BackendStatus() map[string]bool {
	if e == nil {
		return map[string]bool{}
	}
	tools, soffice, anydoc := e.probeTools()
	return map[string]bool{
		"docling-vlm":    e.vlmStatus(),
		"onnx-docparser": e.hybrid != nil,
		"pdftotext":      tools["pdftotext"],
		"pdftoppm":       tools["pdftoppm"],
		"libreoffice":    soffice,
		"anydoc":         anydoc,
	}
}

// MissingFormats lists the formats that fail for want of a backend.
func (e *EIFDoclingEngine) MissingFormats() []string {
	b := e.BackendStatus()
	var out []string
	if !b["pdftotext"] {
		out = append(out, ".pdf")
	} else if !b["pdftoppm"] || !(b["docling-vlm"] || b["onnx-docparser"]) {
		out = append(out, ".pdf (scanned)")
	}
	if !(b["docling-vlm"] || b["onnx-docparser"]) {
		out = append(out, ".png (OCR)", ".jpg (OCR)", ".tif (OCR)", ".bmp (OCR)", ".webp (OCR)")
	}
	if !b["anydoc"] {
		out = append(out, ".odt", ".ods", ".odp", ".rtf")
	}
	if !b["libreoffice"] {
		out = append(out, ".doc", ".ppt")
	}
	sort.Strings(out)
	return out
}
