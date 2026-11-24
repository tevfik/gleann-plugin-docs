package engine

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ExtractPPTX extracts structured text and slides from a .pptx file.
// Slides are emitted in presentation order; titles become headings, bullet
// paragraphs become list items, tables become Markdown tables and speaker
// notes are appended as a quote.
func ExtractPPTX(filePath string) (string, int, error) {
	return extractPPTX(newOutBudget(), filePath)
}

func extractPPTX(ob *outBudget, filePath string) (string, int, error) {
	r, err := zip.OpenReader(filePath)
	if err != nil {
		return "", 0, fmt.Errorf("open pptx zip: %w", err)
	}
	defer r.Close()

	files := map[string]*zip.File{}
	for _, f := range r.File {
		files[filepath.ToSlash(f.Name)] = f
	}

	budget := newZipBudget()
	slidePaths := orderedSlides(budget, files)
	if len(slidePaths) == 0 {
		return "", 0, fmt.Errorf("no slides found in pptx")
	}

	sb := newMDBuilderOn(ob)
	for idx, sp := range slidePaths {
		slideNum := idx + 1
		f := files[sp]
		title, body, err := extractSlideContent(budget, ob, f)
		if errors.Is(err, ErrTooLarge) || errors.Is(err, ErrCorruptInput) {
			return "", 0, err
		}
		if err != nil {
			continue
		}

		notes := ""
		if nf := files[notesPathFor(budget, files, sp)]; nf != nil {
			_, nb, err := extractSlideContent(budget, ob, nf)
			if errors.Is(err, ErrTooLarge) || errors.Is(err, ErrCorruptInput) {
				return "", 0, err
			}
			if err == nil {
				notes = strings.TrimSpace(nb)
			}
		}

		body = strings.TrimSpace(body)
		if title == "" && body == "" && notes == "" {
			continue
		}

		if title != "" {
			fmt.Fprintf(sb, "## Slide %d: %s\n\n", slideNum, title)
		} else {
			fmt.Fprintf(sb, "## Slide %d\n\n", slideNum)
		}
		if body != "" {
			sb.WriteString(body + "\n\n")
		}
		if notes != "" {
			sb.WriteString("> Notes: " + strings.ReplaceAll(notes, "\n\n", "\n> ") + "\n\n")
		}
		if err := sb.Err(); err != nil {
			return "", 0, err
		}
	}

	return strings.TrimSpace(sb.String()), len(slidePaths), nil
}

func parseSlideNumber(name string) int {
	base := filepath.Base(name)
	base = strings.TrimPrefix(base, "slide")
	base = strings.TrimSuffix(base, ".xml")
	n, _ := strconv.Atoi(base)
	return n
}

// readRels parses an OPC .rels part into id -> target.
func readRels(budget *zipBudget, f *zip.File) map[string]string {
	out := map[string]string{}
	if f == nil {
		return out
	}
	rc, err := openEntry(budget, f)
	if err != nil {
		return out
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "Relationship" {
			var id, target string
			for _, a := range se.Attr {
				switch a.Name.Local {
				case "Id":
					id = a.Value
				case "Target":
					target = a.Value
				}
			}
			out[id] = target
		}
	}
	return out
}

func resolvePart(base, target string) string {
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(target, "/")
	}
	return path.Clean(path.Join(path.Dir(base), target))
}

// orderedSlides returns slide parts in presentation order, falling back to
// numeric file-name order when presentation.xml cannot be used.
func orderedSlides(budget *zipBudget, files map[string]*zip.File) []string {
	var out []string
	seen := map[string]bool{}
	if pf := files["ppt/presentation.xml"]; pf != nil {
		rels := readRels(budget, files["ppt/_rels/presentation.xml.rels"])
		if rc, err := openEntry(budget, pf); err == nil {
			dec := xml.NewDecoder(rc)
			for {
				tok, err := dec.Token()
				if err != nil {
					break
				}
				if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sldId" {
					for _, a := range se.Attr {
						if a.Name.Local == "id" && strings.HasPrefix(a.Value, "rId") {
							if t := rels[a.Value]; t != "" {
								p := resolvePart("ppt/presentation.xml", t)
								// A slide referenced twice is still one slide: repeating
								// it would multiply the output for a few bytes of XML.
								if files[p] != nil && !seen[p] {
									seen[p] = true
									out = append(out, p)
								}
							}
						}
					}
				}
			}
			rc.Close()
		}
	}
	if len(out) > 0 {
		return out
	}

	for name := range files {
		if strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml") {
			out = append(out, name)
		}
	}
	sort.Slice(out, func(i, j int) bool { return parseSlideNumber(out[i]) < parseSlideNumber(out[j]) })
	return out
}

