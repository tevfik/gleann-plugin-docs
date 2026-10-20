package config

import (
	"os"
	"path/filepath"
	"testing"
)

func useTemp(t *testing.T) {
	t.Helper()
	t.Setenv(ConfigEnv, filepath.Join(t.TempDir(), "docs.json"))
}

func TestSaveLoadRoundTrip(t *testing.T) {
	useTemp(t)
	want := &Config{Engine: EngineIndra, Profile: "chinese", DPI: 200, VLM: true, Deep: true, IndraLicenseAccepted: true, Completed: true}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	if got := *Load(); got != *want {
		t.Fatalf("got %+v, want %+v", got, *want)
	}
}

func TestValidateRejects(t *testing.T) {
	useTemp(t)
	for _, c := range []Config{{Engine: "tesseract"}, {Profile: "klingon"}, {DPI: 10}, {DPI: 9000}} {
		if err := Save(&c); err == nil {
			t.Errorf("%+v was accepted", c)
		}
	}
	if _, err := os.Stat(Path()); err == nil {
		t.Error("an invalid config was written")
	}
}

func TestApplyNeverOverridesEnvironment(t *testing.T) {
	useTemp(t)
	for _, k := range []string{"GLEANN_DOCLING_ONNX_ENGINE", "GLEANN_DOCLING_DPI", "GLEANN_DOCLING_VLM", "GLEANN_DOCLING_ONNX_DEEP", "GLEANN_DOCLING_OCR_PROFILE", "GLEANN_DOCLING_ONNX"} {
		t.Setenv(k, "") // register for restore
		os.Unsetenv(k)
	}
	if err := Save(&Config{Engine: EngineIndra, DPI: 200, VLM: true, Completed: true}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_DOCLING_DPI", "300") // explicit variable wins
	Apply()
	if got := os.Getenv("GLEANN_DOCLING_DPI"); got != "300" {
		t.Errorf("DPI = %q, the variable was overridden", got)
	}
	if got := os.Getenv("GLEANN_DOCLING_ONNX_ENGINE"); got != "indra" {
		t.Errorf("engine = %q", got)
	}
	if got := os.Getenv("GLEANN_DOCLING_VLM"); got != "1" {
		t.Errorf("vlm = %q", got)
	}
	if got := os.Getenv("GLEANN_DOCLING_ONNX_DEEP"); got != "0" {
		t.Errorf("deep without its flag = %q", got)
	}
}

func TestApplyIgnoresIncompleteConfig(t *testing.T) {
	useTemp(t)
	os.Unsetenv("GLEANN_DOCLING_ONNX_ENGINE")
	t.Setenv("GLEANN_DOCLING_ONNX_ENGINE", "")
	os.Unsetenv("GLEANN_DOCLING_ONNX_ENGINE")
	_ = Save(&Config{Engine: EngineIndra}) // Completed=false
	if set := Apply(); len(set) != 0 {
		t.Errorf("applied an unfinished config: %v", set)
	}
}
