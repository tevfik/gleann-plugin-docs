package engine

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// ExtractDocx reads word/document.xml from a .docx file and produces Markdown.
func ExtractDocx(filePath string) (string, error) {
	r, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("open docx zip: %w", err)
	}
	defer r.Close()

	var docFile, stylesFile *zip.File
	for _, f := range r.File {
		switch f.Name {
		case "word/document.xml":
			docFile = f
		case "word/styles.xml":
			stylesFile = f
		}
	}
	if docFile == nil {
		return "", fmt.Errorf("word/document.xml not found in %s", filePath)
	}

	var styles map[string]int
	if stylesFile != nil {
		if rc, err := stylesFile.Open(); err == nil {
			styles = parseHeadingStyles(rc)
			rc.Close()
		}
	}

	rc, err := docFile.Open()
	if err != nil {
		return "", fmt.Errorf("open word/document.xml: %w", err)
	}
	defer rc.Close()

	return parseDocumentXMLWithStyles(rc, styles)
}

func parseDocumentXML(r io.Reader) (string, error) {
	return parseDocumentXMLWithStyles(r, nil)
}

var (
	headingNameRe = regexp.MustCompile(`^(?:heading|başlık|baslik)\s*(\d)$`)
	// Localized style ids look like "Heading1" / "Balk1" (Turkish "Başlık 1").
	headingIDRe = regexp.MustCompile(`(?i)^(?:heading|balk|başlık|baslik)(\d)$`)
)

// headingLevelFromName maps a style name to a Markdown heading level. Word
// stores localized style ids ("Balk1") but the style *name* in styles.xml stays
// in the canonical form ("heading 1").
func headingLevelFromName(name string) int {
	n := strings.ToLower(strings.TrimSpace(name))
	if m := headingNameRe.FindStringSubmatch(n); m != nil {
		if lvl, _ := strconv.Atoi(m[1]); lvl >= 1 && lvl <= 6 {
			return lvl
		}
	}
	if n == "title" {
		return 1
	}
	return 0
}

// parseHeadingStyles returns styleId -> heading level (1..6) from styles.xml.
func parseHeadingStyles(r io.Reader) map[string]int {
	out := map[string]int{}
	dec := xml.NewDecoder(r)
	var curID string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch e := tok.(type) {
		case xml.StartElement:
			switch e.Name.Local {
			case "style":
				curID = ""
				for _, a := range e.Attr {
					if a.Name.Local == "styleId" {
						curID = a.Value
					}
				}
			case "name":
				if curID == "" {
					continue
				}
				for _, a := range e.Attr {
					if a.Name.Local == "val" {
						if lvl := headingLevelFromName(a.Value); lvl > 0 {
							out[curID] = lvl
						}
					}
				}
			}
		case xml.EndElement:
			if e.Name.Local == "style" {
				curID = ""
			}
		}
	}
	return out
}

type docxPara struct {
	text    strings.Builder
	heading int
	list    bool
	listLvl int
}

type docxTable struct {
	rows    [][]string
	cur     []string
	cell    []string
	depth   int
	inCell  bool
	maxCols int
}

