package engine

import (
	"archive/zip"
	"context"
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
	f, err := os.CreateTemp("", "gleann-img-*.png")
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

// listEmbeddedImages returns the decodable pictures under mediaDir ("word/media",
// "ppt/media") in name order. EMF/WMF/SVG and anything undecodable are skipped.
func listEmbeddedImages(zipPath, mediaDir string) ([]embeddedImage, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var names []*zip.File
	for _, f := range r.File {
		if path.Dir(f.Name) == mediaDir {
			names = append(names, f)
		}
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Name < names[j].Name })
	var out []embeddedImage
	for _, f := range names {
		if len(out) >= maxEmbeddedImages {
			log.Printf("[docling-eif] %s: only the first %d embedded images are read", filepath.Base(zipPath), maxEmbeddedImages)
			break
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		img, _, err := image.Decode(io.LimitReader(rc, 64<<20))
		rc.Close()
		if err != nil {
			continue
		}
		b := img.Bounds()
		if b.Dx() < minEmbeddedSide || b.Dy() < minEmbeddedSide {
			continue
		}
		out = append(out, embeddedImage{Name: path.Base(f.Name), Img: img})
	}
	return out, nil
}

// appendEmbeddedImageText reads the pictures inside an Office file with the
// page reader (ONNX OCR or the VLM) and appends their text as extra sections.
// Without a reader, or when nothing is found, md is returned unchanged.
func (e *EIFDoclingEngine) appendEmbeddedImageText(ctx context.Context, md, zipPath, mediaDir string) string {
	if !e.canReadScanned() {
		return md
	}
	imgs, err := listEmbeddedImages(zipPath, mediaDir)
	if err != nil || len(imgs) == 0 {
		return md
	}
	var b strings.Builder
	b.WriteString(md)
	n := 0
	for _, im := range imgs {
		if ctx.Err() != nil {
			break
		}
		tmp, cleanup, err := writeTempPNG(im.Img)
		if err != nil {
			continue
		}
		text, err := e.convertPage(ctx, tmp)
		cleanup()
		text = strings.TrimSpace(text)
		if err != nil || text == "" {
			continue
		}
		n++
		fmt.Fprintf(&b, "\n\n## Embedded image: %s\n\n%s\n", im.Name, text)
	}
	if n > 0 {
		log.Printf("[docling-eif] %s: read text from %d embedded image(s)", filepath.Base(zipPath), n)
	}
	return b.String()
}
