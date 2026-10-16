package docparser

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	ort "github.com/yalue/onnxruntime_go"
)

// PP-OCRv4 parameters (RapidOCR defaults).
const (
	detMaxSide   = 960  // longest side after resizing
	detThresh    = 0.3  // probability map binarisation
	detBoxThresh = 0.5  // minimum mean probability of a box
	detUnclip    = 1.6  // box growth factor (DB "unclip ratio")
	detMinSide   = 3    // boxes thinner than this are noise
	detPad       = 10   // white margin added around a block before detection
	recHeight    = 48   // line crops are scaled to this height
	recMaxWidth  = 3200 // width cap for very long lines
	recMinConf   = 0.3  // lines recognised with less confidence are dropped
)

var (
	detNorm = NormParams{
		Mean: [3]float32{0.485, 0.456, 0.406},
		Std:  [3]float32{0.229, 0.224, 0.225},
		BGR:  true,
	}
	recNorm = NormParams{
		Mean: [3]float32{0.5, 0.5, 0.5},
		Std:  [3]float32{0.5, 0.5, 0.5},
		BGR:  true,
	}
)

// TextLine is one recognised line of text.
type TextLine struct {
	Rect image.Rectangle // in the coordinates of the image passed to ReadBlock
	Text string
	Conf float32
}

// OCROptions tunes detection. The zero value of a field selects its default.
type OCROptions struct {
	Thresh    float32 // probability map binarisation (default 0.3)
	BoxThresh float32 // minimum mean probability of a box (default 0.5)
	Unclip    float64 // box growth factor (default 1.6)
	MaxSide   int     // longest side the detector sees (default 960)
}

func (o OCROptions) withDefaults() OCROptions {
	if o.Thresh <= 0 {
		o.Thresh = detThresh
	}
	if o.BoxThresh <= 0 {
		o.BoxThresh = detBoxThresh
	}
	if o.Unclip <= 0 {
		o.Unclip = detUnclip
	}
	if o.MaxSide <= 0 {
		o.MaxSide = detMaxSide
	}
	return o
}

// OCR reads text with a DB detector and a CTC recogniser.
type OCR struct {
	det  *ort.DynamicAdvancedSession
	rec  *ort.DynamicAdvancedSession
	dict []string // CTC classes: [0] blank, then the dictionary, then a space
	opts OCROptions
}

// NewOCR loads the detector and recogniser of profile from dir ("" = ModelDir()).
func NewOCR(dir string, profile OCRProfile, intraThreads int, opts ...OCROptions) (*OCR, error) {
	detData, err := loadModel(dir, profile.Det)
	if err != nil {
		return nil, err
	}
	recData, err := loadModel(dir, profile.Rec)
	if err != nil {
		return nil, err
	}
	det, err := newSingleIOSession(detData, intraThreads)
	if err != nil {
		return nil, fmt.Errorf("detection model: %w", err)
	}
	rec, err := newSingleIOSession(recData, intraThreads)
	if err != nil {
		det.Destroy()
		return nil, fmt.Errorf("recognition model: %w", err)
	}

	var chars string
	if profile.Dict != nil {
		raw, err := loadModel(dir, *profile.Dict)
		if err != nil {
			det.Destroy()
			rec.Destroy()
			return nil, err
		}
		chars = strings.TrimSuffix(string(raw), "\n")
	} else if chars, err = recDictionary(rec); err != nil {
		det.Destroy()
		rec.Destroy()
		return nil, err
	}

	var o OCROptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return &OCR{det: det, rec: rec, dict: buildDictionary(chars), opts: o.withDefaults()}, nil
}

