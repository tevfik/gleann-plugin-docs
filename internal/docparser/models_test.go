package docparser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func specFor(payload []byte, url string) ModelSpec {
	sum := sha256.Sum256(payload)
	return ModelSpec{Name: "t", File: "t.onnx", URL: url, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload))}
}

func TestDownloadModel(t *testing.T) {
	payload := []byte(strings.Repeat("model-bytes-", 1000))
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/ok":
			w.Write(payload)
		case "/tampered":
			w.Write([]byte("something else entirely"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Run("downloads, verifies and installs", func(t *testing.T) {
		dir := t.TempDir()
		m := specFor(payload, srv.URL+"/ok")
		got, err := DownloadModel(context.Background(), nil, dir, m)
		if err != nil || !got {
			t.Fatalf("downloaded=%v err=%v", got, err)
		}
		if err := VerifyModel(dir, m); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("skips a file that already matches", func(t *testing.T) {
		dir := t.TempDir()
		m := specFor(payload, srv.URL+"/ok")
		os.WriteFile(filepath.Join(dir, m.File), payload, 0o644)
		before := hits.Load()
		got, err := DownloadModel(context.Background(), nil, dir, m)
		if err != nil || got {
			t.Fatalf("downloaded=%v err=%v", got, err)
		}
		if hits.Load() != before {
			t.Error("a valid file must not trigger a request")
		}
	})

	t.Run("replaces a corrupt file", func(t *testing.T) {
		dir := t.TempDir()
		m := specFor(payload, srv.URL+"/ok")
		os.WriteFile(filepath.Join(dir, m.File), []byte("truncated"), 0o644)
		if got, err := DownloadModel(context.Background(), nil, dir, m); err != nil || !got {
			t.Fatalf("downloaded=%v err=%v", got, err)
		}
		if err := VerifyModel(dir, m); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a checksum mismatch installs nothing", func(t *testing.T) {
		dir := t.TempDir()
		m := specFor(payload, srv.URL+"/tampered")
		_, err := DownloadModel(context.Background(), nil, dir, m)
		if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("err = %v", err)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Errorf("directory should be empty after a failed download, has %v", entries)
		}
	})

	t.Run("HTTP errors are reported", func(t *testing.T) {
		m := specFor(payload, srv.URL+"/missing")
		if _, err := DownloadModel(context.Background(), nil, t.TempDir(), m); err == nil || !strings.Contains(err.Error(), "404") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("a cancelled context stops the download", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		m := specFor(payload, srv.URL+"/ok")
		if _, err := DownloadModel(ctx, nil, t.TempDir(), m); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestEnsureModelsReportsProgress(t *testing.T) {
	payload := []byte("abc")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(payload) }))
	defer srv.Close()
	dir := t.TempDir()
	m := specFor(payload, srv.URL)

	var lines []string
	say := func(s string) { lines = append(lines, s) }
	for i := 0; i < 2; i++ {
		if err := EnsureModels(context.Background(), dir, []ModelSpec{m}, say); err != nil {
			t.Fatal(err)
		}
	}
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "downloaded") || !strings.HasPrefix(lines[1], "ok") {
		t.Errorf("progress = %q", lines)
	}
}

func TestModelRegistryIsConsistent(t *testing.T) {
	for _, p := range []OCRProfile{ProfileLatin, ProfileChinese} {
		files := map[string]bool{}
		for _, m := range DefaultModels(p) {
			if len(m.SHA256) != 64 || !strings.HasPrefix(m.URL, "https://") || m.File == "" || m.Size <= 0 {
				t.Errorf("%s/%s: incomplete spec %+v", p.Name, m.Name, m)
			}
			if strings.Contains(m.URL, "/resolve/main/") {
				t.Errorf("%s: URL must be pinned to a revision, not main: %s", m.Name, m.URL)
			}
			if files[m.File] {
				t.Errorf("%s: file %s listed twice", p.Name, m.File)
			}
			files[m.File] = true
		}
	}
	if ProfileLatin.Dict == nil || ProfileChinese.Dict != nil {
		t.Error("latin carries an external dictionary, chinese reads it from the model")
	}
}

func TestProfileByName(t *testing.T) {
	for in, want := range map[string]string{"": "latin", "LATIN": "latin", "chinese": "chinese", "ppocrv4": "chinese"} {
		p, err := ProfileByName(in)
		if err != nil || p.Name != want {
			t.Errorf("ProfileByName(%q) = %q, %v; want %q", in, p.Name, err, want)
		}
	}
	if _, err := ProfileByName("klingon"); err == nil {
		t.Error("an unknown profile must be an error")
	}
}

func TestModelDirEnv(t *testing.T) {
	t.Setenv(ModelDirEnv, "/custom/dir")
	if got := ModelDir(); got != "/custom/dir" {
		t.Errorf("ModelDir() = %q", got)
	}
	t.Setenv(ModelDirEnv, "")
	if got := ModelDir(); !strings.HasSuffix(got, filepath.Join(".gleann", "models", "docparser")) {
		t.Errorf("default ModelDir() = %q", got)
	}
}

func TestLoadModelMissingMentionsFix(t *testing.T) {
	_, err := loadModel(t.TempDir(), LayoutModel)
	if err == nil || !strings.Contains(err.Error(), "models download") {
		t.Errorf("err = %v", err)
	}
}
