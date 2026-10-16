package docparser

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// LibEnv overrides the ONNX Runtime shared library location.
const LibEnv = "GLEANN_ORT_LIB"

var (
	ortOnce sync.Once
	ortErr  error
)

// ortLibNames are the file name patterns of the runtime library per platform.
func ortLibGlobs() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"onnxruntime.dll"}
	case "darwin":
		return []string{"libonnxruntime*.dylib"}
	}
	return []string{"libonnxruntime.so", "libonnxruntime.so.*"}
}

// FindORTLibrary locates libonnxruntime. Order: $GLEANN_ORT_LIB, next to the
// executable, ~/.gleann/lib, then the usual system directories. When several
// versions match in one directory the highest file name wins.
func FindORTLibrary() (string, error) {
	if p := os.Getenv(LibEnv); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("%s=%s: %w", LibEnv, p, err)
		}
		return p, nil
	}

	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe), filepath.Join(filepath.Dir(exe), "lib"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".gleann", "lib"))
	}
	dirs = append(dirs, "/usr/local/lib", "/usr/lib", "/usr/lib/x86_64-linux-gnu", "/usr/lib/aarch64-linux-gnu", "/opt/homebrew/lib")

	for _, dir := range dirs {
		for _, g := range ortLibGlobs() {
			matches, _ := filepath.Glob(filepath.Join(dir, g))
			if len(matches) > 0 {
				sort.Strings(matches)
				return matches[len(matches)-1], nil
			}
		}
	}
	return "", fmt.Errorf("onnxruntime shared library not found (set %s or place libonnxruntime in ~/.gleann/lib)", LibEnv)
}

// initORT initialises the process-wide ONNX Runtime environment once.
func initORT() error {
	ortOnce.Do(func() {
		if ort.IsInitialized() {
			return
		}
		path, err := FindORTLibrary()
		if err != nil {
			ortErr = err
			return
		}
		ort.SetSharedLibraryPath(path)
		if err := ort.InitializeEnvironment(); err != nil {
			ortErr = fmt.Errorf("initialising onnxruntime (%s): %w", path, err)
		}
	})
	return ortErr
}

// newSession builds a CPU session from model bytes. intraThreads <= 0 keeps the
// runtime default.
func newSession(model []byte, inputs, outputs []string, intraThreads int) (*ort.DynamicAdvancedSession, error) {
	if err := initORT(); err != nil {
		return nil, err
	}
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	defer opts.Destroy()
	if intraThreads > 0 {
		if err := opts.SetIntraOpNumThreads(intraThreads); err != nil {
			return nil, err
		}
	}
	// ONNX Runtime's worker threads spin-wait by default. Several sessions run at
	// once here, and the spinning burned twice the CPU for a slower page
	// (Measured: 31 s of CPU for a 2.1 s page; 15 s for a 1.8 s one without it).
	if err := opts.AddSessionConfigEntry("session.intra_op.allow_spinning", "0"); err != nil {
		return nil, err
	}
	return ort.NewDynamicAdvancedSessionWithONNXData(model, inputs, outputs, opts)
}

// newSingleIOSession builds a session for a model with one input and one
// output, reading the tensor names from the model itself (they differ between
// PaddleOCR exports).
func newSingleIOSession(model []byte, intraThreads int) (*ort.DynamicAdvancedSession, error) {
	if err := initORT(); err != nil {
		return nil, err
	}
	ins, outs, err := ort.GetInputOutputInfoWithONNXData(model)
	if err != nil {
		return nil, err
	}
	if len(ins) != 1 || len(outs) != 1 {
		return nil, fmt.Errorf("expected a model with 1 input and 1 output, got %d and %d", len(ins), len(outs))
	}
	return newSession(model, []string{ins[0].Name}, []string{outs[0].Name}, intraThreads)
}
