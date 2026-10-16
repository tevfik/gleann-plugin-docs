//go:build embedmodels

package docparser

import "embed"

// Models are copied here by `make download-doc-models` (the directory is
// git-ignored) and linked into the binary with `-tags embedmodels`.
//
//go:embed all:embedded
var embeddedFS embed.FS

func embeddedModel(name string) ([]byte, bool) {
	b, err := embeddedFS.ReadFile("embedded/" + name)
	return b, err == nil
}
