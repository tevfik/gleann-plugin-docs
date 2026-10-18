package docparser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ModelDirEnv overrides where the ONNX models are looked up and downloaded to.
const ModelDirEnv = "GLEANN_DOCPARSER_MODELS"

// ModelSpec describes one downloadable model file. URLs point at a pinned
// repository revision, so the SHA-256 stays valid.
type ModelSpec struct {
	Name   string // short id: "layout", "det", "rec"
	File   string // file name inside the model directory
	URL    string
	SHA256 string
	Size   int64
}

const (
	layoutRev = "96930e0f99f6792df4ead2e2d8cadbc73f993573" // Desperado-JT/RapidLayout-PP-Structure
	ocrRev    = "1cfba2e90fc938db55889873735088de210cc173" // SWHL/RapidOCR
	latinRev  = "7b02d0a30a07ba2b92ad1ff5a8941ae2c633de65" // monkt/paddleocr-onnx
	detV5Rev  = "e6f4fa85f00e168c862bc462aebca69eef9b3d3d" // PaddlePaddle/PP-OCRv5_mobile_det_onnx
)

// Model specs (all Apache-2.0).
var (
	// LayoutModel is PicoDet trained on PubLayNet: text, title, list, table, figure.
	LayoutModel = ModelSpec{
		Name:   "layout",
		File:   "layout_publaynet.onnx",
		URL:    "https://huggingface.co/Desperado-JT/RapidLayout-PP-Structure/resolve/" + layoutRev + "/layout_publaynet.onnx",
		SHA256: "958aa6dcef1cc1a542d0a513b5976a3d5edbcc37d76460ec1e9f126358e4d100",
		Size:   7413151,
	}
	// DetModel is the PP-OCRv4 DB text detector.
	DetModel = ModelSpec{
		Name:   "det",
		File:   "ch_PP-OCRv4_det_infer.onnx",
		URL:    "https://huggingface.co/SWHL/RapidOCR/resolve/" + ocrRev + "/PP-OCRv4/ch_PP-OCRv4_det_infer.onnx",
		SHA256: "d2a7720d45a54257208b1e13e36a8479894cb74155a5efe29462512d42f49da9",
		Size:   4745517,
	}
	// RecModel is the PP-OCRv4 CRNN recogniser (Chinese + English; the
	// dictionary lives in the model metadata and has no Turkish letters).
	RecModel = ModelSpec{
		Name:   "rec",
		File:   "ch_PP-OCRv4_rec_infer.onnx",
		URL:    "https://huggingface.co/SWHL/RapidOCR/resolve/" + ocrRev + "/PP-OCRv4/ch_PP-OCRv4_rec_infer.onnx",
		SHA256: "48fc40f24f6d2a207a2b1091d3437eb3cc3eb6b676dc3ef9c37384005483683b",
		Size:   10857958,
	}
	// DetV5Model is the official PP-OCRv5 mobile DB detector.
	DetV5Model = ModelSpec{
		Name:   "det-v5",
		File:   "ppocrv5_mobile_det.onnx",
		URL:    "https://huggingface.co/PaddlePaddle/PP-OCRv5_mobile_det_onnx/resolve/" + detV5Rev + "/inference.onnx",
		SHA256: "a431985659dc921974177a95adcfbb90fd9e51989a5e04d70d0b75f597b6e61d",
		Size:   4826518,
	}
	// RecLatinModel is the PP-OCRv5 Latin-script recogniser (32 languages incl.
	// Turkish); its dictionary is the separate LatinDict file.
	RecLatinModel = ModelSpec{
		Name:   "rec-latin",
		File:   "ppocrv5_latin_rec.onnx",
		URL:    "https://huggingface.co/monkt/paddleocr-onnx/resolve/" + latinRev + "/languages/latin/rec.onnx",
		SHA256: "614ffc2d6d3902d360fad7f1b0dd455ee45e877069d14c4e51a99dc4ef144409",
		Size:   7862832,
	}
	LatinDict = ModelSpec{
		Name:   "dict-latin",
		File:   "ppocrv5_latin_dict.txt",
		URL:    "https://huggingface.co/monkt/paddleocr-onnx/resolve/" + latinRev + "/languages/latin/dict.txt",
		SHA256: "3c0a8a79b612653c25f765271714f71281e4e955962c153e272b7b8c1d2b13ff",
		Size:   1634,
	}
)

// OCRProfile is a matching detector + recogniser (+ dictionary) set.
type OCRProfile struct {
	Name string
	Det  ModelSpec
	Rec  ModelSpec
	// Dict is the external character list; nil means the recogniser carries it
	// in its metadata.
	Dict *ModelSpec
}

// Profiles.
var (
	// ProfileLatin reads Latin-script text, Turkish included (default).
	ProfileLatin = OCRProfile{Name: "latin", Det: DetV5Model, Rec: RecLatinModel, Dict: &LatinDict}
	// ProfileChinese is PP-OCRv4 Chinese/English. It cannot produce ç ğ ı İ ö ş.
	ProfileChinese = OCRProfile{Name: "chinese", Det: DetModel, Rec: RecModel}
)

// ProfileByName returns a profile by name ("" = DefaultProfile).
func ProfileByName(name string) (OCRProfile, error) {
	switch strings.ToLower(name) {
	case "", "default":
		return DefaultProfile, nil
	case "latin":
		return ProfileLatin, nil
	case "chinese", "ppocrv4", "ch":
		return ProfileChinese, nil
	}
	return OCRProfile{}, fmt.Errorf("unknown OCR profile %q (use latin or chinese)", name)
}

