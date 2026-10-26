// Package tui is the interactive setup of gleann-plugin-docs (`gleann-plugin-docs tui`):
// pick the page parser, see which models are installed, download them and
// register the plugin with gleann. It writes ~/.gleann/docs.json (package config).
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

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
	// Update checks for and installs the latest release.
	Update func() error
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
	itemUpdate
	itemSave
	itemCount
)

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
	fillDefaults(cfg)
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
		case itemUpdate:
			return m.updatePlugin()
		case itemSave:
			return m.save()
		default:
			m.change(1)
		}
	}
	return m, nil
}

func (m Model) updatePlugin() (tea.Model, tea.Cmd) {
	if m.hooks.Update == nil {
		m.status = "update not configured"
		return m, nil
	}
	m.busy = true
	m.status = "checking and installing update..."
	return m, func() tea.Msg {
		err := m.hooks.Update()
		return doneMsg{what: "update", err: err}
	}
}

// settingKeys maps menu rows to config.Settings fields, which supply the
// values a row steps through and the rules (the same ones the web form and
// `config set` use).
var settingKeys = map[item]string{
	itemEngine: "engine", itemProfile: "ocr_profile", itemVLM: "vlm", itemDeep: "deep_tables", itemDPI: "dpi",
}

// change steps the value under the cursor.
func (m *Model) change(d int) {
	key, ok := settingKeys[m.cursor]
	if !ok {
		return
	}
	reg, next := config.Settings(), *m.cfg
	err := reg.Step(&next, key, d)
	if err == nil {
		err = reg.Validate(&next)
	}
	if err != nil {
		m.status = err.Error()
		return
	}
	fillDefaults(&next)
	*m.cfg = next
}

// fillDefaults spells out the defaults the menu shows.
func fillDefaults(cfg *config.Config) {
	if cfg.Engine == "" {
		cfg.Engine = config.EngineAuto
	}
	if cfg.Profile == "" {
		cfg.Profile = "latin"
	}
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

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

type itemMeta struct {
	icon  string
	title string
	value string
	desc  string
}

func (m Model) itemsMeta() [itemCount]itemMeta {
	dpi := m.cfg.DPI
	if dpi == 0 {
		dpi = 150
	}
	deepVal := onOff(m.cfg.Deep)
	deepDesc := "Table extraction with cell-level bounding boxes"
	if !m.cfg.VLM {
		deepVal = "off"
		deepDesc = "Table structure extraction (requires Granite VLM)"
	}
	return [itemCount]itemMeta{
		itemEngine: {
			icon:  "⚙ ",
			title: "Page Parser",
			value: m.cfg.Engine,
			desc:  engineHint(m.cfg.Engine),
		},
		itemProfile: {
			icon:  "🔤",
			title: "PP-OCR Profile",
			value: m.cfg.Profile,
			desc:  "Target language/character set (latin, cyrillic, devanagari)",
		},
		itemVLM: {
			icon:  "👁 ",
			title: "Granite VLM",
			value: onOff(m.cfg.VLM),
			desc:  "Visual language model for dense complex pages & layouts",
		},
		itemDeep: {
			icon:  "📊",
			title: "Deep Tables",
			value: deepVal,
			desc:  deepDesc,
		},
		itemDPI: {
			icon:  "🔍",
			title: "PDF Page DPI",
			value: fmt.Sprintf("%d dpi", dpi),
			desc:  "Rasterization resolution (150 = fast, 200 = balanced, 300 = crisp)",
		},
		itemModels: {
			icon:  "⬇ ",
			title: "Download Models",
			value: "enter to fetch",
			desc:  "Verify & download missing ONNX/VLM model weights",
		},
		itemInstall: {
			icon:  "🔌",
			title: "Register in Gleann",
			value: "enter to register",
			desc:  "Save capability registration to ~/.gleann/plugins.json",
		},
		itemUpdate: {
			icon:  "⬆ ",
			title: "Update Plugin",
			value: "enter to update",
			desc:  "Check for and install latest release from GitHub",
		},
		itemSave: {
			icon:  "💾",
			title: "Save and Quit",
			value: "enter or 's'",
			desc:  "Persist settings to ~/.gleann/docs.json and exit",
		},
	}
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
		itemUpdate:  {"Update plugin", "enter"},
		itemSave:    {"Save and quit", "enter  (or s)"},
	}
}

