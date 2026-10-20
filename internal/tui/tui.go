// Package tui is the interactive setup of gleann-plugin-docs (`gleann-plugin-docs tui`):
// pick the page parser, see which models are installed, download them and
// register the plugin with gleann. It writes ~/.gleann/docs.json (package config).
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tevfik/gleann-plugin-docs/internal/config"
	"github.com/tevfik/gleann-plugin-docs/internal/docparser"
	"github.com/tevfik/gleann-plugin-docs/internal/indra"
)

// Hooks are the side effects the screen delegates, so tests can replace them.
type Hooks struct {
	// Install registers the plugin in ~/.gleann/plugins.json.
	Install func() error
	// Download fetches model files into dir (default docparser.EnsureModels).
	Download func(ctx context.Context, dir string, specs []docparser.ModelSpec) error
	// InstallORT fetches libonnxruntime when it is missing (default docparser.InstallORT).
	InstallORT func(ctx context.Context) error
	// Save persists the config (default config.Save).
	Save func(*config.Config) error
}

type item int

const (
	itemEngine item = iota
	itemProfile
	itemVLM
	itemDeep
	itemDPI
	itemModels
	itemInstall
	itemSave
	itemCount
)

var dpiChoices = []int{150, 200, 300}

type mode int

const (
	modeMenu mode = iota
	modeLicense
)

type doneMsg struct {
	what string
	err  error
}

// Model is the bubbletea model of the setup screen.
type Model struct {
	cfg      *config.Config
	hooks    Hooks
	cursor   item
	mode     mode
	busy     bool
	status   string
	saved    bool
	quitting bool
}

// New starts from the stored config (or the defaults).
func New(cfg *config.Config, h Hooks) Model {
	if h.Download == nil {
		h.Download = func(ctx context.Context, dir string, specs []docparser.ModelSpec) error {
			return docparser.EnsureModels(ctx, dir, specs, nil)
		}
	}
	if h.InstallORT == nil {
		h.InstallORT = func(ctx context.Context) error {
			_, err := docparser.InstallORT(ctx, "", nil)
			return err
		}
	}
	if h.Save == nil {
		h.Save = config.Save
	}
	if cfg.Engine == "" {
		cfg.Engine = config.EngineAuto
	}
	if cfg.Profile == "" {
		cfg.Profile = "latin"
	}
	return Model{cfg: cfg, hooks: h}
}

// Config returns the settings as edited so far.
func (m Model) Config() *config.Config { return m.cfg }

// Saved reports whether the config was written.
func (m Model) Saved() bool { return m.saved }

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case doneMsg:
		m.busy = false
		if msg.err != nil {
			m.status = fmt.Sprintf("%s failed: %v", msg.what, msg.err)
		} else {
			m.status = msg.what + " done"
		}
		return m, nil
	case tea.KeyMsg:
		if m.mode == modeLicense {
			return m.updateLicense(msg)
		}
		return m.updateMenu(msg)
	}
	return m, nil
}

func (m Model) updateLicense(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "y" {
		m.cfg.IndraLicenseAccepted = true
		m.mode = modeMenu
		return m.startDownload()
	}
	m.mode = modeMenu
	m.status = "download cancelled: license not accepted"
	return m, nil
}

func (m Model) updateMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.busy {
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		m.cursor = (m.cursor + itemCount - 1) % itemCount
	case "down", "j", "tab":
		m.cursor = (m.cursor + 1) % itemCount
	case "left", "h":
		m.change(-1)
	case "right", "l", " ":
		m.change(1)
	case "s":
		return m.save()
	case "enter":
		switch m.cursor {
		case itemModels:
			return m.download()
		case itemInstall:
			if err := m.hooks.Install(); err != nil {
				m.status = "install failed: " + err.Error()
			} else {
				m.status = "registered in ~/.gleann/plugins.json"
			}
		case itemSave:
			return m.save()
		default:
			m.change(1)
		}
	}
	return m, nil
}

