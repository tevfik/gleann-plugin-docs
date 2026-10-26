package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tevfik/gleann-plugin-docs/internal/docparser"
	"github.com/tevfik/gleann-plugin-docs/internal/indra"
	"github.com/tevfik/gleann/modules/settings"
)

// modelSet is a catalog entry: files that are only useful together.
type modelSet struct {
	id, name, group, note string
	dir                   func() string
	specs                 []docparser.ModelSpec
}

func modelSets() []modelSet {
	return []modelSet{
		{"layout-latin", "Layout + PP-OCR (latin)", "Page parser", "latin script, includes Turkish",
			docparser.ModelDir, docparser.DefaultModels(docparser.ProfileLatin)},
		{"layout-chinese", "Layout + PP-OCR (chinese)", "Page parser", "Chinese and English",
			docparser.ModelDir, docparser.DefaultModels(docparser.ProfileChinese)},
		{"granite-vlm", "Granite-Docling VLM", "Vision model", "for the Granite VLM setting",
			docparser.VLMModelDir, docparser.VLMModels()},
		{"indra", "Indra OCR", "Page parser", "for the indra engine",
			indra.ModelDir, indra.Models()},
	}
}

// present reports whether every file of a set is on disk with its expected
// size. Downloads verify checksums; listing does not re-hash hundreds of MB.
func present(dir string, specs []docparser.ModelSpec) bool {
	for _, s := range specs {
		fi, err := os.Stat(filepath.Join(dir, s.File))
		if err != nil || (s.Size > 0 && fi.Size() != s.Size) {
			return false
		}
	}
	return true
}

func setSize(specs []docparser.ModelSpec) int64 {
	var n int64
	for _, s := range specs {
		n += s.Size
	}
	return n
}

// Catalog offers the model sets and the ONNX Runtime for download
// (GET/POST /models), the same files `models download` and the Setup
// screen fetch. Indra's weights wait for their license to be accepted.
func Catalog() *settings.Catalog {
	return &settings.Catalog{
		List: func() []settings.Model {
			_, ortErr := docparser.FindORTLibrary()
			out := []settings.Model{{
				ID: "onnxruntime", Name: "ONNX Runtime", Group: "Runtime",
				Note: "runs the page parsers", Installed: ortErr == nil,
			}}
			licensed := Load().IndraLicenseAccepted
			for _, s := range modelSets() {
				m := settings.Model{
					ID: s.id, Name: s.name, Group: s.group, Note: s.note,
					Size: settings.HumanSize(setSize(s.specs)), Installed: present(s.dir(), s.specs),
				}
				if s.id == "indra" && !licensed {
					m.Blocked = "accept the Indra model license in the settings first"
				}
				out = append(out, m)
			}
			return out
		},
		Download: func(ctx context.Context, id string, report func(settings.Progress)) error {
			say := func(msg string) { report(settings.Progress{Message: msg}) }
			if id == "onnxruntime" {
				_, err := docparser.InstallORT(ctx, "", say)
				return err
			}
			for _, s := range modelSets() {
				if s.id != id {
					continue
				}
				if id == "indra" && !Load().IndraLicenseAccepted {
					return fmt.Errorf("accept the Indra model license first")
				}
				if err := os.MkdirAll(s.dir(), 0o755); err != nil {
					return err
				}
				return docparser.EnsureModels(ctx, s.dir(), s.specs, say)
			}
			return fmt.Errorf("unknown model %q", id)
		},
	}
}
