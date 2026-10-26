package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann-plugin-docs/internal/docparser"
	"github.com/tevfik/gleann-plugin-docs/internal/indra"
	"github.com/tevfik/gleann/modules/settings"
)

// Settings lists every user-editable setting once. The web form (GET/PUT
// /settings), the TUI and `config set` all edit through it, so they offer the
// same values and refuse the same mistakes.
func Settings() *settings.Registry[Config] {
	return &settings.Registry[Config]{
		Title:      "gleann-plugin-docs",
		ModelsPath: "/models",
		Note:       "Saved to ~/.gleann/docs.json. Restart the plugin (or gleann) to apply.",
		Load:       func() (*Config, error) { return Load(), nil },
		Save:       Save,
		// completed is set by every save.
		Internal: []string{"completed"},
		Validate: func(c *Config) error {
			if c.Deep && !c.VLM {
				return errors.New("deep tables need the Granite VLM")
			}
			return c.Validate()
		},
		// Apply ignores a config that was never completed.
		BeforeSave: func(c *Config) { c.Completed = true },
		Fields: []settings.Field[Config]{
			{Key: "engine", Label: "Page parser", Type: settings.Select, Group: "Scanned pages", Default: EngineAuto,
				Help: "How scanned pages and images are read.",
				Options: settings.Opts(
					EngineAuto, "auto — layout model + PP-OCR when installed",
					EnginePaddle, "paddle — layout model + PP-OCR, downloaded when missing",
					EngineIndra, "indra — Datalab Indra OCR models",
					EngineOff, "off — no ONNX page parser"),
				Bind: func(c *Config) any { return &c.Engine }},
			{Key: "ocr_profile", Label: "PP-OCR profile", Type: settings.Select, Group: "Scanned pages", Default: "latin",
				Help:    "Script of the documents: latin covers Turkish and other European languages.",
				Options: settings.Opts("latin", "latin", "chinese", "chinese"),
				Bind:    func(c *Config) any { return &c.Profile }},
			{Key: "dpi", Label: "DPI", Type: settings.Number, Group: "Scanned pages", Default: 150,
				Help: "Resolution PDF pages are rendered at for OCR. Higher reads small print better and is slower.",
				Min:  settings.F(72), Max: settings.F(600),
				Options: settings.Opts("150", "150", "200", "200", "300", "300"),
				Bind:    func(c *Config) any { return &c.DPI }},
			{Key: "vlm", Label: "Granite VLM", Type: settings.Bool, Group: "Vision model",
				Help: "Read scanned pages with the Granite-Docling vision model (slower, better layout).",
				Bind: func(c *Config) any { return &c.VLM },
				Check: func(c *Config, v any) (any, error) {
					if v == false {
						c.Deep = false // deep tables need the VLM
					}
					return v, nil
				}},
			{Key: "deep_tables", Label: "Deep tables", Type: settings.Bool, Group: "Vision model",
				Help:      "Send tables to the VLM as well.",
				VisibleIf: &settings.Cond{Key: "vlm", Equals: true},
				Bind:      func(c *Config) any { return &c.Deep }},
			{Key: "vlm_max_pages", Label: "VLM page budget", Type: settings.Number, Group: "Vision model",
				Help: "Scanned pages per document the VLM reads before the faster ONNX parser takes the rest. " +
					"0 = the VLM reads none while the ONNX parser is installed.",
				Min: settings.F(0), Max: settings.F(10000), Step: settings.F(1),
				VisibleIf: &settings.Cond{Key: "vlm", Equals: true},
				Bind:      func(c *Config) any { return &c.VLMMaxPages }},
			{Key: "force_vlm", Label: "VLM for every page", Type: settings.Bool, Group: "Vision model",
				Help:      "Render every PDF page and read it with the VLM, even pages with a text layer. Slow.",
				VisibleIf: &settings.Cond{Key: "vlm", Equals: true},
				Bind:      func(c *Config) any { return &c.ForceVLM }},
			{Key: "threads", Label: "CPU threads", Type: settings.Number, Group: "Performance",
				Help:    "Threads the VLM uses (at least 4). 0 = half the cores; using all cores is slower on a busy machine.",
				Default: 0, Min: settings.F(4), Max: settings.F(256), Step: settings.F(1), Apply: settings.Restart,
				Bind: func(c *Config) any { return &c.Threads }},
			{Key: "offline", Label: "Offline", Type: settings.Bool, Group: "Performance",
				Help: "Never download missing models; pages that need them are skipped.",
				Bind: func(c *Config) any { return &c.Offline }},
			{Key: "indra_license_accepted", Label: "Accept the Indra model license", Type: settings.Bool, Group: "Scanned pages",
				Help: "The Indra weights are under a modified OpenRAIL-M license: free for research, personal use and " +
					"organisations under USD 20M in revenue and funding. Read it before accepting: " +
					"https://huggingface.co/datalab-to/indra-ocr/blob/main/LICENSE",
				VisibleIf: &settings.Cond{Key: "engine", Equals: EngineIndra},
				Bind:      func(c *Config) any { return &c.IndraLicenseAccepted }},
			// Leave these empty: each is found automatically, and the empty
			// field shows what was found. Set one only to override it.
			pathField("ort_lib", "ONNX Runtime library", "libonnxruntime to load (1.24 or newer). Empty = ~/.gleann/lib, then the system.",
				func(c *Config) any { return &c.ORTLib }, foundORT),
			pathField("soffice", "LibreOffice (soffice)", "Converts .doc, .ppt and .xls. Empty = found on PATH.",
				func(c *Config) any { return &c.SOffice }, foundSoffice),
			pathField("parser_models", "Layout + OCR models", "Directory of the layout and PP-OCR models.",
				func(c *Config) any { return &c.ParserModels }, func(*Config) string { return docparser.ModelDir() }),
			pathField("vlm_models", "VLM models", "Directory of the Granite-Docling VLM.",
				func(c *Config) any { return &c.VLMModels }, func(*Config) string { return docparser.VLMModelDir() }),
			pathField("indra_models", "Indra models", "Directory of the Indra OCR models.",
				func(c *Config) any { return &c.IndraModels }, func(*Config) string { return indra.ModelDir() }),
		},
	}
}

// foundORT is the libonnxruntime the automatic lookup picks.
func foundORT(*Config) string {
	if p, err := docparser.FindORTLibrary(); err == nil {
		return p
	}
	return "not found (Setup -> Download Models installs it)"
}

// foundSoffice is the LibreOffice binary on PATH.
func foundSoffice(*Config) string {
	for _, n := range []string{"soffice", "libreoffice"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return "not found (needed only for .doc, .ppt and .xls)"
}

// pathField is an optional path in the Paths group that must exist when set;
// found describes what an empty value resolves to.
func pathField(key, label, help string, bind func(*Config) any, found func(*Config) string) settings.Field[Config] {
	return settings.Field[Config]{Key: key, Label: label, Type: settings.Path, Group: "Paths", Help: help, Bind: bind, Placeholder: found,
		Check: func(_ *Config, v any) (any, error) {
			p := v.(string)
			if p == "" {
				return "", nil
			}
			if strings.HasPrefix(p, "~/") {
				if home, err := os.UserHomeDir(); err == nil {
					p = filepath.Join(home, p[2:])
				}
			}
			if _, err := os.Stat(p); err != nil {
				return nil, fmt.Errorf("%s does not exist", p)
			}
			return p, nil
		}}
}
