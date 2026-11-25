//go:build !anydoc

package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// HasInProcessAnyDoc reports false because this binary was built without in-process anydoc.
const HasInProcessAnyDoc = false

// findAnyDocCLI looks for the anydoc CLI on PATH, then in the standard cargo
// and local locations.
func findAnyDocCLI(lookPath func(string) (string, error)) string {
	if p, err := lookPath("anydoc"); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, p := range []string{
			filepath.Join(home, ".cargo", "bin", "anydoc"),
			filepath.Join(home, ".local", "bin", "anydoc"),
		} {
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p
			}
		}
	}
	return ""
}

// anyDocAvailable reports whether the anydoc CLI can be found.
func anyDocAvailable(lookPath func(string) (string, error)) bool {
	return findAnyDocCLI(lookPath) != ""
}

func convertAnyDoc(ctx context.Context, filePath string) (string, error) {
	anydocBin := findAnyDocCLI(exec.LookPath)
	if anydocBin == "" {
		return "", errAnyDocNotAvailable
	}

	cmd := exec.CommandContext(ctx, anydocBin, filePath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("%w: anydoc CLI: %w", ErrBackendUnavailable, err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("%w: anydoc CLI failed to start: %w", ErrBackendUnavailable, err)
	}
	// Bound the output at the Markdown cap so a runaway converter cannot fill memory.
	limit := MaxMarkdownBytes()
	out, readErr := io.ReadAll(io.LimitReader(stdout, limit+1))
	if int64(len(out)) > limit {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", fmt.Errorf("%w: anydoc output exceeds %d bytes", ErrTooLarge, limit)
	}
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("anydoc CLI failed: %w", err)
	}
	if readErr != nil {
		return "", fmt.Errorf("anydoc CLI: %w", readErr)
	}
	return string(out), nil
}