// DefaultProfile is used when none is requested.
var DefaultProfile = ProfileLatin

// Specs lists the files of a profile.
func (p OCRProfile) Specs() []ModelSpec {
	specs := []ModelSpec{p.Det, p.Rec}
	if p.Dict != nil {
		specs = append(specs, *p.Dict)
	}
	return specs
}

// DefaultModels lists every model file the hybrid parser needs for a profile
// (the layout model plus the OCR set).
func DefaultModels(p OCRProfile) []ModelSpec {
	return append([]ModelSpec{LayoutModel}, p.Specs()...)
}

// VLM Model constants for Granite Docling BitNet 1.58b VLM
const (
	VLMModelDirEnv = "GLEANN_DOCLING_MODELS"
)

var (
	// VLMModel is the quantized BitNet b1.58 ternary vision-language model for Docling.
	VLMModel = ModelSpec{
		Name:   "vlm",
		File:   "granite_docling_bitnet_dense.eifm",
		URL:    "https://huggingface.co/tevfikk/granite-docling-258M-bitnet/resolve/main/granite_docling_bitnet_dense.eifm",
		SHA256: "0c63088453124aae2d8b8d4203265b278f932aa17aa1039c80a3aa213c0b84c9",
		Size:   263676400,
	}
	// VLMTokenizer is the byte-level BPE tokenizer for the Granite VLM.
	VLMTokenizer = ModelSpec{
		Name:   "vlm_tok",
		File:   "tokenizer.bin",
		URL:    "https://huggingface.co/tevfikk/granite-docling-258M-bitnet/resolve/main/tokenizer.bin",
		SHA256: "da9899cd5e597030102000e918482ace9ad80d31d7fb9d1943863eb4a1af1df9",
		Size:   1521105,
	}
)

// VLMModelDir returns ~/.gleann/models/granite-docling or $GLEANN_DOCLING_MODELS.
func VLMModelDir() string {
	if d := os.Getenv(VLMModelDirEnv); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".gleann", "models", "granite-docling")
	}
	return filepath.Join(home, ".gleann", "models", "granite-docling")
}

// VLMModels returns the model specs needed for the Granite VLM.
func VLMModels() []ModelSpec {
	return []ModelSpec{VLMModel, VLMTokenizer}
}

// ModelDir returns the directory holding the ONNX models:
// $GLEANN_DOCPARSER_MODELS, else ~/.gleann/models/docparser.
func ModelDir() string {
	if d := os.Getenv(ModelDirEnv); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".gleann", "models", "docparser")
	}
	return filepath.Join(home, ".gleann", "models", "docparser")
}

// ErrModelMissing is returned when a model is neither embedded nor on disk.
var ErrModelMissing = errors.New("docparser model not found")

// FileSHA256 returns the hex SHA-256 of a file.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyModel reports whether the model file in dir exists and matches its checksum.
func VerifyModel(dir string, m ModelSpec) error {
	sum, err := FileSHA256(filepath.Join(dir, m.File))
	if err != nil {
		return err
	}
	if sum != m.SHA256 {
		return fmt.Errorf("%s: checksum mismatch (got %s, want %s)", m.File, sum, m.SHA256)
	}
	return nil
}

// DownloadModel fetches m into dir unless a file with the right checksum is
// already there. The download goes to a temporary file that is verified before
// it is renamed into place, so a failed or tampered download never leaves a
// file under the final name. It reports whether a download happened.
func DownloadModel(ctx context.Context, client *http.Client, dir string, m ModelSpec) (bool, error) {
	if VerifyModel(dir, m) == nil {
		return false, nil
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("downloading %s: %w", m.File, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("downloading %s: HTTP %s", m.File, resp.Status)
	}

	tmp, err := os.CreateTemp(dir, "."+m.File+".*.part")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return false, fmt.Errorf("downloading %s: %w", m.File, err)
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != m.SHA256 {
		return false, fmt.Errorf("%s: checksum mismatch (got %s, want %s); refusing to install it", m.File, got, m.SHA256)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, m.File)); err != nil {
		return false, err
	}
	return true, nil
}

// EnsureModels downloads every missing model. progress, when non-nil, is called
// once per model with a status line.
func EnsureModels(ctx context.Context, dir string, specs []ModelSpec, progress func(string)) error {
	say := func(s string) {
		if progress != nil {
			progress(s)
		}
	}
	for _, m := range specs {
		downloaded, err := DownloadModel(ctx, nil, dir, m)
		if err != nil {
			return err
		}
		if downloaded {
			say(fmt.Sprintf("downloaded %s (%d bytes)", m.File, m.Size))
		} else {
			say(fmt.Sprintf("ok        %s", m.File))
		}
	}
	return nil
}

// loadModel returns the model bytes: embedded copy first (build tag
// embedmodels), then the model directory.
func loadModel(dir string, m ModelSpec) ([]byte, error) {
	if b, ok := embeddedModel(m.File); ok {
		return b, nil
	}
	if dir == "" {
		dir = ModelDir()
	}
	b, err := os.ReadFile(filepath.Join(dir, m.File))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s (run `gleann-plugin-docs models download`)", ErrModelMissing, filepath.Join(dir, m.File))
		}
		return nil, err
	}
	return b, nil
}
