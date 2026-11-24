package engine

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os/exec"
)

// Typed conversion failures. The HTTP layer maps them to status codes:
// ErrCorruptInput to 422, ErrBackendUnavailable to 503, ErrTooLarge to 413.
var (
	// ErrCorruptInput marks a file that is damaged or is not what its
	// extension says.
	ErrCorruptInput = errors.New("corrupt or unreadable input")
	// ErrBackendUnavailable marks a format that needs a tool or model this
	// installation does not have.
	ErrBackendUnavailable = errors.New("backend unavailable")
	// ErrTooLarge marks an input that exceeds a size, pixel or entry limit.
	ErrTooLarge = errors.New("input exceeds a size limit")
	// ErrImageTooLarge is an ErrTooLarge for an image over the pixel budget.
	ErrImageTooLarge = fmt.Errorf("%w: image exceeds the pixel budget", ErrTooLarge)
)

// classifyError tags err with one of the typed errors when its cause is
// recognised. The original error stays in the chain. Context errors and
// already typed errors are returned unchanged.
func classifyError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, ErrCorruptInput), errors.Is(err, ErrBackendUnavailable), errors.Is(err, ErrTooLarge):
		return err
	case errors.Is(err, errAnyDocNotAvailable), errors.Is(err, exec.ErrNotFound):
		return fmt.Errorf("%w: %w", ErrBackendUnavailable, err)
	}
	var exitErr *exec.ExitError
	switch {
	case errors.Is(err, zip.ErrFormat), errors.Is(err, image.ErrFormat), errors.Is(err, io.ErrUnexpectedEOF),
		errors.As(err, &exitErr):
		return fmt.Errorf("%w: %w", ErrCorruptInput, err)
	}
	return err
}
