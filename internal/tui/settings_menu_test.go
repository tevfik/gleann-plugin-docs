package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tevfik/gleann-plugin-docs/internal/config"
)

func settingsKeys(m tea.Model, keys ...tea.KeyMsg) SettingsModel {
	for _, k := range keys {
		m, _ = m.Update(k)
	}
	return m.(SettingsModel)
}

func settingsOn(t *testing.T, c *config.Config, key string) SettingsModel {
	t.Helper()
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "docs.json"))
	if err := config.Save(c); err != nil {
		t.Fatal(err)
	}
	m := NewSettingsModel()
	for i, k := range m.rows() {
		if k == key {
			m.cursor = i
			return m
		}
	}
	t.Fatalf("no row %q in %v", key, m.rows())
	return m
}

func TestSettingsMenuShowsTheSettingsSetupLacks(t *testing.T) {
	m := settingsOn(t, &config.Config{Completed: true}, "threads")
	v := m.View()
	for _, want := range []string{"CPU threads", "Offline", "ONNX Runtime library", "PATHS"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	if strings.Contains(v, "VLM page budget") {
		t.Error("VLM tuning shown while the VLM is off")
	}
}

func TestSettingsMenuEditsAndValidates(t *testing.T) {
	right, enter := tea.KeyMsg{Type: tea.KeyRight}, tea.KeyMsg{Type: tea.KeyEnter}
	m := settingsOn(t, &config.Config{Completed: true}, "threads")
	m = settingsKeys(m, right, right)
	if c := config.Load(); c.Threads != 5 { // 0 (auto) -> 4, the minimum -> 5
		t.Errorf("threads after → →: %d", c.Threads)
	}
	m = settingsKeys(m, enter, tea.KeyMsg{Type: tea.KeyCtrlU}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("12")}, enter)
	if c := config.Load(); c.Threads != 12 || m.editing {
		t.Errorf("typed threads: %d (editing %v)", c.Threads, m.editing)
	}

	m = settingsOn(t, &config.Config{Completed: true}, "soffice")
	m = settingsKeys(m, enter, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/no/such/soffice")}, enter)
	if m.err == nil || !m.editing || config.Load().SOffice != "" {
		t.Errorf("a missing path was accepted: %v", m.err)
	}
}
