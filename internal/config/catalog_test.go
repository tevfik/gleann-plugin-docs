package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann-plugin-docs/internal/docparser"
)

func TestCatalogStates(t *testing.T) {
	t.Setenv(ConfigEnv, filepath.Join(t.TempDir(), "docs.json"))
	models := t.TempDir()
	t.Setenv("GLEANN_DOCPARSER_MODELS", models)
	t.Setenv("GLEANN_DOCLING_MODELS", t.TempDir())
	t.Setenv("GLEANN_INDRA_MODELS", t.TempDir())

	// The latin set is on disk with the right sizes; the chinese one is not.
	for _, s := range docparser.DefaultModels(docparser.ProfileLatin) {
		_ = os.WriteFile(filepath.Join(models, s.File), make([]byte, s.Size), 0o644)
	}
	got := map[string]bool{}
	blocked := map[string]string{}
	for _, m := range Catalog().List() {
		got[m.ID], blocked[m.ID] = m.Installed, m.Blocked
	}
	if !got["layout-latin"] || got["layout-chinese"] || got["granite-vlm"] {
		t.Errorf("installed: %v", got)
	}
	if blocked["indra"] == "" {
		t.Error("indra must wait for its license")
	}
	_ = Save(&Config{IndraLicenseAccepted: true})
	for _, m := range Catalog().List() {
		if m.ID == "indra" && m.Blocked != "" {
			t.Errorf("indra still blocked after the license was accepted")
		}
	}
}
