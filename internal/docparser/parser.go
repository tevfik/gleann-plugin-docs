package docparser

import (
	"context"
	"image"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tevfik/gleann-plugin-docs/internal/parser"
)

// DeepRecognizer converts a region with a heavy vision-language model
// (Granite-Docling through eif-runtime). page is the whole page and box the
// region on it; the result is DocTags/OTSL text. An error or an empty result
// makes the parser fall back to its OCR-only reconstruction.
type DeepRecognizer interface {
	Recognize(ctx context.Context, page image.Image, box BoundingBox) (docTags string, err error)
}

// Options configure a Parser. The zero value is usable.
type Options struct {
	ModelDir     string // "" = ModelDir()
	Profile      string // OCR model set: "latin" (default) or "chinese"
	Workers      int    // concurrent OCR workers (default min(4, NumCPU/2), at least 1)
	IntraThreads int    // threads per ONNX session (default: NumCPU/Workers, at least 1)
	OCR          OCROptions
	// LayoutMinScore overrides the layout confidence threshold (default 0.4).
	LayoutMinScore float32
	// Deep, when set, receives table blocks (and figures when DeepFigures is true).
	Deep        DeepRecognizer
	DeepFigures bool
	// DisableRecovery turns off reading text the layout model did not cover.
	DisableRecovery bool
	// KeepFurniture keeps recovered text in the header/footer bands (running
	// titles, page numbers, download notices), which is dropped by default.
	KeepFurniture bool
}

// Parser turns page images into Markdown.
type Parser struct {
	layout *LayoutDetector
	ocr    *OCR
	opts   Options
}

// New loads the layout and OCR models.
func New(opts Options) (*Parser, error) {
	cpus := runtime.NumCPU()
	if opts.Workers <= 0 {
		opts.Workers = min(4, max(1, cpus/2))
	}
	if opts.IntraThreads <= 0 {
		opts.IntraThreads = max(1, cpus/opts.Workers)
	}
	layout, err := NewLayoutDetector(opts.ModelDir, opts.IntraThreads)
	if err != nil {
		return nil, err
	}
	layout.MinScore = opts.LayoutMinScore
	profile, err := ProfileByName(opts.Profile)
	if err != nil {
		layout.Close()
		return nil, err
	}
	ocr, err := NewOCR(opts.ModelDir, profile, opts.IntraThreads, opts.OCR)
	if err != nil {
		layout.Close()
		return nil, err
	}
	return &Parser{layout: layout, ocr: ocr, opts: opts}, nil
}

// Close releases the ONNX sessions.
func (p *Parser) Close() error {
	if p == nil {
		return nil
	}
	err := p.layout.Close()
	if e := p.ocr.Close(); err == nil {
		err = e
	}
	return err
}

// TableBoxes returns the table regions the layout model finds on the page, in
// image pixels. It runs only layout analysis (no OCR), so it is cheap enough to
// ask of every page of a digital PDF.
func (p *Parser) TableBoxes(img image.Image) ([]image.Rectangle, error) {
	boxes, err := p.layout.Detect(img)
	if err != nil {
		return nil, err
	}
	var tables []image.Rectangle
	for _, b := range dropNested(boxes) {
		if b.Type == TypeTable {
			tables = append(tables, b.Rect)
		}
	}
	return tables, nil
}

// ParsePage returns the Markdown of one page image.
func (p *Parser) ParsePage(ctx context.Context, img image.Image) (string, error) {
	blocks, _, err := p.ParsePageBlocks(ctx, img)
	if err != nil {
		return "", err
	}
	return blocksMarkdown(blocks), nil
}