func notesPathFor(budget *zipBudget, files map[string]*zip.File, slidePath string) string {
	relPath := path.Join(path.Dir(slidePath), "_rels", path.Base(slidePath)+".rels")
	for _, target := range readRels(budget, files[relPath]) {
		if strings.Contains(target, "notesSlide") {
			return resolvePart(slidePath, target)
		}
	}
	return ""
}

type pptxPara struct {
	text   strings.Builder
	bullet bool
	level  int
}

type pptxTable struct {
	rows [][]string
	cur  []string
	cell []string
}

// extractSlideContent returns the slide title and the Markdown body.
func extractSlideContent(budget *zipBudget, ob *outBudget, f *zip.File) (string, string, error) {
	rc, err := openEntry(budget, f)
	if err != nil {
		return "", "", err
	}
	defer rc.Close()

	decoder := xml.NewDecoder(rc)

	body := newMDBuilderOn(ob)
	defer body.Discard() // the caller gets a copy and charges it where it keeps it
	var title string
	var para *pptxPara
	var tbl *pptxTable
	var inText bool
	var shapeKind string // placeholder type of the current shape
	prevBullet := false

	write := func(s string) {
		if body.Len() > 0 {
			body.WriteString("\n\n")
		}
		body.WriteString(s)
		prevBullet = false
	}

	flushPara := func(p *pptxPara) {
		text := strings.TrimSpace(p.text.String())
		if text == "" {
			return
		}
		if tbl != nil {
			tbl.cell = append(tbl.cell, text)
			return
		}
		switch shapeKind {
		case "sldNum", "ftr", "dt", "hdr", "sldImg":
			return
		case "title", "ctrTitle":
			if title == "" {
				title = text
			} else {
				title += " " + text
			}
			return
		}
		if p.bullet {
			item := strings.Repeat("  ", p.level) + "- " + text
			if prevBullet {
				body.WriteString("\n" + item)
			} else {
				write(item)
			}
			prevBullet = true
			return
		}
		write(text)
	}

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", err
		}

		switch elem := token.(type) {
		case xml.StartElement:
			switch elem.Name.Local {
			case "sp":
				shapeKind = ""
			case "ph":
				for _, a := range elem.Attr {
					if a.Name.Local == "type" {
						shapeKind = a.Value
					}
				}
			case "tbl":
				tbl = &pptxTable{}
			case "tr":
				if tbl != nil {
					tbl.cur = nil
				}
			case "tc":
				if tbl != nil {
					tbl.cell = nil
				}
			case "p":
				para = &pptxPara{}
			case "pPr":
				if para != nil {
					for _, a := range elem.Attr {
						if a.Name.Local == "lvl" {
							n, _ := strconv.Atoi(a.Value)
							para.level = clampListLevel(n)
						}
					}
				}
			case "buChar", "buAutoNum":
				if para != nil {
					para.bullet = true
				}
			case "br":
				if para != nil {
					para.text.WriteString(" ")
				}
			case "t":
				inText = true
			}
		case xml.EndElement:
			switch elem.Name.Local {
			case "t":
				inText = false
			case "p":
				if para != nil {
					flushPara(para)
					para = nil
				}
			case "sp":
				shapeKind = ""
			case "tc":
				if tbl != nil {
					cell := strings.Join(tbl.cell, "<br>")
					cell = strings.ReplaceAll(cell, "|", "\\|")
					if len(tbl.cur) >= maxTableCols {
						return "", "", fmt.Errorf("%w: a table row has more than %d cells", ErrCorruptInput, maxTableCols)
					}
					tbl.cur = append(tbl.cur, cell)
				}
			case "tr":
				if tbl != nil && len(tbl.cur) > 0 {
					tbl.rows = append(tbl.rows, tbl.cur)
				}
			case "tbl":
				if tbl != nil {
					tb := newMDBuilderOn(ob)
					if err := writeTable(tb, tbl.rows, remainingBudget(tb), tableOpts{}); err != nil {
						tb.Discard()
						return "", "", err
					}
					if tb.Len() > 0 {
						md := tb.String()
						tb.Discard()
						write(md)
					}
					tbl = nil
				}
			}
		case xml.CharData:
			if inText && para != nil {
				para.text.Write(elem)
			}
		}
	}

	if err := body.Err(); err != nil {
		return "", "", err
	}
	return title, body.String(), nil
}
