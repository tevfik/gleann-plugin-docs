package indra

import (
	"os"
	"path/filepath"

	"github.com/tevfik/gleann-plugin-docs/internal/docparser"
)

// ModelDirEnv overrides the Indra model directory.
const ModelDirEnv = "GLEANN_INDRA_MODELS"

// License is shown before the models are downloaded: the code is Apache-2.0
// but the weights carry a modified OpenRAIL-M license with commercial limits.
const License = "Indra OCR weights (datalab-to/indra-ocr) are under a modified OpenRAIL-M license: " +
	"free for research, personal use and organisations under USD 20M in revenue and funding; " +
	"larger organisations need a commercial license from Datalab, and the weights may not be used " +
	"to compete with Datalab's API. Read it before you use them: " +
	"https://huggingface.co/datalab-to/indra-ocr/blob/main/LICENSE"

// indraRev pins the Hugging Face revision the checksums below belong to.
const indraRev = "bd02167fdf2005a19962cc15188f8496a2c87d3b"

func spec(name, file, sha string, size int64) docparser.ModelSpec {
	return docparser.ModelSpec{
		Name:   name,
		File:   file,
		URL:    "https://huggingface.co/datalab-to/indra-ocr/resolve/" + indraRev + "/" + file,
		SHA256: sha,
		Size:   size,
	}
}

// Models are the files the Go port reads (Latin-script path: detector,
// recogniser, reading order and the vocabulary), about 9 MB.
func Models() []docparser.ModelSpec {
	return []docparser.ModelSpec{
		spec("det", "det.onnx", "f2bd4cba23e9c328cf7ec824634e423e191a2c6aacbbf35c62bd79dd70f62f59", 389948),
		spec("rec", "rec.onnx", "475972a2710be4a3cbfeb2bc7f3624ffd563de07de42476fc7a290d4e1ece484", 5042161),
		spec("order", "order.onnx", "a42db2e8b536ad3d84045a4b7f9b0a72667cf8be4aa02d63f7b38004719fb180", 2973138),
		spec("meta", "meta.json", "4ce4c94b46f5d644033c02e718322014ff4081270f385038c571912b56696339", 211869),
	}
}

// ModelDir returns $GLEANN_INDRA_MODELS, else ~/.gleann/models/indra.
func ModelDir() string {
	if d := os.Getenv(ModelDirEnv); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".gleann", "models", "indra")
	}
	return filepath.Join(home, ".gleann", "models", "indra")
}

// Installed reports whether every model file is present with the right checksum.
func Installed(dir string) bool {
	for _, m := range Models() {
		if docparser.VerifyModel(dir, m) != nil {
			return false
		}
	}
	return true
}