// Close releases both sessions.
func (o *OCR) Close() error {
	if o == nil {
		return nil
	}
	var first error
	for _, s := range []*ort.DynamicAdvancedSession{o.det, o.rec} {
		if s != nil {
			if err := s.Destroy(); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}

// recDictionary reads the character list stored in the recogniser's metadata
// and wraps it with the CTC blank (index 0) and the trailing space class.
func recDictionary(rec *ort.DynamicAdvancedSession) (string, error) {
	md, err := rec.GetModelMetadata()
	if err != nil {
		return "", fmt.Errorf("reading recogniser metadata: %w", err)
	}
	defer md.Destroy()
	chars, ok, err := md.LookupCustomMetadataMap("character")
	if err != nil || !ok {
		return "", fmt.Errorf("recogniser has no character dictionary in its metadata")
	}
	return chars, nil
}

func buildDictionary(metadata string) []string {
	list := strings.Split(metadata, "\n")
	dict := make([]string, 0, len(list)+2)
	dict = append(dict, "") // CTC blank
	dict = append(dict, list...)
	dict = append(dict, " ")
	return dict
}

// DetectLines finds text line boxes in img, in img's own coordinates, in
// reading order (top to bottom, left to right within a line).
func (o *OCR) DetectLines(img image.Image) ([]image.Rectangle, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 8 || h < 8 {
		return nil, nil
	}

	scale := 1.0
	if m := max(w, h); m > o.opts.MaxSide {
		scale = float64(o.opts.MaxSide) / float64(m)
	}
	rw := roundUp32(int(math.Round(float64(w) * scale)))
	rh := roundUp32(int(math.Round(float64(h) * scale)))

	in, err := ort.NewTensor(ort.NewShape(1, 3, int64(rh), int64(rw)), ResizeToCHW(img, rw, rh, detNorm))
	if err != nil {
		return nil, err
	}
	defer in.Destroy()
	outs := []ort.Value{nil}
	if err := o.det.Run([]ort.Value{in}, outs); err != nil {
		return nil, fmt.Errorf("text detection: %w", err)
	}
	defer outs[0].Destroy()
	prob, ok := outs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("text detector returned an unexpected output type")
	}
	shape := prob.GetShape()
	if len(shape) != 4 {
		return nil, fmt.Errorf("text detector output has shape %v", shape)
	}

	boxes := dbBoxes(prob.GetData(), int(shape[3]), int(shape[2]), o.opts)
	sx, sy := float64(w)/float64(rw), float64(h)/float64(rh)
	rects := make([]image.Rectangle, 0, len(boxes))
	for _, r := range boxes {
		out := image.Rect(
			b.Min.X+int(math.Floor(float64(r.Min.X)*sx)), b.Min.Y+int(math.Floor(float64(r.Min.Y)*sy)),
			b.Min.X+int(math.Ceil(float64(r.Max.X)*sx)), b.Min.Y+int(math.Ceil(float64(r.Max.Y)*sy)),
		).Intersect(b)
		if out.Dx() >= detMinSide && out.Dy() >= detMinSide {
			rects = append(rects, out)
		}
	}
	return sortReadingOrder(rects), nil
}

func roundUp32(v int) int {
	if v < 32 {
		return 32
	}
	return (v + 31) / 32 * 32
}

// dbBoxes post-processes a DB probability map (w x h): threshold, group
// 8-connected pixels, score each group, and grow the surviving boxes
// ("unclip") because the network predicts a shrunken text kernel.
func dbBoxes(prob []float32, w, h int, opt OCROptions) []image.Rectangle {
	if len(prob) < w*h {
		return nil
	}
	seen := make([]bool, w*h)
	var out []image.Rectangle
	var stack []int

	for start := 0; start < w*h; start++ {
		if seen[start] || prob[start] <= opt.Thresh {
			continue
		}
		minX, minY, maxX, maxY := w, h, -1, -1
		stack = append(stack[:0], start)
		seen[start] = true
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			x, y := p%w, p/w
			minX, maxX = min(minX, x), max(maxX, x)
			minY, maxY = min(minY, y), max(maxY, y)
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					nx, ny := x+dx, y+dy
					if nx < 0 || ny < 0 || nx >= w || ny >= h {
						continue
					}
					q := ny*w + nx
					if !seen[q] && prob[q] > opt.Thresh {
						seen[q] = true
						stack = append(stack, q)
					}
				}
			}
		}

		bw, bh := maxX-minX+1, maxY-minY+1
		if min(bw, bh) < detMinSide {
			continue
		}
		var sum float64
		for y := minY; y <= maxY; y++ {
			for x := minX; x <= maxX; x++ {
				sum += float64(prob[y*w+x])
			}
		}
		if float32(sum/float64(bw*bh)) < opt.BoxThresh {
			continue
		}

		// Unclip: grow every side by area*ratio/perimeter.
		d := float64(bw*bh) * opt.Unclip / float64(2*(bw+bh))
		r := image.Rect(
			int(math.Floor(float64(minX)-d)), int(math.Floor(float64(minY)-d)),
			int(math.Ceil(float64(maxX+1)+d)), int(math.Ceil(float64(maxY+1)+d)),
		).Intersect(image.Rect(0, 0, w, h))
		if !r.Empty() {
			out = append(out, r)
		}
	}
	return out
}

