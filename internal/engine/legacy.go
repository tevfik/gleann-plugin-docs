package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Legacy binary Office formats (.doc, .xls, .ppt) are converted to their OOXML
// twin with a headless LibreOffice and then read by the native extractors.
// LibreOffice is an optional dependency: without it these files fail with an
// error that says what to install, everything else keeps working.
//
//	GLEANN_SOFFICE=/path/to/soffice   override the binary lookup
var legacyTarget = map[string]string{".doc": "docx", ".xls": "xlsx", ".ppt": "pptx"}

const legacyTimeout = 180 * time.Second

func findSoffice() (string, error) {
	if p := os.Getenv("GLEANN_SOFFICE"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("GLEANN_SOFFICE=%s: %w", p, err)
		}
		return p, nil
	}
	for _, n := range []string{"soffice", "libreoffice"} {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("legacy Office files (.doc/.xls/.ppt) need LibreOffice (install it or set GLEANN_SOFFICE), or save the file as .docx/.xlsx/.pptx")
}

// convertLegacyOffice converts path to its OOXML twin inside a temporary
// directory and returns the new path plus a cleanup function.
func convertLegacyOffice(ctx context.Context, path string) (string, func(), error) {
	target, ok := legacyTarget[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return "", nil, fmt.Errorf("not a legacy Office file: %s", path)
	}
	soffice, err := findSoffice()
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "gleann-legacy-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }

	ctx, cancel := context.WithTimeout(ctx, legacyTimeout)
	defer cancel()
	// A private profile dir lets several conversions (and a running desktop
	// LibreOffice) coexist.
	cmd := exec.CommandContext(ctx, soffice,
		"-env:UserInstallation=file://"+filepath.Join(dir, "profile"),
		"--headless", "--norestore", "--convert-to", target, "--outdir", dir, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("LibreOffice failed on %s: %v: %s", filepath.Base(path), err, strings.TrimSpace(string(out)))
	}
	out := filepath.Join(dir, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+"."+target)
	if _, err := os.Stat(out); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("LibreOffice produced no .%s for %s", target, filepath.Base(path))
	}
	return out, cleanup, nil
}