func engineHint(e string) string {
	switch e {
	case config.EngineAuto:
		return "layout + PP-OCR when installed"
	case config.EnginePaddle:
		return "layout + PP-OCR, downloads what is missing"
	case config.EngineIndra:
		return "Indra OCR, CPU, ~0.5 s/page; weights have their own license"
	case config.EngineOff:
		return "text layers and Office files only"
	}
	return ""
}

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(Logo())
	b.WriteString("\n")
	b.WriteString(SubtitleStyle.Render("  Local document extraction & OCR companion for gleann"))
	b.WriteString("\n")
	b.WriteString(DescStyle.Render("  Config: " + config.Path()))
	b.WriteString("\n\n")

	if m.mode == modeLicense {
		b.WriteString(WarnBadge.Render("  ⚠ License Confirmation (Indra Models)") + "\n\n")
		b.WriteString("  " + wrap(indra.License, 76) + "\n\n")
		b.WriteString("  Download the Indra models (~9 MB)? [y/N] ")
		return b.String()
	}

	items := m.itemsMeta()
	for i, it := range items {
		active := item(i) == m.cursor
		if active {
			b.WriteString(ActiveItemStyle.Render(fmt.Sprintf("▸ %s %-18s %s", it.icon, it.title, ValueBadgeActive.Render("["+it.value+"]"))))
			b.WriteString("\n")
			b.WriteString(ActiveDescStyle.Render(fmt.Sprintf("  %s", it.desc)))
			b.WriteString("\n")
		} else {
			b.WriteString(NormalItemStyle.Render(fmt.Sprintf("  %s %-18s %s", it.icon, it.title, ValueBadgeNormal.Render("["+it.value+"]"))))
			b.WriteString("\n")
			b.WriteString(DescStyle.Render(fmt.Sprintf("  %s", it.desc)))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(SectionHeaderStyle.Render("  📦 Installed Models & Runtime"))
	b.WriteString("\n")

	if _, err := docparser.FindORTLibrary(); err != nil {
		b.WriteString("  " + WarnBadge.Render("⚠ missing ") + "libonnxruntime" + DescStyle.Render(" (press enter on Download models or set GLEANN_ORT_LIB)") + "\n")
	} else {
		b.WriteString("  " + SuccessBadge.Render("✓ ok      ") + "libonnxruntime runtime active\n")
	}

	sets := m.sets()
	if len(sets) == 0 {
		b.WriteString("  " + DescStyle.Render("no additional models needed for current engine") + "\n")
	}
	for _, s := range sets {
		if n, bytes := s.missing(); n == 0 {
			b.WriteString("  " + SuccessBadge.Render("✓ ok      ") + s.name + "\n")
		} else {
			b.WriteString("  " + WarnBadge.Render("⚠ missing ") + fmt.Sprintf("%-28s (%d files, %.1f MB)", s.name, n, float64(bytes)/1e6) + "\n")
		}
	}

	if m.status != "" {
		b.WriteString("\n  " + ActiveDescStyle.Render("ℹ "+m.status) + "\n")
	}

	b.WriteString("\n" + StatusBarStyle.Render("  ↑/↓ move · ←/→ adjust · enter select · s save · q quit without saving") + "\n")
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

// RunApp starts the full interactive TUI application loop with the home menu.
func RunApp(port int) error {
	for {
		home := NewHomeModel()
		p := tea.NewProgram(home, tea.WithAltScreen())
		result, err := p.Run()
		if err != nil {
			return fmt.Errorf("home screen: %w", err)
		}
		h := result.(HomeModel)
		if h.Quitting() {
			return nil
		}

		switch h.Chosen() {
		case ScreenSetup:
			if err := runSetupWizard(port); err != nil {
				return err
			}
		case ScreenConvert:
			runCLIMode("convert")
		case ScreenServe:
			runServeToggle(port)
		case ScreenModels:
			runCLIMode("models", "verify")
		case ScreenUpdate:
			runSubcommand("update")
		case ScreenInstall:
			runSubcommand("install")
		case ScreenUninstall:
			runSubcommand("uninstall")
		case ScreenSettings:
			if _, err := tea.NewProgram(NewSettingsModel(), tea.WithAltScreen()).Run(); err != nil {
				return fmt.Errorf("settings: %w", err)
			}
		}
	}
}

func runSetupWizard(port int) error {
	cfg := config.Load()
	// The Setup screen stays on while these run, so their output must not go
	// to the terminal; the screen reports the outcome on its status line.
	h := Hooks{
		Install: func() error { return runQuiet("install") },
		Update:  func() error { return runQuiet("update") },
	}
	m := New(cfg, h)
	p := tea.NewProgram(m, tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	sm := result.(Model)
	if sm.Saved() {
		_ = config.Save(sm.Config())
	}
	return nil
}

// runQuiet runs another gleann-plugin-docs command without the terminal and
// returns its last output lines as the error when it fails.
func runQuiet(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	out, err := exec.Command(exe, args...).CombinedOutput()
	if err != nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 2 {
			lines = lines[len(lines)-2:]
		}
		return fmt.Errorf("%v: %s", err, strings.Join(lines, " / "))
	}
	return nil
}

func runSubcommand(args ...string) {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := runForeground(cmd); err != nil {
		fmt.Fprintf(os.Stderr, "\n%v\n", err)
	}
	fmt.Println("\nPress enter to return to menu...")
	buf := make([]byte, 1)
	_, _ = os.Stdin.Read(buf)
}

func runCLIMode(mode string, extraArgs ...string) {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	var args []string
	if mode == "convert" {
		fmt.Print("Enter document file path to convert: ")
		var path string
		_, _ = fmt.Scanln(&path)
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		args = []string{"convert", path}
	} else {
		args = append([]string{mode}, extraArgs...)
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	_ = runForeground(cmd)
	fmt.Println("\nPress enter to return to menu...")
	buf := make([]byte, 1)
	_, _ = os.Stdin.Read(buf)
}

// runForeground runs cmd in this terminal. Ctrl+C reaches the whole
// foreground process group, so while cmd runs this process ignores it: the
// key stops the command and the menu comes back instead of closing.
func runForeground(cmd *exec.Cmd) error {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	return cmd.Run()
}
