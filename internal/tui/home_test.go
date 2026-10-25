package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tevfik/gleann-plugin-docs/internal/config"
)

func TestHomeModel_PreSetupItems(t *testing.T) {
	cfg := &config.Config{Completed: false}
	m := newHomeModelWithConfig(cfg)
	if len(m.items) != len(preSetupMenuItems) {
		t.Fatalf("expected %d pre-setup items, got %d", len(preSetupMenuItems), len(m.items))
	}
	titles := make([]string, len(m.items))
	for i, it := range m.items {
		titles[i] = it.title
	}
	joined := strings.Join(titles, ",")
	if !strings.Contains(joined, "Setup") || !strings.Contains(joined, "Update") || !strings.Contains(joined, "Quit") {
		t.Errorf("pre-setup items missing expected titles: %s", joined)
	}
}

func TestHomeModel_PostSetupItems(t *testing.T) {
	cfg := &config.Config{Completed: true, Engine: config.EnginePaddle, Profile: "latin"}
	m := newHomeModelWithConfig(cfg)
	if len(m.items) != len(postSetupMenuItems) {
		t.Fatalf("expected %d post-setup items, got %d", len(postSetupMenuItems), len(m.items))
	}
	titles := make([]string, len(m.items))
	for i, it := range m.items {
		titles[i] = it.title
	}
	joined := strings.Join(titles, ",")
	for _, expected := range []string{"Convert", "Serve", "Models", "Setup", "Update", "Install", "Uninstall", "Quit"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("post-setup items missing expected title %s: %s", expected, joined)
		}
	}
}

func TestHomeModel_NavigationAndSelection(t *testing.T) {
	cfg := &config.Config{Completed: true}
	m := newHomeModelWithConfig(cfg)

	// Navigate down
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	hm := next.(HomeModel)
	if hm.cursor != 1 {
		t.Errorf("cursor after down = %d, want 1", hm.cursor)
	}

	// Navigate up
	next, _ = hm.Update(tea.KeyMsg{Type: tea.KeyUp})
	hm = next.(HomeModel)
	if hm.cursor != 0 {
		t.Errorf("cursor after up = %d, want 0", hm.cursor)
	}

	// Press enter on Convert
	next, _ = hm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	hm = next.(HomeModel)
	if hm.Chosen() != ScreenConvert {
		t.Errorf("chosen screen = %v, want ScreenConvert", hm.Chosen())
	}
}

func TestHomeModel_QuitSelection(t *testing.T) {
	cfg := &config.Config{Completed: false}
	m := newHomeModelWithConfig(cfg)
	// preSetupMenuItems has Quit at index 2
	m.cursor = len(m.items) - 1
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	hm := next.(HomeModel)
	if !hm.Quitting() {
		t.Errorf("expected quitting=true when selecting Quit")
	}
}

func TestHomeModel_View(t *testing.T) {
	cfg := &config.Config{Completed: true, Engine: config.EnginePaddle, Profile: "latin", VLM: true, DPI: 200}
	m := newHomeModelWithConfig(cfg)
	v := m.View()

	if !strings.Contains(v, "╔═╗") {
		t.Errorf("View missing ASCII logo banner")
	}
	if !strings.Contains(v, "Local document extraction & OCR companion for gleann") {
		t.Errorf("View missing subtitle")
	}
	if !strings.Contains(v, "Update") {
		t.Errorf("View missing Update menu item")
	}
	if !strings.Contains(v, "engine: paddle") {
		t.Errorf("View missing engine status line: %s", v)
	}
}