func parseDocumentXMLWithStyles(r io.Reader, styles map[string]int) (string, error) {
	decoder := xml.NewDecoder(r)

	var sb strings.Builder
	var paras []*docxPara
	var tbl *docxTable // outermost table; nested tables are flattened into the cell
	var inT bool
	prevWasList := false

	writeBlock := func(s string) {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(s)
		prevWasList = false
	}

	emitPara := func(p *docxPara) {
		text := strings.TrimSpace(p.text.String())
		if text == "" {
			return
		}
		if tbl != nil && tbl.inCell {
			tbl.cell = append(tbl.cell, text)
			return
		}
		switch {
		case p.heading > 0:
			writeBlock(strings.Repeat("#", p.heading) + " " + text)
		case p.list:
			item := strings.Repeat("  ", p.listLvl) + "- " + text
			if prevWasList {
				sb.WriteString("\n" + item)
			} else {
				writeBlock(item)
			}
			prevWasList = true
		default:
			writeBlock(text)
		}
	}

	flushTable := func(t *docxTable) {
		if len(t.rows) == 0 {
			return
		}
		var tb strings.Builder
		for i, row := range t.rows {
			for len(row) < t.maxCols {
				row = append(row, "")
			}
			tb.WriteString("| " + strings.Join(row, " | ") + " |\n")
			if i == 0 {
				tb.WriteString("|" + strings.Repeat(" --- |", t.maxCols) + "\n")
			}
		}
		writeBlock(strings.TrimRight(tb.String(), "\n"))
	}

	cur := func() *docxPara {
		if len(paras) == 0 {
			return nil
		}
		return paras[len(paras)-1]
	}

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		switch elem := token.(type) {
		case xml.StartElement:
			switch elem.Name.Local {
			case "tbl":
				if tbl == nil {
					tbl = &docxTable{depth: 1}
				} else {
					tbl.depth++
				}
			case "tr":
				if tbl != nil && tbl.depth == 1 {
					tbl.cur = nil
				}
			case "tc":
				if tbl != nil && tbl.depth == 1 {
					tbl.inCell = true
					tbl.cell = nil
				}
			case "p":
				paras = append(paras, &docxPara{})
			case "pStyle":
				if p := cur(); p != nil {
					for _, a := range elem.Attr {
						if a.Name.Local != "val" {
							continue
						}
						if lvl, ok := styles[a.Value]; ok {
							p.heading = lvl
						} else if lvl := headingLevelFromName(a.Value); lvl > 0 {
							p.heading = lvl
						} else if m := headingIDRe.FindStringSubmatch(a.Value); m != nil {
							p.heading, _ = strconv.Atoi(m[1])
						}
					}
				}
			case "outlineLvl":
				if p := cur(); p != nil && p.heading == 0 {
					for _, a := range elem.Attr {
						if a.Name.Local == "val" {
							if n, err := strconv.Atoi(a.Value); err == nil && n >= 0 && n < 6 {
								p.heading = n + 1
							}
						}
					}
				}
			case "numPr":
				if p := cur(); p != nil {
					p.list = true
				}
			case "ilvl":
				if p := cur(); p != nil {
					for _, a := range elem.Attr {
						if a.Name.Local == "val" {
							p.listLvl, _ = strconv.Atoi(a.Value)
						}
					}
				}
			case "t":
				inT = true
			case "tab", "br", "cr":
				if p := cur(); p != nil {
					p.text.WriteString(" ")
				}
			}

		case xml.EndElement:
			switch elem.Name.Local {
			case "t":
				inT = false
			case "p":
				if len(paras) > 0 {
					p := paras[len(paras)-1]
					paras = paras[:len(paras)-1]
					emitPara(p)
				}
			case "tc":
				if tbl != nil && tbl.depth == 1 {
					cell := strings.Join(tbl.cell, "<br>")
					cell = strings.ReplaceAll(cell, "|", "\\|")
					tbl.cur = append(tbl.cur, cell)
					tbl.inCell = false
				}
			case "tr":
				if tbl != nil && tbl.depth == 1 && len(tbl.cur) > 0 {
					if len(tbl.cur) > tbl.maxCols {
						tbl.maxCols = len(tbl.cur)
					}
					tbl.rows = append(tbl.rows, tbl.cur)
				}
			case "tbl":
				if tbl != nil {
					tbl.depth--
					if tbl.depth == 0 {
						flushTable(tbl)
						tbl = nil
					}
				}
			}

		case xml.CharData:
			// Only w:t carries visible text; w:delText (tracked deletions) and
			// w:instrText (field codes) are separate elements and are ignored.
			if inT {
				if p := cur(); p != nil {
					p.text.Write(elem)
				}
			}
		}
	}

	return strings.TrimSpace(sb.String()), nil
}
