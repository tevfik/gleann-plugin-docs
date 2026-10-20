package indra

import (
	"context"
	"image"
	"strings"
	"sync"
)

// Parser converts page images to Markdown with the Indra models. A page is
// read by one call at a time; the ONNX sessions use the thread count given to
// Open.
type Parser struct {
	m             *Model
	mu            sync.Mutex
	KeepFurniture bool
}

// NewParser opens the model directory.
func NewParser(dir string, threads int) (*Parser, error) {
	m, err := Open(dir, threads)
	if err != nil {
		return nil, err
	}
	return &Parser{m: m}, nil
}

// Close releases the ONNX sessions.
func (p *Parser) Close() error { p.m.Close(); return nil }

// page is the assembled result for one page.
type page struct {
	lines  []rline
	layout *Layout
	blocks []pageBlock
}

func (p *Parser) read(img image.Image) (*page, error) {
	g := FromImage(img)
	boxes, err := p.m.Detect(g)
	if err != nil {
		return nil, err
	}
	if len(boxes) == 0 {
		return &page{layout: &Layout{}}, nil
	}
	rec, err := p.m.Recognize(g, boxes)
	if err != nil {
		return nil, err
	}
	raw := make([]rline, len(rec))
	for i, l := range rec {
		raw[i] = rline{box: [4]float64{l.Box.X0, l.Box.Y0, l.Box.X1, l.Box.Y1}, text: l.Text, conf: l.Conf, toks: l.Toks}
	}
	var split []rline
	for i, cuts := range p.m.gutterCuts(raw) {
		split = append(split, p.m.splitLine(raw[i], splitGap, cuts)...)
	}
	lines := p.m.tidyLines(split)
	lay, err := p.m.ReadingOrder(toLines(lines), g.W, g.H)
	if err != nil {
		return nil, err
	}
	pg := &page{lines: lines, layout: lay}
	if len(lines) >= 2 {
		pg.blocks = pageBlocks(lines, lay)
	}
	return pg, nil
}

func toLines(ls []rline) []Line {
	out := make([]Line, len(ls))
	for i, l := range ls {
		out[i] = Line{Box: Box{X0: l.box[0], Y0: l.box[1], X1: l.box[2], Y1: l.box[3]}, Text: l.text, Conf: l.conf}
	}
	return out
}

// ParsePage returns the Markdown of one page image.
func (p *Parser) ParsePage(ctx context.Context, img image.Image) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	pg, err := p.read(img)
	if err != nil {
		return "", err
	}
	if len(pg.lines) < 2 {
		var t []string
		for _, l := range pg.lines {
			t = append(t, l.text)
		}
		return strings.Join(t, "\n"), nil
	}
	var parts []string
	for _, b := range pg.blocks {
		if s := p.m.blockMarkdown(pg.lines, b, p.KeepFurniture); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

// TableBoxes returns the table regions of the page in image pixels.
func (p *Parser) TableBoxes(img image.Image) ([]image.Rectangle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pg, err := p.read(img)
	if err != nil {
		return nil, err
	}
	var out []image.Rectangle
	for _, b := range pg.blocks {
		if b.Class == ClsTable && len(b.Lines) > 1 {
			out = append(out, image.Rect(int(b.Box[0]), int(b.Box[1]), int(b.Box[2]+0.5), int(b.Box[3]+0.5)))
		}
	}
	return out, nil
}
