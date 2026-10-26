package config

import (
	"path/filepath"
	"testing"
)

// A value added to Config must be given a settings field (so the web form,
// the TUI and `config set` can edit it) or be listed as internal on purpose.
func TestEveryConfigValueIsASettingOrInternal(t *testing.T) {
	if missing := Settings().Uncovered(); len(missing) > 0 {
		t.Errorf("config keys without a settings field: %v", missing)
	}
}

func TestSettingsRules(t *testing.T) {
	t.Setenv(ConfigEnv, filepath.Join(t.TempDir(), "docs.json"))
	r, c := Settings(), &Config{}
	if _, errs := r.Update(c, map[string]any{"deep_tables": true}); errs[""] == "" {
		t.Errorf("deep tables without the VLM accepted: %v", errs)
	}
	if _, errs := r.Update(c, map[string]any{"deep_tables": true, "vlm": true}); len(errs) > 0 || !c.Deep {
		t.Fatalf("vlm + deep: %v %+v", errs, c)
	}
	if _, errs := r.Update(c, map[string]any{"vlm": false}); len(errs) > 0 || c.Deep {
		t.Errorf("turning the VLM off must clear deep tables: %v %+v", errs, c)
	}
	for key, bad := range map[string]any{"engine": "tesseract", "ocr_profile": "arabic", "dpi": 1000.0} {
		if err := r.Set(c, key, bad); err == nil {
			t.Errorf("%s=%v accepted", key, bad)
		}
	}
	if err := r.Set(c, "dpi", "150"); err != nil || c.DPI != 0 {
		t.Errorf("the default DPI must be stored as 0: %v %d", err, c.DPI)
	}
}

// The settings that used to exist only as environment variables reach the
// engine through Env.
func TestNewSettingsBecomeEnvironment(t *testing.T) {
	dir := t.TempDir()
	c := &Config{Completed: true, VLM: true, VLMMaxPages: 5, ForceVLM: true, Threads: 8, Offline: true, ORTLib: dir}
	env := c.Env()
	for k, want := range map[string]string{
		"GLEANN_DOCLING_VLM_MAX_PAGES": "5", "GLEANN_DOCLING_FORCE_VLM": "1", "GLEANN_DOCLING_THREADS": "8",
		"GLEANN_DOCPARSER_OFFLINE": "1", "GLEANN_ORT_LIB": dir,
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
	c.VLM = false
	if env := c.Env(); env["GLEANN_DOCLING_FORCE_VLM"] != "" || env["GLEANN_DOCLING_VLM_MAX_PAGES"] != "" {
		t.Errorf("VLM tuning without the VLM: %v", env)
	}
	if err := Settings().Set(c, "soffice", "/no/such/soffice"); err == nil {
		t.Error("a missing soffice path was accepted")
	}
}
