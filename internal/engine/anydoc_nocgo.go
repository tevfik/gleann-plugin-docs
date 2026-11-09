//go:build !anydoc

package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// HasInProcessAnyDoc reports false because this binary was built without in-process anydoc.
const HasInProcessAnyDoc = false

func convertAnyDoc(ctx context.Context, filePath string) (string, error) {
	// Look for anydoc CLI on PATH or standard cargo/local locations as fallback.
	anydocBin, err := exec.LookPath("anydoc")
	if err != nil {
		if home, err := os.UserHomeDir(); err == nil {
			for _, p := range []string{
				filepath.Join(home, ".cargo", "bin", "anydoc"),
				filepath.Join(home, ".local", "bin", "anydoc"),
			} {
				if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
					anydocBin = p
					break
				}
			}
		}
	}
	if anydocBin == "" {
		return "", errAnyDocNotAvailable
	}

	cmd := exec.CommandContext(ctx, anydocBin, filePath)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("anydoc CLI failed: %w", err)
	}
	return string(out), nil
}