// sortReadingOrder orders boxes into lines (centres within half a box height
// of each other) and then left to right inside each line.
func sortReadingOrder(rects []image.Rectangle) []image.Rectangle {
	sort.SliceStable(rects, func(i, j int) bool {
		ci, cj := rects[i].Min.Y+rects[i].Dy()/2, rects[j].Min.Y+rects[j].Dy()/2
		return ci < cj
	})
	var lines [][]image.Rectangle
	for _, r := range rects {
		placed := false
		if n := len(lines); n > 0 {
			ref := lines[n-1][0]
			cy := r.Min.Y + r.Dy()/2
			if abs(cy-(ref.Min.Y+ref.Dy()/2)) <= max(ref.Dy(), r.Dy())/2 {
				lines[n-1] = append(lines[n-1], r)
				placed = true
			}
		}
		if !placed {
			lines = append(lines, []image.Rectangle{r})
		}
	}
	out := make([]image.Rectangle, 0, len(rects))
	for _, ln := range lines {
		sort.SliceStable(ln, func(i, j int) bool { return ln[i].Min.X < ln[j].Min.X })
		out = append(out, ln...)
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// recBatch is how many line crops go through the recogniser in one call.
// Batching made it slower: on a page of 124 lines it took 2.0 s at 1, 2.6 s at
// 2, 3.2 s at 4 and 3.4 s at 8, with lines sorted by width. The recogniser's
// attention layers make the right-padding expensive, so lines run one by one.
const recBatch = 1

// recWidth is the width a line crop is scaled to (height recHeight).
func recWidth(b image.Rectangle) int {
	w := int(math.Ceil(float64(recHeight) * float64(b.Dx()) / float64(b.Dy())))
	return min(max(w, 16), recMaxWidth)
}

// Recognize reads one line crop and returns its text and mean confidence.
func (o *OCR) Recognize(line image.Image) (string, float32, error) {
	b := line.Bounds()
	if b.Dx() < 2 || b.Dy() < 2 {
		return "", 0, nil
	}
	texts, confs, err := o.recognizeBatch([]image.Image{line})
	if err != nil {
		return "", 0, err
	}
	return texts[0], confs[0], nil
}

// recognizeBatch reads several line crops in one model call. Each crop is
// scaled to height recHeight and the batch is right-padded with zeros (the
// normalised mean) to the widest one, as PaddleOCR does.
func (o *OCR) recognizeBatch(crops []image.Image) ([]string, []float32, error) {
	widths := make([]int, len(crops))
	maxW := 0
	for i, c := range crops {
		widths[i] = recWidth(c.Bounds())
		maxW = max(maxW, widths[i])
	}
	plane := recHeight * maxW
	data := make([]float32, len(crops)*3*plane)
	for i, c := range crops {
		chw := ResizeToCHW(c, widths[i], recHeight, recNorm) // 3 x recHeight x widths[i]
		for ch := 0; ch < 3; ch++ {
			for y := 0; y < recHeight; y++ {
				copy(data[i*3*plane+ch*plane+y*maxW:], chw[ch*recHeight*widths[i]+y*widths[i]:ch*recHeight*widths[i]+(y+1)*widths[i]])
			}
		}
	}

	in, err := ort.NewTensor(ort.NewShape(int64(len(crops)), 3, recHeight, int64(maxW)), data)
	if err != nil {
		return nil, nil, err
	}
	defer in.Destroy()
	outs := []ort.Value{nil}
	if err := o.rec.Run([]ort.Value{in}, outs); err != nil {
		return nil, nil, fmt.Errorf("text recognition: %w", err)
	}
	defer outs[0].Destroy()
	logits, ok := outs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, nil, fmt.Errorf("recogniser returned an unexpected output type")
	}
	shape := logits.GetShape()
	if len(shape) != 3 || int(shape[0]) != len(crops) {
		return nil, nil, fmt.Errorf("recogniser output has shape %v for %d lines", shape, len(crops))
	}
	steps, classes := int(shape[1]), int(shape[2])
	all := logits.GetData()
	texts := make([]string, len(crops))
	confs := make([]float32, len(crops))
	for i := range crops {
		texts[i], confs[i] = ctcGreedy(all[i*steps*classes:(i+1)*steps*classes], steps, classes, o.dict)
	}
	return texts, confs, nil
}

// ctcGreedy decodes T x C class probabilities: best class per step, repeats
// collapsed, blanks (class 0) removed. It returns the text and the mean
// probability of the emitted characters.
func ctcGreedy(probs []float32, steps, classes int, dict []string) (string, float32) {
	var sb strings.Builder
	var sum float32
	var n int
	prev := -1
	for t := 0; t < steps; t++ {
		row := probs[t*classes : (t+1)*classes]
		best := 0
		for c := 1; c < classes; c++ {
			if row[c] > row[best] {
				best = c
			}
		}
		if best != 0 && best != prev && best < len(dict) {
			sb.WriteString(dict[best])
			sum += row[best]
			n++
		}
		prev = best
	}
	if n == 0 {
		return "", 0
	}
	return sb.String(), sum / float32(n)
}

// ReadBlock detects and reads every text line of img. Lines are recognised by
// `workers` goroutines. It adds the time spent to t.
func (o *OCR) ReadBlock(ctx context.Context, img image.Image, workers int, t *Timings) ([]TextLine, error) {
	b := img.Bounds()
	padded := padWhite(img, detPad)

	t0 := time.Now()
	rects, err := o.DetectLines(padded)
	if t != nil {
		t.Det += time.Since(t0)
	}
	if err != nil {
		return nil, err
	}
	lines, err := o.RecognizeLines(ctx, padded, rects, workers, t)
	if err != nil {
		return nil, err
	}
	for i := range lines {
		lines[i].Rect = lines[i].Rect.Sub(image.Pt(detPad, detPad)).Add(b.Min)
	}
	return lines, nil
}

// recognizeAligned reads the given line boxes of img (in img's coordinates) and
// returns one TextLine per box, in the order of rects. Boxes are sorted by
// width, cut into batches of recBatch and the batches run on `workers`
// goroutines. Nothing is filtered; it adds the recognition time to t.
func (o *OCR) recognizeAligned(ctx context.Context, img image.Image, rects []image.Rectangle, workers int, t *Timings) ([]TextLine, error) {
	if len(rects) == 0 {
		return nil, nil
	}
	if workers < 1 {
		workers = 1
	}

	order := make([]int, len(rects))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return recWidth(rects[order[a]]) < recWidth(rects[order[b]]) })

	lines := make([]TextLine, len(rects))
	var batches [][]int
	for i := 0; i < len(order); i += recBatch {
		batches = append(batches, order[i:min(i+recBatch, len(order))])
	}
	errs := make([]error, len(batches))
	recTime := make([]time.Duration, len(batches))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for bi, batch := range batches {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(bi int, batch []int) {
			defer wg.Done()
			defer func() { <-sem }()
			crops := make([]image.Image, len(batch))
			for k, idx := range batch {
				crops[k] = cropImage(img, rects[idx])
			}
			start := time.Now()
			texts, confs, err := o.recognizeBatch(crops)
			recTime[bi], errs[bi] = time.Since(start), err
			if err != nil {
				return
			}
			for k, idx := range batch {
				lines[idx] = TextLine{Rect: rects[idx], Text: texts[k], Conf: confs[k]}
			}
		}(bi, batch)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for bi := range batches {
		if t != nil {
			t.Rec += recTime[bi]
		}
		if errs[bi] != nil {
			return nil, errs[bi]
		}
	}
	return lines, nil
}

// RecognizeLines reads the given line boxes of img (in img's coordinates) and
// drops empty and low-confidence results.
func (o *OCR) RecognizeLines(ctx context.Context, img image.Image, rects []image.Rectangle, workers int, t *Timings) ([]TextLine, error) {
	lines, err := o.recognizeAligned(ctx, img, rects, workers, t)
	if err != nil {
		return nil, err
	}
	kept := keepConfident(lines)
	if t != nil {
		t.Lines += len(kept)
	}
	return kept, nil
}

// padWhite returns img surrounded by a white margin of pad pixels.
func padWhite(img image.Image, pad int) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx()+2*pad, b.Dy()+2*pad))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, image.Rect(pad, pad, pad+b.Dx(), pad+b.Dy()), img, b.Min, draw.Src)
	return dst
}
