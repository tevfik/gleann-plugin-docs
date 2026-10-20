package docparser

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ORTVersion is the ONNX Runtime release `models download` installs. The Go
// binding (onnxruntime_go) only needs a runtime whose API level is at least its own.
const ORTVersion = "1.31.0"

const ortBaseURL = "https://github.com/microsoft/onnxruntime/releases/download/v" + ORTVersion

// ortRelease pins one official release archive (MIT licensed, built by Microsoft).
type ortRelease struct {
	Asset  string // archive file name
	SHA256 string // of the archive
	Lib    string // library path inside the archive
	Name   string // installed file name
}

var ortReleases = map[string]ortRelease{
	"linux/arm64": {
		Asset:  "onnxruntime-linux-aarch64-" + ORTVersion + ".tgz",
		SHA256: "c5b8b3cca31f3d643a3b313b8f42e2d0f16a4fcefd02cda343d97bd5306afaab",
		Lib:    "onnxruntime-linux-aarch64-" + ORTVersion + "/lib/libonnxruntime.so." + ORTVersion,
		Name:   "libonnxruntime.so." + ORTVersion,
	},
	"linux/amd64": {
		Asset:  "onnxruntime-linux-x64-" + ORTVersion + ".tgz",
		SHA256: "cc5c72baf5ae5c8238a6841f0897227be2d02826b9cf98eaf02fdefaeeb03a57",
		Lib:    "onnxruntime-linux-x64-" + ORTVersion + "/lib/libonnxruntime.so." + ORTVersion,
		Name:   "libonnxruntime.so." + ORTVersion,
	},
	"darwin/arm64": {
		Asset:  "onnxruntime-osx-arm64-" + ORTVersion + ".tgz",
		SHA256: "1031b36dd3bdaa8d976a6130d9cf2a05e9be320b93ae804f8427b91dc3432afc",
		Lib:    "onnxruntime-osx-arm64-" + ORTVersion + "/lib/libonnxruntime." + ORTVersion + ".dylib",
		Name:   "libonnxruntime." + ORTVersion + ".dylib",
	},
}

// ORTDownloadSupported reports whether a pinned runtime exists for this platform.
func ORTDownloadSupported() bool {
	_, ok := ortReleases[runtime.GOOS+"/"+runtime.GOARCH]
	return ok
}

// ORTLibDir is where InstallORT puts the runtime (~/.gleann/lib), a directory
// FindORTLibrary already searches.
func ORTLibDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "lib"
	}
	return filepath.Join(home, ".gleann", "lib")
}

// InstallORT downloads the pinned ONNX Runtime archive for this platform, checks
// its SHA-256 and installs the shared library into dir (ORTLibDir when empty).
// It returns the library path. An existing library found by FindORTLibrary is
// left alone: the function then does nothing and returns that path.
func InstallORT(ctx context.Context, dir string, progress func(string)) (string, error) {
	if p, err := FindORTLibrary(); err == nil {
		return p, nil
	}
	rel, ok := ortReleases[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", fmt.Errorf("no pinned ONNX Runtime for %s/%s: install it yourself and set %s", runtime.GOOS, runtime.GOARCH, LibEnv)
	}
	if dir == "" {
		dir = ORTLibDir()
	}
	return installORT(ctx, http.DefaultClient, ortBaseURL, rel, dir, progress)
}

func installORT(ctx context.Context, client *http.Client, base string, rel ortRelease, dir string, progress func(string)) (string, error) {
	say := func(s string) {
		if progress != nil {
			progress(s)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".ort-*.tgz")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+rel.Asset, nil)
	if err != nil {
		return "", err
	}
	say("downloading ONNX Runtime " + ORTVersion + " (" + rel.Asset + ")")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", rel.Asset, resp.Status)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != rel.SHA256 {
		return "", fmt.Errorf("%s: sha256 %s, expected %s", rel.Asset, got, rel.SHA256)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return "", err
	}

	dest := filepath.Join(dir, rel.Name)
	if err := extractTarMember(tmp, rel.Lib, dest); err != nil {
		return "", err
	}
	// FindORTLibrary matches libonnxruntime.so / .dylib or a versioned file.
	if strings.HasSuffix(rel.Name, ".dylib") {
		link := filepath.Join(dir, "libonnxruntime.dylib")
		os.Remove(link)
		_ = os.Symlink(rel.Name, link)
	} else {
		link := filepath.Join(dir, "libonnxruntime.so")
		os.Remove(link)
		_ = os.Symlink(rel.Name, link)
	}
	say("installed " + dest)
	return dest, nil
}

// extractTarMember writes the member `name` of a .tgz stream to dest atomically.
func extractTarMember(r io.Reader, name, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s not found in the archive", name)
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || path.Clean(hdr.Name) != name {
			continue
		}
		part := dest + ".part"
		f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			os.Remove(part)
			return err
		}
		if err := f.Close(); err != nil {
			os.Remove(part)
			return err
		}
		return os.Rename(part, dest)
	}
}
