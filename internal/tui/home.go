package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tevfik/gleann-plugin-docs/internal/config"
)

// Screen represents the active screen in the TUI hub.
type Screen int

const (
	ScreenHome Screen = iota
	ScreenSetup
	ScreenConvert
	ScreenServe
	ScreenModels
	ScreenUpdate
	ScreenInstall
	ScreenUninstall
	ScreenSettings
)

type menuItem struct {
	title  string
	desc   string
	icon   string
	screen Screen
}

// Menu items shown before onboarding/setup is completed.
var preSetupMenuItems = []menuItem{
	{title: "Setup", desc: "Configure page parser, OCR profile & VLM", icon: "⚙ ", screen: ScreenSetup},
	{title: "Update", desc: "Update from the latest GitHub release", icon: "⬆ ", screen: ScreenUpdate},
	{title: "Quit", desc: "Exit", icon: "👋", screen: ScreenHome},
}

// Menu items shown after onboarding/setup is completed.
var postSetupMenuItems = []menuItem{
	{title: "Convert", desc: "Convert document (PDF, Office, image) to markdown or JSON", icon: "📄", screen: ScreenConvert},
	{title: "Serve", desc: "Plugin server for gleann (HTTP daemon on :8765)", icon: "🔌", screen: ScreenServe}, // replaced by serveMenuItem at runtime
	{title: "Models", desc: "Verify & download OCR and VLM model weights", icon: "🔬", screen: ScreenModels},
	{title: "Setup", desc: "Configure page parser, OCR profile, VLM & DPI", icon: "⚙ ", screen: ScreenSetup},
	{title: "Settings", desc: "Every setting, one at a time (the same ones as the gleann web UI)", icon: "🎛 ", screen: ScreenSettings},
	{title: "Update", desc: "Update from the latest GitHub release", icon: "⬆ ", screen: ScreenUpdate},
	{title: "Install", desc: "Register plugin into ~/.gleann/plugins.json", icon: "📦", screen: ScreenInstall},
	{title: "Uninstall", desc: "Remove plugin from ~/.gleann/plugins.json", icon: "🗑 ", screen: ScreenUninstall},
	{title: "Quit", desc: "Exit", icon: "👋", screen: ScreenHome},
}

// DefaultServePort is the port gleann's plugins.json entry points at.
const DefaultServePort = 8765

// HomeModel is the main hub of the TUI.
type HomeModel struct {
	cursor   int
	width    int
	height   int
	quitting bool
	chosen   Screen
	items    []menuItem
	cfg      *config.Config
}

// NewHomeModel creates a home screen model, loading current config to determine menu items.
func NewHomeModel() HomeModel {
	cfg := config.Load()
	return newHomeModelWithConfig(cfg)
}

// NewHomeModelWithConfig creates a home screen with the given config.
func NewHomeModelWithConfig(cfg *config.Config) HomeModel {
	return newHomeModelWithConfig(cfg)
}

func newHomeModelWithConfig(cfg *config.Config) HomeModel {
	items := preSetupMenuItems
	if cfg != nil && cfg.Completed {
		// Copy so the Serve entry can show the live enabled/disabled state.
		items = append([]menuItem(nil), postSetupMenuItems...)
		for i := range items {
			if items[i].screen == ScreenServe {
				items[i] = serveMenuItem(DefaultServePort)
			}
		}
	}
	return HomeModel{items: items, cfg: cfg}
}

func (m HomeModel) Init() tea.Cmd { return nil }

func (m HomeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter":
			if m.cursor < len(m.items) {
				it := m.items[m.cursor]
				if it.title == "Quit" {
					m.quitting = true
				} else {
					m.chosen = it.screen
				}
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m HomeModel) Chosen() Screen         { return m.chosen }
func (m HomeModel) Quitting() bool         { return m.quitting }
func (m HomeModel) Config() *config.Config { return m.cfg }

func (m HomeModel) View() string {
	if m.quitting {
		return "\n  " + lipgloss.NewStyle().Foreground(ColorMuted).Render("Bye! 👋") + "\n"
	}

	var b strings.Builder
	b.WriteString(Logo())
	b.WriteString("\n")
	b.WriteString(SubtitleStyle.Render("  Local document extraction & OCR companion for gleann"))
	b.WriteString("\n\n")

	for i, item := range m.items {
		if i == m.cursor {
			b.WriteString(ActiveItemStyle.Render(fmt.Sprintf("▸ %s %s", item.icon, item.title)))
			b.WriteString("\n")
			b.WriteString(ActiveDescStyle.Render("  " + item.desc))
		} else {
			b.WriteString(NormalItemStyle.Render(fmt.Sprintf("  %s %s", item.icon, item.title)))
			b.WriteString("\n")
			b.WriteString(DescStyle.Render(item.desc))
		}
		b.WriteString("\n")
	}

	if m.cfg != nil && m.cfg.Completed {
		b.WriteString("\n")
		engineStr := m.cfg.Engine
		if engineStr == "" {
			engineStr = "auto"
		}
		profileStr := m.cfg.Profile
		if profileStr == "" {
			profileStr = "latin"
		}
		vlmStr := "off"
		if m.cfg.VLM {
			vlmStr = "on"
		}
		dpiVal := m.cfg.DPI
		if dpiVal == 0 {
			dpiVal = 150
		}
		statusLine := fmt.Sprintf("  engine: %s │ profile: %s │ vlm: %s │ %d dpi",
			engineStr, profileStr, vlmStr, dpiVal)
		b.WriteString(lipgloss.NewStyle().Foreground(ColorDimFg).Render(statusLine))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(StatusBarStyle.Render("  ↑/↓ navigate • enter select • q quit"))
	return b.String()
}