// ParsePageBlocks parses a page into ordered blocks and reports the time spent per stage.
//
// Layout analysis and text-line detection are independent and run together.
// Each detected line is assigned to the layout block that covers it, lines no
// block covers become extra blocks, and all lines of the page go through the
// recogniser in one pool, so the workers stay busy whatever the block sizes.
func (p *Parser) ParsePageBlocks(ctx context.Context, img image.Image) ([]ParsedBlock, Timings, error) {
	var t Timings
	start := time.Now()
	b := img.Bounds()

	var (
		boxes              []BoundingBox
		pageLines          []image.Rectangle
		layoutErr, lineErr error
		wg                 sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		t0 := time.Now()
		boxes, layoutErr = p.layout.Detect(img)
		t.Layout = time.Since(t0)
	}()
	go func() {
		defer wg.Done()
		t0 := time.Now()
		pageLines, lineErr = p.ocr.DetectLines(img)
		t.Det = time.Since(t0)
	}()
	wg.Wait()
	if layoutErr != nil {
		return nil, t, layoutErr
	}
	if lineErr != nil {
		return nil, t, lineErr
	}
	if err := ctx.Err(); err != nil {
		return nil, t, err
	}
	boxes = dropNested(boxes)

	// Work items: layout blocks with their lines, plus groups of uncovered lines.
	perBlock, uncovered := assignLines(pageLines, boxes, b)
	items := make([]*workItem, 0, len(boxes))
	for i, box := range boxes {
		items = append(items, &workItem{box: box, rects: perBlock[i]})
	}
	switch {
	case !p.opts.DisableRecovery:
		if !p.opts.KeepFurniture {
			// Per line, before grouping: otherwise a running header glues itself
			// to the first paragraph below it.
			body := uncovered[:0:0]
			for _, ln := range uncovered {
				if !isFurniture(ln, b) {
					body = append(body, ln)
				}
			}
			uncovered = body
		}
		for _, grp := range groupLines(uncovered) {
			r := unionRect(grp)
			items = append(items, &workItem{
				box:   BoundingBox{Rect: r, NormCoords: normalize(r.Sub(b.Min), b.Dx(), b.Dy()), Type: TypeText, Confidence: 0.5},
				rects: grp, recovered: true,
			})
		}
	case len(items) == 0:
		// Nothing recognised as a block: read the page as one text block.
		items = append(items, &workItem{
			box:   BoundingBox{Rect: b, NormCoords: [4]int{0, 0, NormGrid, NormGrid}, Type: TypeText, Confidence: 1},
			rects: pageLines,
		})
	}
	t.Blocks = len(items)

	// Heavy recogniser first for the blocks it takes (tables); a failure leaves
	// the block to the OCR path below.
	if p.opts.Deep != nil {
		for _, it := range items {
			box := it.box
			if box.Type != TypeTable && !(box.Type == TypeFigure && p.opts.DeepFigures) {
				continue
			}
			t0 := time.Now()
			tags, err := p.opts.Deep.Recognize(ctx, img, box)
			t.Deep += time.Since(t0)
			if err == nil {
				if md := deepMarkdown(box.Type, tags); strings.TrimSpace(md) != "" {
					it.md, it.source, it.done = md, "deep", true
				}
			}
		}
	}

	// One recognition pool for every line of the page.
	var all []image.Rectangle
	for _, it := range items {
		if it.done || it.box.Type == TypeFigure {
			it.done = true // pictures carry no text worth indexing
			continue
		}
		it.off = len(all)
		all = append(all, it.rects...)
	}
	recognised, err := p.ocr.recognizeAligned(ctx, img, all, p.opts.Workers, &t)
	if err != nil {
		return nil, t, err
	}

	// A text block in which the page-wide detector found no line may hold text
	// too small for it: detect again on the block's own crop, which is upscaled.
	var retry []*workItem
	for _, it := range items {
		if !it.done && !it.recovered && len(it.rects) == 0 {
			retry = append(retry, it)
		}
	}
	retryTimes := make([]Timings, len(retry))
	retryErrs := make([]error, len(retry))
	sem := make(chan struct{}, max(1, p.opts.Workers))
	for i, it := range retry {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, it *workItem) {
			defer wg.Done()
			defer func() { <-sem }()
			it.lines, retryErrs[i] = p.ocr.ReadBlock(ctx, cropImage(img, it.box.Rect), 1, &retryTimes[i])
			it.read = true
		}(i, it)
	}
	wg.Wait()
	for i := range retry {
		if retryErrs[i] != nil {
			return nil, t, retryErrs[i]
		}
		t.Det += retryTimes[i].Det
		t.Rec += retryTimes[i].Rec
	}
	if err := ctx.Err(); err != nil {
		return nil, t, err
	}

	var kept []ParsedBlock
	for _, it := range items {
		res := ParsedBlock{BBox: it.box, Markdown: it.md, Source: it.source}
		if !it.done {
			if !it.read {
				it.lines = keepConfident(recognised[it.off : it.off+len(it.rects)])
			}
			t.Lines += len(it.lines)
			res.Markdown, res.Source = assembleBlock(it.box.Type, it.lines, it.recovered)
		}
		if strings.TrimSpace(res.Markdown) != "" {
			kept = append(kept, res)
		}
	}
	t.Total = time.Since(start)
	return orderBlocks(kept), t, nil
}

