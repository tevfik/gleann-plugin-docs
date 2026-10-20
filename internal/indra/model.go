// Package indra runs Datalab's Indra OCR models (ONNX Runtime) from Go:
// line detection, CTC line recognition and reading order. It is a port of the
// indra-ocr Python package's inference path (Apache-2.0 code, models under
// their own license, see the model card).
package indra

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/tevfik/gleann-plugin-docs/internal/docparser"
)

type meta struct {
	DetLongSide int    `json:"det_long_side"`
	RecHeight   int    `json:"rec_height"`
	RecWindow   int    `json:"rec_window"`
	Vocab       string `json:"vocab"`
}

type vocabJSON struct {
	Blank int      `json:"blank"`
	Chars []string `json:"chars"`
}

// Model holds the sessions of one Indra model directory.
type Model struct {
	det, rec, order *ort.DynamicAdvancedSession
	chars           []string
	longSide        int
	window          int
	hasCommands     bool // the vocabulary holds LaTeX command tokens (\\Delta)
}

// Open loads det.onnx, rec.onnx, order.onnx and meta.json from dir.
func Open(dir string, threads int) (*Model, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, err
	}
	var mt meta
	if err := json.Unmarshal(raw, &mt); err != nil {
		return nil, fmt.Errorf("indra meta.json: %w", err)
	}
	var v vocabJSON
	if err := json.Unmarshal([]byte(mt.Vocab), &v); err != nil {
		return nil, fmt.Errorf("indra vocab: %w", err)
	}
	if v.Blank != 0 {
		return nil, fmt.Errorf("indra vocab: only blank=0 is supported")
	}
	if mt.RecHeight != lineHeight {
		return nil, fmt.Errorf("indra: rec_height %d, expected %d", mt.RecHeight, lineHeight)
	}
	m := &Model{chars: v.Chars, longSide: mt.DetLongSide, window: max(1, mt.RecWindow)}
	for _, c := range v.Chars {
		if utf8.RuneCountInString(c) > 1 {
			m.hasCommands = true
		}
	}
	load := func(name string, in, out []string) (*ort.DynamicAdvancedSession, error) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		return docparser.NewSession(b, in, out, threads)
	}
	if m.det, err = load("det.onnx", []string{"x"}, []string{"out"}); err != nil {
		return nil, err
	}
	if m.rec, err = load("rec.onnx", []string{"x", "widths"}, []string{"ids", "conf", "lengths"}); err != nil {
		m.Close()
		return nil, err
	}
	if m.order, err = load("order.onnx", []string{"feats", "mask"}, []string{"a", "cls", "same"}); err != nil {
		m.Close()
		return nil, err
	}
	return m, nil
}

// Close releases the sessions.
func (m *Model) Close() {
	if m == nil {
		return
	}
	for _, s := range []*ort.DynamicAdvancedSession{m.det, m.rec, m.order} {
		if s != nil {
			s.Destroy()
		}
	}
}

func (m *Model) decode(ids []int64) string {
	toks := make([]string, 0, len(ids))
	for _, i := range ids {
		if i > 0 && int(i) <= len(m.chars) {
			toks = append(toks, m.chars[i-1])
		}
	}
	if !m.hasCommands {
		return strings.Join(toks, "")
	}
	var sb strings.Builder
	for k, t := range toks {
		sb.WriteString(t)
		// "\\alpha" + "x" must not read back as "\\alphax".
		if len(t) > 2 && t[0] == '\\' && isAlphaOnly(t[1:]) && k+1 < len(toks) {
			if r, _ := utf8.DecodeRuneInString(toks[k+1]); unicode.IsLetter(r) {
				sb.WriteByte(' ')
			}
		}
	}
	return sb.String()
}

func isAlphaOnly(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return !unicode.IsLetter(r) }) < 0
}
