package docparser

import (
	"fmt"
	"image"
	"math"
	"sort"

	ort "github.com/yalue/onnxruntime_go"
)

// PicoDet (PP-Structure, PubLayNet) geometry. The model takes a fixed
// 800x608 (H x W) image and predicts on four feature maps.
const (
	layoutInH   = 800
	layoutInW   = 608
	layoutBins  = 8 // distribution bins per box side (reg_max + 1)
	layoutClsN  = 5
	layoutScore = 0.4
	layoutIoU   = 0.5
	layoutMax   = 100
)

var (
	layoutStrides = [4]int{8, 16, 32, 64}
	layoutClasses = [layoutClsN]ElementType{TypeText, TypeTitle, TypeList, TypeTable, TypeFigure}

	layoutNorm = NormParams{
		Mean: [3]float32{0.485, 0.456, 0.406},
		Std:  [3]float32{0.229, 0.224, 0.225},
	}

	layoutOutputs = []string{
		"transpose_0.tmp_0", "transpose_2.tmp_0", "transpose_4.tmp_0", "transpose_6.tmp_0", // class scores
		"transpose_1.tmp_0", "transpose_3.tmp_0", "transpose_5.tmp_0", "transpose_7.tmp_0", // box distributions
	}
)

// LayoutDetector finds text, title, list, table and figure blocks on a page.
type LayoutDetector struct {
	sess interface {
		Run(inputs, outputs []ort.Value) error
		Destroy() error
	}
	// MinScore overrides the default confidence threshold when > 0.
	MinScore float32
}

// NewLayoutDetector loads the layout model from dir ("" = ModelDir()).
func NewLayoutDetector(dir string, intraThreads int) (*LayoutDetector, error) {
	data, err := loadModel(dir, LayoutModel)
	if err != nil {
		return nil, err
	}
	sess, err := newSession(data, []string{"image"}, layoutOutputs, intraThreads)
	if err != nil {
		return nil, fmt.Errorf("layout model: %w", err)
	}
	return &LayoutDetector{sess: sess}, nil
}

// Close releases the session.
func (d *LayoutDetector) Close() error {
	if d == nil || d.sess == nil {
		return nil
	}
	return d.sess.Destroy()
}

// Detect returns the layout blocks of img in page pixels, highest confidence first.
func (d *LayoutDetector) Detect(img image.Image) ([]BoundingBox, error) {
	b := img.Bounds()
	pw, ph := b.Dx(), b.Dy()
	if pw < 8 || ph < 8 {
		return nil, nil
	}

	in, err := ort.NewTensor(ort.NewShape(1, 3, layoutInH, layoutInW),
		ResizeToCHW(img, layoutInW, layoutInH, layoutNorm))
	if err != nil {
		return nil, err
	}
	defer in.Destroy()

	outs := make([]ort.Value, len(layoutOutputs))
	if err := d.sess.Run([]ort.Value{in}, outs); err != nil {
		return nil, fmt.Errorf("layout inference: %w", err)
	}
	defer func() {
		for _, o := range outs {
			if o != nil {
				o.Destroy()
			}
		}
	}()

	var levelScores, levelRegs [4][]float32
	for i := 0; i < 4; i++ {
		s, ok1 := outs[i].(*ort.Tensor[float32])
		r, ok2 := outs[4+i].(*ort.Tensor[float32])
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("layout model returned unexpected output types")
		}
		levelScores[i], levelRegs[i] = s.GetData(), r.GetData()
	}

	thr := float32(layoutScore)
	if d.MinScore > 0 {
		thr = d.MinScore
	}
	cands := decodePicoDet(levelScores, levelRegs, thr)
	cands = nms(cands, layoutIoU)
	if len(cands) > layoutMax {
		cands = cands[:layoutMax]
	}

	sx, sy := float64(pw)/layoutInW, float64(ph)/layoutInH
	boxes := make([]BoundingBox, 0, len(cands))
	for _, c := range cands {
		r := image.Rect(
			b.Min.X+int(math.Floor(float64(c.x1)*sx)), b.Min.Y+int(math.Floor(float64(c.y1)*sy)),
			b.Min.X+int(math.Ceil(float64(c.x2)*sx)), b.Min.Y+int(math.Ceil(float64(c.y2)*sy)),
		).Intersect(b)
		if r.Dx() < 4 || r.Dy() < 4 {
			continue
		}
		boxes = append(boxes, BoundingBox{
			Rect:       r,
			NormCoords: normalize(r.Sub(b.Min), pw, ph),
			Type:       layoutClasses[c.cls],
			Confidence: c.score,
		})
	}
	return boxes, nil
}

// cand is a decoded detection in model input pixels.
type cand struct {
	x1, y1, x2, y2 float32
	score          float32
	cls            int
}

// decodePicoDet turns the four per-stride (scores, box distributions) pairs
// into candidate boxes above thr. Scores are expected to be probabilities
// already; each box side is the expectation of a softmax over layoutBins bins,
// in units of the stride, measured from the cell centre (Generalized Focal Loss).
func decodePicoDet(scores, regs [4][]float32, thr float32) []cand {
	var out []cand
	for lvl, stride := range layoutStrides {
		fw := (layoutInW + stride - 1) / stride
		n := len(scores[lvl]) / layoutClsN
		if len(regs[lvl]) < n*4*layoutBins {
			continue
		}
		for idx := 0; idx < n; idx++ {
			best, bi := float32(0), -1
			for c := 0; c < layoutClsN; c++ {
				if s := scores[lvl][idx*layoutClsN+c]; s > best {
					best, bi = s, c
				}
			}
			if bi < 0 || best < thr {
				continue
			}
			cx := (float32(idx%fw) + 0.5) * float32(stride)
			cy := (float32(idx/fw) + 0.5) * float32(stride)
			var d [4]float32
			reg := regs[lvl][idx*4*layoutBins:]
			for side := 0; side < 4; side++ {
				d[side] = softmaxExpect(reg[side*layoutBins:(side+1)*layoutBins]) * float32(stride)
			}
			out = append(out, cand{
				x1: cx - d[0], y1: cy - d[1], x2: cx + d[2], y2: cy + d[3],
				score: best, cls: bi,
			})
		}
	}
	return out
}

// softmaxExpect returns sum(i * softmax(v)[i]).
func softmaxExpect(v []float32) float32 {
	m := v[0]
	for _, x := range v[1:] {
		if x > m {
			m = x
		}
	}
	var sum, acc float64
	for i, x := range v {
		e := math.Exp(float64(x - m))
		sum += e
		acc += float64(i) * e
	}
	return float32(acc / sum)
}

// nms keeps the best-scoring box among overlapping ones of the same class.
func nms(c []cand, iouThr float32) []cand {
	sort.Slice(c, func(i, j int) bool { return c[i].score > c[j].score })
	var keep []cand
	for _, a := range c {
		ok := true
		for _, k := range keep {
			if k.cls == a.cls && iou(a, k) > iouThr {
				ok = false
				break
			}
		}
		if ok {
			keep = append(keep, a)
		}
	}
	return keep
}

func iou(a, b cand) float32 {
	ix1, iy1 := max32(a.x1, b.x1), max32(a.y1, b.y1)
	ix2, iy2 := min32(a.x2, b.x2), min32(a.y2, b.y2)
	iw, ih := ix2-ix1, iy2-iy1
	if iw <= 0 || ih <= 0 {
		return 0
	}
	inter := iw * ih
	union := (a.x2-a.x1)*(a.y2-a.y1) + (b.x2-b.x1)*(b.y2-b.y1) - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}
