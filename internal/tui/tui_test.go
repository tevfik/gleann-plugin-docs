package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tevfik/gleann-plugin-docs/internal/config"
	"github.com/tevfik/gleann-plugin-docs/internal/docparser"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

type harness struct {
	m         Model
	downloads [][]string // dir, file names per call
	saved     *config.Config
	installed int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	t.Setenv("GLEANN_INDRA_MODELS", t.TempDir())
	t.Setenv("GLEANN_DOCPARSER_MODELS", t.TempDir())
	t.Setenv("GLEANN_DOCLING_MODELS", t.TempDir())
	h := &harness{}
	h.m = New(&config.Config{}, Hooks{
		Install: func() error { h.installed++; return nil },
		Download: func(_ context.Context, dir string, specs []docparser.ModelSpec) error {
			row := []string{dir}
			for _, s := range specs {
				row = append(row, s.File)
			}
			h.downloads = append(h.downloads, row)
			return nil
		},
		Save: func(c *config.Config) error { cp := *c; h.saved = &cp; return nil },
	})
	return h
}

func (h *harness) press(keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = h.m.Update(key(k))
		h.m = next.(Model)
	}
	return cmd
}

// run executes a command returned by Update and feeds its message back.
func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	next, _ := h.m.Update(cmd())
	h.m = next.(Model)
}

func TestEngineCyclesAndSaves(t *testing.T) {
	h := newHarness(t)
	h.press("right", "right") // auto -> paddle -> indra
	if h.m.cfg.Engine != config.EngineIndra {
		t.Fatalf("engine = %q", h.m.cfg.Engine)
	}
	h.press("left", "left", "left") // indra -> paddle -> auto -> off
	if h.m.cfg.Engine != config.EngineOff {
		t.Fatalf("engine = %q after wrap-around", h.m.cfg.Engine)
	}
	h.press("s")
	if h.saved == nil || !h.saved.Completed || h.saved.Engine != config.EngineOff {
		t.Fatalf("saved = %+v", h.saved)
	}
}

func TestQuitDoesNotSave(t *testing.T) {
	h := newHarness(t)
	h.press("right", "q")
	if h.saved != nil || h.m.Saved() {
		t.Fatal("q must discard the changes")
	}
}

func TestDeepNeedsVLM(t *testing.T) {
	h := newHarness(t)
	h.press("down", "down", "down", "right") // cursor on Deep tables, VLM off
	if h.m.cfg.Deep {
		t.Fatal("deep tables enabled without the VLM")
	}
	h.press("up", "right", "down", "right") // VLM on, then Deep on
	if !h.m.cfg.VLM || !h.m.cfg.Deep {
		t.Fatalf("vlm=%v deep=%v", h.m.cfg.VLM, h.m.cfg.Deep)
	}
	h.press("up", "right") // VLM off again clears deep
	if h.m.cfg.VLM || h.m.cfg.Deep {
		t.Fatalf("turning the VLM off kept deep=%v", h.m.cfg.Deep)
	}
}

func TestIndraDownloadAsksForLicense(t *testing.T) {
	h := newHarness(t)
	h.press("right", "right")                       // indra
	h.press("down", "down", "down", "down", "down") // Download models row
	cmd := h.press("enter")
	if h.m.mode != modeLicense || cmd != nil || len(h.downloads) != 0 {
		t.Fatalf("mode=%v downloads=%v: the license prompt must come first", h.m.mode, h.downloads)
	}
	if !strings.Contains(h.m.View(), "OpenRAIL") {
		t.Error("license text not shown")
	}

	h.press("n") // decline
	if h.m.cfg.IndraLicenseAccepted || len(h.downloads) != 0 {
		t.Fatal("declining must not download or record acceptance")
	}

	h.press("enter") // ask again
	cmd = h.press("y")
	if !h.m.cfg.IndraLicenseAccepted {
		t.Fatal("acceptance not recorded")
	}
	h.run(cmd) // the download starts only now
	if len(h.downloads) != 1 {
		t.Fatalf("downloads = %v", h.downloads)
	}
	got := strings.Join(h.downloads[len(h.downloads)-1], " ")
	for _, f := range []string{"det.onnx", "rec.onnx", "order.onnx", "meta.json"} {
		if !strings.Contains(got, f) {
			t.Errorf("download misses %s: %s", f, got)
		}
	}
}

func TestAcceptedLicenseSkipsPrompt(t *testing.T) {
	h := newHarness(t)
	h.m.cfg.IndraLicenseAccepted = true
	h.press("right", "right", "down", "down", "down", "down", "down")
	cmd := h.press("enter")
	if h.m.mode != modeMenu || cmd == nil {
		t.Fatalf("mode=%v cmd=%v", h.m.mode, cmd)
	}
	h.run(cmd)
	if len(h.downloads) != 1 {
		t.Fatalf("downloads = %v", h.downloads)
	}
}

func TestDownloadErrorIsShown(t *testing.T) {
	h := newHarness(t)
	h.m.hooks.Download = func(context.Context, string, []docparser.ModelSpec) error { return errors.New("offline") }
	h.press("down", "down", "down", "down", "down") // Download models, engine auto -> PP-OCR set
	h.run(h.press("enter"))
	if !strings.Contains(h.m.status, "offline") || h.m.busy {
		t.Fatalf("status = %q busy=%v", h.m.status, h.m.busy)
	}
}

func TestInstallHook(t *testing.T) {
	h := newHarness(t)
	h.press("down", "down", "down", "down", "down", "down", "enter")
	if h.installed != 1 {
		t.Fatalf("install calls = %d", h.installed)
	}
}
