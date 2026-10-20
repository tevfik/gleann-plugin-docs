// Package config stores the plugin's settings in ~/.gleann/docs.json, the way
// gleann-plugin-sound keeps ~/.gleann/sound.json. `gleann-plugin-docs tui`
// writes it; Apply turns it into the GLEANN_DOCLING_* variables the engine
// reads. A variable that is already set wins over the file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ConfigEnv overrides the config file location (used by tests).
const ConfigEnv = "GLEANN_DOCS_CONFIG"

// Page parser engines for scanned pages and images.
const (
	EngineAuto   = "auto"   // layout model + PP-OCR when installed (the default)
	EnginePaddle = "paddle" // layout model + PP-OCR, downloaded when missing
	EngineIndra  = "indra"  // Datalab Indra OCR models
	EngineOff    = "off"    // no ONNX page parser
)

// Engines lists the valid Engine values in menu order.
var Engines = []string{EngineAuto, EnginePaddle, EngineIndra, EngineOff}

// Profiles lists the valid PP-OCR profiles.
var Profiles = []string{"latin", "chinese"}

// Config is the persisted setting set. Zero values mean "default".
type Config struct {
	Engine  string `json:"engine,omitempty"`      // one of Engines
	Profile string `json:"ocr_profile,omitempty"` // PP-OCR profile: latin | chinese
	DPI     int    `json:"dpi,omitempty"`         // PDF rasterisation DPI (72..600)
	VLM     bool   `json:"vlm,omitempty"`         // use the Granite-Docling VLM for scanned pages
	Deep    bool   `json:"deep_tables,omitempty"` // send tables to the VLM (needs VLM)

	// IndraLicenseAccepted records that the user confirmed the Indra weights' license.
	IndraLicenseAccepted bool `json:"indra_license_accepted,omitempty"`

	Completed bool `json:"completed"`
}

// Path returns the config file location.
func Path() string {
	if p := os.Getenv(ConfigEnv); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".gleann", "docs.json")
	}
	return filepath.Join(home, ".gleann", "docs.json")
}

// Load reads the config; a missing or unreadable file gives the defaults.
func Load() *Config {
	c := &Config{}
	if data, err := os.ReadFile(Path()); err == nil {
		_ = json.Unmarshal(data, c)
	}
	return c
}

// Validate rejects values the engine would silently ignore.
func (c *Config) Validate() error {
	if c.Engine != "" && !contains(Engines, c.Engine) {
		return fmt.Errorf("engine %q: want one of %v", c.Engine, Engines)
	}
	if c.Profile != "" && !contains(Profiles, c.Profile) {
		return fmt.Errorf("ocr_profile %q: want one of %v", c.Profile, Profiles)
	}
	if c.DPI != 0 && (c.DPI < 72 || c.DPI > 600) {
		return fmt.Errorf("dpi %d: want 72..600", c.DPI)
	}
	return nil
}

// Save writes the config atomically.
func Save(c *Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Env returns the environment variables the config stands for.
func (c *Config) Env() map[string]string {
	env := map[string]string{}
	switch c.Engine {
	case EngineIndra:
		env["GLEANN_DOCLING_ONNX_ENGINE"] = "indra"
	case EnginePaddle:
		env["GLEANN_DOCLING_ONNX"] = "1"
	case EngineOff:
		env["GLEANN_DOCLING_ONNX"] = "0"
	}
	if c.Profile != "" {
		env["GLEANN_DOCLING_OCR_PROFILE"] = c.Profile
	}
	if c.DPI != 0 {
		env["GLEANN_DOCLING_DPI"] = fmt.Sprint(c.DPI)
	}
	if c.Completed {
		env["GLEANN_DOCLING_VLM"] = boolEnv(c.VLM)
		env["GLEANN_DOCLING_ONNX_DEEP"] = boolEnv(c.VLM && c.Deep)
	}
	return env
}

// Apply exports Env for every variable that is not set yet and returns the
// names it set. It does nothing for a config the TUI never completed.
func Apply() []string {
	c := Load()
	if !c.Completed || c.Validate() != nil {
		return nil
	}
	var set []string
	for k, v := range c.Env() {
		if _, ok := os.LookupEnv(k); !ok {
			_ = os.Setenv(k, v)
			set = append(set, k)
		}
	}
	return set
}

func boolEnv(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
