package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

func solid(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 7, 255})
		}
	}
	return img
}

func TestExtraImageFormatsDecodeToPNG(t *testing.T) {
	dir := t.TempDir()
	for ext, enc := range map[string]func(io.Writer, image.Image) error{
		".tiff": func(w io.Writer, i image.Image) error { return tiff.Encode(w, i, nil) },
		".bmp":  bmp.Encode,
	} {
		p := filepath.Join(dir, "x"+ext)
		var buf bytes.Buffer
		if err := enc(&buf, solid(64, 48)); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(p, buf.Bytes(), 0o644)
		out, cleanup, err := toTempPNG(p)
		if err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		f, _ := os.Open(out)
		img, err := png.Decode(f)
		f.Close()
		cleanup()
		if err != nil || img.Bounds().Dx() != 64 || img.Bounds().Dy() != 48 {
			t.Errorf("%s: bad PNG (%v)", ext, err)
		}
	}
	// the routing table must know the extensions
	for _, e := range []string{".tif", ".tiff", ".bmp", ".webp"} {
		if !extraImageExts[e] {
			t.Errorf("%s not routed", e)
		}
	}
}

func TestConvertTiffWithoutReaderSaysSo(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.tif")
	var buf bytes.Buffer
	tiff.Encode(&buf, solid(300, 300), nil)
	os.WriteFile(p, buf.Bytes(), 0o644)
	_, _, err := (&EIFDoclingEngine{}).Convert(context.Background(), p)
	if err == nil || strings.Contains(err.Error(), "unsupported file extension") {
		t.Fatalf("want the 'needs OCR or the VLM' error, got %v", err)
	}
}

func TestListEmbeddedImagesSkipsSmallAndUndecodable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.docx")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	add := func(name string, b []byte) { w, _ := zw.Create(name); w.Write(b) }
	enc := func(i image.Image) []byte { var b bytes.Buffer; png.Encode(&b, i); return b.Bytes() }
	add("word/media/image1.png", enc(solid(400, 300)))
	add("word/media/icon.png", enc(solid(32, 32)))
	add("word/media/image2.emf", []byte("not an image"))
	add("word/other/image3.png", enc(solid(400, 300)))
	zw.Close()
	f.Close()
	got, err := listEmbeddedImages(p, "word/media")
	if err != nil || len(got) != 1 || got[0].Name != "image1.png" {
		t.Fatalf("got %v err %v", got, err)
	}
}

// With the ONNX reader available, text inside a picture embedded in a DOCX must
// end up in the output (it was dropped before).
func TestEmbeddedImageTextIsRead(t *testing.T) {
	scan := filepath.Join("..", "..", "testdata", "documents", "scanned_table.png")
	src := filepath.Join("..", "..", "testdata", "documents", "technical_report.docx")
	if _, err := os.Stat(scan); err != nil {
		t.Skip("testdata missing")
	}
	eng := &EIFDoclingEngine{}
	eng.initHybrid()
	if eng.hybrid == nil {
		t.Skip("ONNX reader not available (set GLEANN_ORT_LIB)")
	}
	defer eng.Close()
	dst := filepath.Join(t.TempDir(), "with_image.docx")
	in, _ := zip.OpenReader(src)
	defer in.Close()
	out, _ := os.Create(dst)
	zw := zip.NewWriter(out)
	for _, f := range in.File {
		w, _ := zw.Create(f.Name)
		rc, _ := f.Open()
		io.Copy(w, rc)
		rc.Close()
	}
	w, _ := zw.Create("word/media/scan.png")
	b, _ := os.ReadFile(scan)
	w.Write(b)
	zw.Close()
	out.Close()
	md, _, err := eng.Convert(context.Background(), dst)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "Embedded image: scan.png") || !strings.Contains(md, "Document Extraction Benchmark") {
		t.Errorf("embedded image text missing:\n%s", md[max(0, len(md)-600):])
	}
}