// change steps the value under the cursor.
func (m *Model) change(d int) {
	switch m.cursor {
	case itemEngine:
		m.cfg.Engine = step(config.Engines, m.cfg.Engine, d)
	case itemProfile:
		m.cfg.Profile = step(config.Profiles, m.cfg.Profile, d)
	case itemVLM:
		m.cfg.VLM = !m.cfg.VLM
		if !m.cfg.VLM {
			m.cfg.Deep = false
		}
	case itemDeep:
		if m.cfg.VLM {
			m.cfg.Deep = !m.cfg.Deep
		} else {
			m.status = "deep tables need the Granite VLM"
		}
	case itemDPI:
		cur := m.cfg.DPI
		if cur == 0 {
			cur = dpiChoices[0]
		}
		idx := 0
		for i, v := range dpiChoices {
			if v == cur {
				idx = i
			}
		}
		m.cfg.DPI = dpiChoices[(idx+d+len(dpiChoices))%len(dpiChoices)]
	}
}

func step(list []string, cur string, d int) string {
	idx := 0
	for i, v := range list {
		if v == cur {
			idx = i
		}
	}
	return list[(idx+d+len(list))%len(list)]
}

func (m Model) save() (tea.Model, tea.Cmd) {
	m.cfg.Completed = true
	if err := m.hooks.Save(m.cfg); err != nil {
		m.cfg.Completed = false
		m.status = "save failed: " + err.Error()
		return m, nil
	}
	m.saved = true
	m.quitting = true
	return m, tea.Quit
}

// needed lists (directory, files) the current choices depend on.
type modelSet struct {
	name  string
	dir   string
	specs []docparser.ModelSpec
}

func (m Model) sets() []modelSet {
	var sets []modelSet
	switch m.cfg.Engine {
	case config.EngineIndra:
		sets = append(sets, modelSet{"Indra OCR", indra.ModelDir(), indra.Models()})
	case config.EngineOff:
	default:
		if p, err := docparser.ProfileByName(m.cfg.Profile); err == nil {
			sets = append(sets, modelSet{"Layout + PP-OCR (" + p.Name + ")", docparser.ModelDir(), docparser.DefaultModels(p)})
		}
	}
	if m.cfg.VLM {
		sets = append(sets, modelSet{"Granite-Docling VLM", docparser.VLMModelDir(), docparser.VLMModels()})
	}
	return sets
}

func (s modelSet) missing() (n int, bytes int64) {
	for _, sp := range s.specs {
		if docparser.VerifyModel(s.dir, sp) != nil {
			n++
			bytes += sp.Size
		}
	}
	return
}

func (m Model) download() (tea.Model, tea.Cmd) {
	if m.cfg.Engine == config.EngineIndra && !m.cfg.IndraLicenseAccepted {
		if n, _ := (modelSet{dir: indra.ModelDir(), specs: indra.Models()}).missing(); n > 0 {
			m.mode = modeLicense
			return m, nil
		}
	}
	return m.startDownload()
}

func (m Model) startDownload() (tea.Model, tea.Cmd) {
	var todo []modelSet
	for _, s := range m.sets() {
		if n, _ := s.missing(); n > 0 {
			todo = append(todo, s)
		}
	}
	_, ortErr := docparser.FindORTLibrary()
	needORT := ortErr != nil && docparser.ORTDownloadSupported()
	if len(todo) == 0 && !needORT {
		m.status = "everything is installed"
		return m, nil
	}
	m.busy = true
	m.status = "downloading..."
	dl := m.hooks.Download
	installORT := m.hooks.InstallORT
	return m, func() tea.Msg {
		if needORT {
			if err := installORT(context.Background()); err != nil {
				return doneMsg{"download of onnxruntime", err}
			}
		}
		for _, s := range todo {
			if err := dl(context.Background(), s.dir, s.specs); err != nil {
				return doneMsg{"download of " + s.name, err}
			}
		}
		return doneMsg{what: "download"}
	}
}

