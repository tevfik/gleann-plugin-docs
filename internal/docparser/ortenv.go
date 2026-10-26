package docparser

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
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

// MinORTMinor is the oldest ONNX Runtime 1.x the bindings load:
// onnxruntime_go v1.26 asks for ORT API version 24, which 1.24 introduced.
// Older libraries fail at start-up ("The requested API version [24] is not
// available"), so they are skipped rather than picked.
const MinORTMinor = 24

var ortVersionRe = regexp.MustCompile(`onnxruntime[._-]?(?:so\.)?(\d+)\.(\d+)`)

// ortLibVersion reads major.minor from the library file name (following
// symlinks, so libonnxruntime.so -> libonnxruntime.so.1.21.0 counts too).
func ortLibVersion(path string) (major, minor int, ok bool) {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		path = r
	}
	m := ortVersionRe.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}

// ortTooOld reports whether path is a runtime the bindings cannot load. A
// library without a version in its name is given the benefit of the doubt.
func ortTooOld(path string) bool {
	major, minor, ok := ortLibVersion(path)
	return ok && major == 1 && minor < MinORTMinor
}

// FindORTLibrary locates libonnxruntime. Order: $GLEANN_ORT_LIB, next to the
// executable, ~/.gleann/lib, then the usual system directories. When several
// versions match in one directory the highest file name wins. Libraries older
// than 1.MinORTMinor are skipped (a distribution's ORT 1.21 in /usr/lib must
// not hide a usable one, nor stop InstallORT from installing it).
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

	var tooOld string
	for _, dir := range dirs {
		for _, g := range ortLibGlobs() {
			matches, _ := filepath.Glob(filepath.Join(dir, g))
			sort.Strings(matches)
			for i := len(matches) - 1; i >= 0; i-- {
				if ortTooOld(matches[i]) {
					if tooOld == "" {
						tooOld = matches[i]
					}
					continue
				}
				return matches[i], nil
			}
		}
	}
	if tooOld != "" {
		return "", fmt.Errorf("onnxruntime at %s is too old (need 1.%d or newer): run 'gleann-plugin-docs models download --ort' to install %s into ~/.gleann/lib, or set %s", tooOld, MinORTMinor, ORTVersion, LibEnv)
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

// NewSession builds a CPU session with explicit input and output names for
// packages outside docparser (internal/indra). It shares the process-wide
// runtime environment and the spin-wait setting of the other sessions.
func NewSession(model []byte, inputs, outputs []string, intraThreads int) (*ort.DynamicAdvancedSession, error) {
	return newSession(model, inputs, outputs, intraThreads)
}
