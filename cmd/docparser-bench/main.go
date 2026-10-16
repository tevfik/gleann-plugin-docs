// docparser-bench measures the ONNX hybrid page parser (layout + OCR) on a PDF
// that has a text layer: the text layer is the ground truth, the page is
// rasterised, parsed from pixels only, and the two are compared.
//
//	go run ./cmd/docparser-bench -pdf paper.pdf -pages 1-3 -dpi 150
//
// It reports per-stage latency and word recall/precision and character error
// rate. It does not run the Granite-Docling (eif-runtime) deep path.
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tevfik/gleann-plugin-docs/internal/docparser"
)

func main() {
	pdf := flag.String("pdf", "", "PDF with a text layer (ground truth)")
	pages := flag.String("pages", "1", "page or range, e.g. 2 or 1-3")
	dpi := flag.Int("dpi", 150, "rasterisation resolution")
	models := flag.String("models", "", "model directory (default ~/.gleann/models/docparser)")
	profile := flag.String("profile", "", "OCR model set: latin (default) or chinese")
	workers := flag.Int("workers", 0, "OCR workers (0 = default)")
	threads := flag.Int("threads", 0, "threads per ONNX session (0 = default)")
	unclip := flag.Float64("unclip", 0, "DB unclip ratio (0 = default 1.6)")
	boxThresh := flag.Float64("boxthresh", 0, "DB box threshold (0 = default 0.5)")
	thresh := flag.Float64("thresh", 0, "DB binarisation threshold (0 = default 0.3)")
	maxSide := flag.Int("maxside", 0, "detector longest side (0 = default 960)")
	layoutScore := flag.Float64("layout-score", 0, "layout confidence threshold (0 = default 0.4)")
	show := flag.Bool("show", false, "print the Markdown of every page")
	overlay := flag.String("overlay", "", "directory to write page PNGs with the detected blocks drawn on them")
	flag.Parse()

	if *pdf == "" {
		flag.Usage()
		os.Exit(2)
	}
	first, last, err := pageRange(*pages)
	if err != nil {
		fatal(err)
	}

	loadStart := time.Now()
	p, err := docparser.New(docparser.Options{
		ModelDir:       *models,
		Profile:        *profile,
		Workers:        *workers,
		IntraThreads:   *threads,
		LayoutMinScore: float32(*layoutScore),
		OCR: docparser.OCROptions{
			Thresh: float32(*thresh), BoxThresh: float32(*boxThresh),
			Unclip: *unclip, MaxSide: *maxSide,
		},
	})
	if err != nil {
		fatal(err)
	}
	defer p.Close()
	fmt.Printf("models loaded in %dms\n", time.Since(loadStart).Milliseconds())

	tmp, err := os.MkdirTemp("", "docparser-bench-*")
	if err != nil {
		fatal(err)
	}
	defer os.RemoveAll(tmp)

	fmt.Printf("%-5s %6s %6s %8s %8s %8s %8s %7s %7s %7s %8s\n",
		"page", "blocks", "lines", "layout", "det", "rec(Σ)", "total", "recall", "prec", "CER", "recall*")
	var sumRecall, sumPrec, sumCER, sumFolded float64
	var sumTotal time.Duration
	n := 0
	for pg := first; pg <= last; pg++ {
		prefix := filepath.Join(tmp, fmt.Sprintf("p%d", pg))
		if out, err := exec.Command("pdftoppm", "-png", "-r", strconv.Itoa(*dpi), "-f", strconv.Itoa(pg), "-l", strconv.Itoa(pg), "-singlefile", *pdf, prefix).CombinedOutput(); err != nil {
			fatal(fmt.Errorf("pdftoppm page %d: %v: %s", pg, err, out))
		}
		f, err := os.Open(prefix + ".png")
		if err != nil {
			fatal(err)
		}
		img, err := png.Decode(f)
		f.Close()
		if err != nil {
			fatal(err)
		}
		gt, err := exec.Command("pdftotext", "-f", strconv.Itoa(pg), "-l", strconv.Itoa(pg), *pdf, "-").Output()
		if err != nil {
			fatal(fmt.Errorf("pdftotext page %d: %w", pg, err))
		}

		blocks, t, err := p.ParsePageBlocks(context.Background(), img)
		if err != nil {
			fatal(fmt.Errorf("page %d: %w", pg, err))
		}
		if *overlay != "" {
			if err := writeOverlay(filepath.Join(*overlay, fmt.Sprintf("page%d.png", pg)), img, blocks); err != nil {
				fatal(err)
			}
		}
		var md strings.Builder
		for _, b := range blocks {
			md.WriteString(b.Markdown + "\n\n")
		}
		recall, prec := docparser.WordScores(string(gt), md.String())
		cer := docparser.CER(string(gt), md.String())
		foldedRecall, _ := docparser.WordScores(docparser.FoldDiacritics(string(gt)), docparser.FoldDiacritics(md.String()))
		fmt.Printf("%-5d %6d %6d %8s %8s %8s %8s %6.1f%% %6.1f%% %6.1f%% %7.1f%%\n", pg, t.Blocks, t.Lines,
			ms(t.Layout), ms(t.Det), ms(t.Rec), ms(t.Total), 100*recall, 100*prec, 100*cer, 100*foldedRecall)
		sumFolded += foldedRecall
		if *show {
			fmt.Println(strings.Repeat("-", 60))
			fmt.Print(md.String())
			fmt.Println(strings.Repeat("-", 60))
		}
		sumRecall, sumPrec, sumCER, sumTotal = sumRecall+recall, sumPrec+prec, sumCER+cer, sumTotal+t.Total
		n++
	}
	if n > 0 {
		fmt.Printf("mean  %6s %6s %8s %8s %8s %8s %6.1f%% %6.1f%% %6.1f%% %7.1f%%\n", "", "", "", "", "", ms(sumTotal/time.Duration(n)),
			100*sumRecall/float64(n), 100*sumPrec/float64(n), 100*sumCER/float64(n), 100*sumFolded/float64(n))
		fmt.Println("recall* = word recall after folding Turkish letters to ASCII (ı→i, ş→s, ...)")
	}
}