var (
	title  = lipgloss.NewStyle().Bold(true)
	dim    = lipgloss.NewStyle().Faint(true)
	sel    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	okSt   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warnSt = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
)

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (m Model) rows() [itemCount][2]string {
	dpi := m.cfg.DPI
	if dpi == 0 {
		dpi = 150
	}
	deep := onOff(m.cfg.Deep)
	if !m.cfg.VLM {
		deep = "off (needs VLM)"
	}
	return [itemCount][2]string{
		itemEngine:  {"Page parser", m.cfg.Engine + "   " + engineHint(m.cfg.Engine)},
		itemProfile: {"PP-OCR profile", m.cfg.Profile},
		itemVLM:     {"Granite VLM", onOff(m.cfg.VLM)},
		itemDeep:    {"Deep tables", deep},
		itemDPI:     {"PDF page DPI", fmt.Sprint(dpi)},
		itemModels:  {"Download models", "enter"},
		itemInstall: {"Register in gleann", "enter"},
		itemSave:    {"Save and quit", "enter  (or s)"},
	}
}

func engineHint(e string) string {
	switch e {
	case config.EngineAuto:
		return dim.Render("layout + PP-OCR when installed")
	case config.EnginePaddle:
		return dim.Render("layout + PP-OCR, downloads what is missing")
	case config.EngineIndra:
		return dim.Render("Indra OCR, CPU, ~0.5 s/page; weights have their own license")
	case config.EngineOff:
		return dim.Render("text layers and Office files only")
	}
	return ""
}

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(title.Render("gleann-plugin-docs setup") + "  " + dim.Render(config.Path()) + "\n\n")

	if m.mode == modeLicense {
		b.WriteString(warnSt.Render("License check") + "\n\n" + wrap(indra.License, 76) + "\n\n")
		b.WriteString("Download the Indra models (~9 MB)? [y/N] ")
		return b.String()
	}

	rows := m.rows()
	for i, r := range rows {
		line := fmt.Sprintf("%-20s %s", r[0], r[1])
		if item(i) == m.cursor {
			b.WriteString(sel.Render("> "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}

	b.WriteString("\n" + title.Render("Models") + "\n")
	if _, err := docparser.FindORTLibrary(); err != nil {
		b.WriteString("  " + warnSt.Render("libonnxruntime not found") + dim.Render(" (press enter on Download models to install it, or set GLEANN_ORT_LIB)") + "\n")
	} else {
		b.WriteString("  " + okSt.Render("libonnxruntime ok") + "\n")
	}
	sets := m.sets()
	if len(sets) == 0 {
		b.WriteString("  " + dim.Render("no model needed") + "\n")
	}
	for _, s := range sets {
		if n, bytes := s.missing(); n == 0 {
			b.WriteString("  " + okSt.Render("ok      ") + s.name + "\n")
		} else {
			b.WriteString("  " + warnSt.Render(fmt.Sprintf("missing ")) + fmt.Sprintf("%s (%d files, %.1f MB)", s.name, n, float64(bytes)/1e6) + "\n")
		}
	}

	if m.status != "" {
		b.WriteString("\n" + m.status + "\n")
	}
	b.WriteString("\n" + dim.Render("up/down move · left/right change · enter run · s save · q quit without saving") + "\n")
	return b.String()
}

func wrap(s string, width int) string {
	var out, line []string
	n := 0
	for _, w := range strings.Fields(s) {
		if n+len(w)+1 > width && n > 0 {
			out = append(out, strings.Join(line, " "))
			line, n = nil, 0
		}
		line = append(line, w)
		n += len(w) + 1
	}
	out = append(out, strings.Join(line, " "))
	return strings.Join(out, "\n")
}

// Run shows the setup screen and returns the config and whether it was saved.
func Run(h Hooks) (*config.Config, bool, error) {
	final, err := tea.NewProgram(New(config.Load(), h)).Run()
	if err != nil {
		return nil, false, err
	}
	m := final.(Model)
	return m.Config(), m.Saved(), nil
}