// workItem is one region to read: a layout block, or a group of lines that no
// block covered.
type workItem struct {
	box       BoundingBox
	rects     []image.Rectangle // line boxes from the page-wide detector
	recovered bool

	off    int        // where its rects start in the page's recognition pool
	lines  []TextLine // recognised lines
	read   bool       // lines came from ReadBlock instead of the pool
	md     string     // set when the block needs no OCR (deep path)
	source string
	done   bool
}

// keepConfident drops empty and low-confidence lines.
func keepConfident(lines []TextLine) []TextLine {
	kept := make([]TextLine, 0, len(lines))
	for _, ln := range lines {
		if strings.TrimSpace(ln.Text) != "" && ln.Conf >= recMinConf {
			kept = append(kept, ln)
		}
	}
	return kept
}

// assembleBlock turns recognised lines into the Markdown of a block of the given type.
func assembleBlock(kind ElementType, lines []TextLine, recovered bool) (md, source string) {
	source = "ocr"
	if recovered {
		source = "ocr-recovered"
		if looksLikeList(lines) {
			return listText(lines), source
		}
	}
	switch kind {
	case TypeTitle:
		return "## " + paragraphText(lines), source
	case TypeList:
		return listText(lines), source
	case TypeTable:
		if md := gridMarkdown(lines); md != "" {
			return md, "ocr-grid"
		}
		return plainLines(lines), source
	}
	return paragraphText(lines), source
}

// deepMarkdown converts a heavy recogniser's DocTags into Markdown.
func deepMarkdown(kind ElementType, tags string) string {
	if kind == TypeFormula {
		inner := strings.TrimSpace(tags)
		inner = strings.TrimPrefix(inner, "<formula>")
		inner = strings.TrimSuffix(inner, "</formula>")
		if inner = strings.TrimSpace(inner); inner != "" {
			return "$$" + inner + "$$"
		}
		return ""
	}
	return parser.DocTagsToMarkdown(tags)
}

func blocksMarkdown(blocks []ParsedBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		sb.WriteString(strings.TrimSpace(b.Markdown))
		sb.WriteString("\n\n")
	}
	return strings.TrimRight(sb.String(), "\n") + "\n"
}

// dropNested removes duplicates among overlapping blocks so the same text is
// not read twice. Two blocks that cover nearly the same area (the model often
// labels one paragraph both "text" and "list") keep only the more confident one;
// a small block lying inside a larger one is dropped, except inside a figure
// where it may be a caption or label worth keeping.
func dropNested(boxes []BoundingBox) []BoundingBox {
	keep := make([]bool, len(boxes))
	for i := range keep {
		keep[i] = true
	}
	for i := range boxes {
		for j := i + 1; j < len(boxes); j++ {
			if !keep[i] || !keep[j] {
				continue
			}
			a, b := boxes[i], boxes[j]
			inter := a.Rect.Intersect(b.Rect)
			if inter.Empty() {
				continue
			}
			areaA, areaB := a.Rect.Dx()*a.Rect.Dy(), b.Rect.Dx()*b.Rect.Dy()
			small, large := j, i
			if areaA < areaB {
				small, large = i, j
			}
			smallArea, largeArea := min(areaA, areaB), max(areaA, areaB)
			if float64(inter.Dx()*inter.Dy()) < 0.8*float64(smallArea) {
				continue // only partly overlapping: both are real
			}
			switch {
			case float64(smallArea) >= 0.6*float64(largeArea):
				// Near-duplicates: keep the more confident label.
				if boxes[i].Confidence >= boxes[j].Confidence {
					keep[j] = false
				} else {
					keep[i] = false
				}
			case boxes[large].Type != TypeFigure:
				keep[small] = false
			}
		}
	}
	out := boxes[:0:0]
	for i, b := range boxes {
		if keep[i] {
			out = append(out, b)
		}
	}
	return out
}