func ms(d time.Duration) string { return fmt.Sprintf("%dms", d.Milliseconds()) }

func pageRange(s string) (int, int, error) {
	a, b, ok := strings.Cut(s, "-")
	first, err := strconv.Atoi(a)
	if err != nil || first < 1 {
		return 0, 0, fmt.Errorf("bad -pages %q", s)
	}
	if !ok {
		return first, first, nil
	}
	last, err := strconv.Atoi(b)
	if err != nil || last < first {
		return 0, 0, fmt.Errorf("bad -pages %q", s)
	}
	return first, last, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

// writeOverlay saves img with each block outlined: text green, title blue,
// list orange, table red, figure magenta.
func writeOverlay(path string, img image.Image, blocks []docparser.ParsedBlock) error {
	dst := image.NewRGBA(img.Bounds())
	draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Src)
	colors := map[docparser.ElementType]color.RGBA{
		docparser.TypeText: {0, 160, 0, 255}, docparser.TypeTitle: {0, 80, 255, 255},
		docparser.TypeList: {255, 140, 0, 255}, docparser.TypeTable: {255, 0, 0, 255},
		docparser.TypeFigure: {255, 0, 255, 255},
	}
	for _, b := range blocks {
		c := colors[b.BBox.Type]
		r := b.BBox.Rect
		for t := 0; t < 3; t++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				dst.Set(x, r.Min.Y+t, c)
				dst.Set(x, r.Max.Y-1-t, c)
			}
			for y := r.Min.Y; y < r.Max.Y; y++ {
				dst.Set(r.Min.X+t, y, c)
				dst.Set(r.Max.X-1-t, y, c)
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, dst)
}
