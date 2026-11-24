package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	_ "golang.org/x/image/bmp"  // .bmp inputs
	_ "golang.org/x/image/tiff" // .tif/.tiff inputs
	_ "golang.org/x/image/webp" // .webp inputs
)

// Extra raster formats are decoded in Go and handed to the page readers as PNG,
// because the VLM loader and the OCR path only take PNG/JPEG files.
var extraImageExts = map[string]bool{".tif": true, ".tiff": true, ".bmp": true, ".webp": true}

// Pictures embedded in Office files are read like scanned pages. Tiny images
// (icons, bullets, logos) are skipped, the count is capped.
const (
	minEmbeddedSide   = 200
	maxEmbeddedImages = 40
)

// toTempPNG decodes any supported image file and writes it as a temporary PNG.
func toTempPNG(src string) (string, func(), error) {
	img, err := decodeImage(src)
	if err != nil {
		return "", nil, err
	}
	return writeTempPNG(img)
}

func writeTempPNG(img image.Image) (string, func(), error) {
	f, err := os.CreateTemp(TempDir(), "gleann-img-*.png")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.Remove(f.Name()) }
	if err := png.Encode(f, img); err != nil {
		f.Close()
		cleanup()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return f.Name(), cleanup, nil
}

// embeddedImage is one picture found inside an OOXML package.
type embeddedImage struct {
	Name string
	Img  image.Image
}

// forEachEmbeddedImage calls fn with every decodable picture under mediaDir
// ("word/media", "ppt/media") in name order, one at a time: an image is read,
// decoded, handed to fn and released before the next one is touched, so memory
// stays at one decoded picture. EMF/WMF/SVG, tiny icons and anything
// undecodable are skipped. Member reads are charged to the conversion's zip
// budget; when it is exhausted the walk stops with ErrTooLarge. fn returns
// false to stop early.
func forEachEmbeddedImage(zipPath, mediaDir string, fn func(embeddedImage) bool) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	var names []*zip.File
	for _, f := range r.File {
		if path.Dir(f.Name) == mediaDir {
			names = append(names, f)
		}
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Name < names[j].Name })
	budget := newZipBudget()
	seen := 0
	for _, f := range names {
		if seen >= maxEmbeddedImages {
			log.Printf("[docling-eif] %s: only the first %d embedded images are read", filepath.Base(zipPath), maxEmbeddedImages)
			break
		}
		img, ok, err := readEmbeddedImage(budget, f, zipPath)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		seen++
		if !fn(embeddedImage{Name: path.Base(f.Name), Img: img}) {
			break
		}
	}
	return nil
}

// readEmbeddedImage decodes one member. ok is false for a member to skip; err
// is only ErrTooLarge from the zip budget.
func readEmbeddedImage(budget *zipBudget, f *zip.File, zipPath string) (image.Image, bool, error) {
	rc, err := openEntry(budget, f)
	if err != nil {
		return nil, false, nil
	}
	data, err := io.ReadAll(io.LimitReader(rc, 64<<20))
	rc.Close()
	if errors.Is(err, ErrTooLarge) {
		return nil, false, err
	}
	if err != nil {
		return nil, false, nil
	}
	rd := bytes.NewReader(data)
	if err := checkImageConfig(rd, MaxPixels()); err != nil {
		if errors.Is(err, ErrTooLarge) {
			log.Printf("[docling-eif] %s: embedded image %s skipped: %v", filepath.Base(zipPath), path.Base(f.Name), err)
		}
		return nil, false, nil
	}
	img, err := decodeImageSafe(rd)
	if err != nil {
		return nil, false, nil
	}
	if bd := img.Bounds(); bd.Dx() < minEmbeddedSide || bd.Dy() < minEmbeddedSide {
		return nil, false, nil
	}
	return img, true, nil
}

// appendEmbeddedImageText reads the pictures inside an Office file with the
// page reader (ONNX OCR or the VLM) and appends their text as extra sections.
// Without a reader, or when nothing is found, md is returned unchanged.
func (e *EIFDoclingEngine) appendEmbeddedImageText(ctx context.Context, md, zipPath, mediaDir string) (string, error) {
	if !e.canReadScanned() {
		return md, nil
	}
	// md is already charged to the conversion budget; the text read from the
	// pictures draws on the same one.
	_, ob := budgetFrom(ctx)
	b := newMDBuilderOn(ob)
	n := 0
	err := forEachEmbeddedImage(zipPath, mediaDir, func(im embeddedImage) bool {
		if ctx.Err() != nil {
			return false
		}
		tmp, cleanup, err := writeTempPNG(im.Img)
		if err != nil {
			return true
		}
		text, err := e.convertPage(ctx, tmp)
		cleanup()
		text = strings.TrimSpace(text)
		if err != nil || text == "" {
			return true
		}
		n++
		fmt.Fprintf(b, "\n\n## Embedded image: %s\n\n%s\n", im.Name, text)
		return true
	})
	if err != nil {
		log.Printf("[docling-eif] %s: embedded images: %v", filepath.Base(zipPath), err)
	}
	if n > 0 {
		log.Printf("[docling-eif] %s: read text from %d embedded image(s)", filepath.Base(zipPath), n)
	}
	if err := b.Err(); err != nil {
		return "", err
	}
	return md + b.String(), nil
}
