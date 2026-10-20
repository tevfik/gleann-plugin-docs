package engine

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ExtractXLSX extracts tables from a .xlsx spreadsheet and returns Markdown tables.
func ExtractXLSX(filePath string) (string, int, error) {
	r, err := zip.OpenReader(filePath)
	if err != nil {
		return "", 0, fmt.Errorf("open xlsx zip: %w", err)
	}
	defer r.Close()

	files := map[string]*zip.File{}
	for _, f := range r.File {
		files[filepath.ToSlash(f.Name)] = f
	}

	var sharedStrings []string
	if f := files["xl/sharedStrings.xml"]; f != nil {
		sharedStrings, _ = readSharedStrings(f)
	}

	sheets := orderedSheets(files)
	if len(sheets) == 0 {
		return "", 0, fmt.Errorf("no worksheets found in xlsx")
	}

	var sb strings.Builder
	for i, sh := range sheets {
		sheetMD, err := parseWorksheet(files[sh.path], sharedStrings)
		if err != nil || strings.TrimSpace(sheetMD) == "" {
			continue
		}
		name := sh.name
		if name == "" {
			name = fmt.Sprintf("Sheet %d", i+1)
		}
		fmt.Fprintf(&sb, "## %s\n\n%s\n\n", name, sheetMD)
	}

	return strings.TrimSpace(sb.String()), len(sheets), nil
}

type xlsxSheet struct {
	name string
	path string
}

// orderedSheets returns worksheets in workbook order with their real names,
// falling back to numerically sorted file names when workbook.xml is unusable.
func orderedSheets(files map[string]*zip.File) []xlsxSheet {
	var out []xlsxSheet

	if wb := files["xl/workbook.xml"]; wb != nil {
		rels := map[string]string{}
		if rf := files["xl/_rels/workbook.xml.rels"]; rf != nil {
			if rc, err := rf.Open(); err == nil {
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
						rels[id] = target
					}
				}
				rc.Close()
			}
		}
		if rc, err := wb.Open(); err == nil {
			dec := xml.NewDecoder(rc)
			for {
				tok, err := dec.Token()
				if err != nil {
					break
				}
				if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sheet" {
					var name, rid string
					for _, a := range se.Attr {
						switch a.Name.Local {
						case "name":
							name = a.Value
						case "id": // r:id
							rid = a.Value
						}
					}
					target := rels[rid]
					if target == "" {
						continue
					}
					p := strings.TrimPrefix(target, "/")
					if !strings.HasPrefix(target, "/") {
						p = path.Join("xl", target)
					}
					if files[p] != nil {
						out = append(out, xlsxSheet{name: name, path: p})
					}
				}
			}
			rc.Close()
		}
	}
	if len(out) > 0 {
		return out
	}

	var nums []int
	byNum := map[int]string{}
	for name := range files {
		if strings.HasPrefix(name, "xl/worksheets/sheet") && strings.HasSuffix(name, ".xml") {
			n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "xl/worksheets/sheet"), ".xml"))
			nums = append(nums, n)
			byNum[n] = name
		}
	}
	sort.Ints(nums)
	for _, n := range nums {
		out = append(out, xlsxSheet{path: byNum[n]})
	}
	return out
}

// readSharedStrings returns one entry per <si>. A single <si> may hold several
// rich-text runs (<r><t>..</t></r>) which must be concatenated, and phonetic
// hints (<rPh>) which must be skipped; appending per <t> shifts every index.
func readSharedStrings(f *zip.File) ([]string, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	decoder := xml.NewDecoder(rc)
	var list []string
	var inSI, inText bool
	phonetic := 0
	var current strings.Builder

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return list, err
		}

		switch elem := tok.(type) {
		case xml.StartElement:
			switch elem.Name.Local {
			case "si":
				inSI = true
				current.Reset()
			case "rPh":
				phonetic++
			case "t":
				inText = true
			}
		case xml.EndElement:
			switch elem.Name.Local {
			case "si":
				inSI = false
				list = append(list, current.String())
			case "rPh":
				phonetic--
			case "t":
				inText = false
			}
		case xml.CharData:
			if inSI && inText && phonetic == 0 {
				current.Write(elem)
			}
		}
	}

	return list, nil
}

// colIndex converts the column letters of a cell reference ("AB12") to a
// zero-based index. It returns -1 when the reference has no letters.
func colIndex(ref string) int {
	n := 0
	for _, c := range ref {
		switch {
		case c >= 'A' && c <= 'Z':
			n = n*26 + int(c-'A') + 1
		case c >= 'a' && c <= 'z':
			n = n*26 + int(c-'a') + 1
		default:
			return n - 1
		}
	}
	return n - 1
}

func parseWorksheet(f *zip.File, sharedStrings []string) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	decoder := xml.NewDecoder(rc)
	var rows [][]string
	var currentRow []string
	var currentVal strings.Builder
	var cellType string
	var cellCol int
	var inValue, inInline bool
	nextCol := 0

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		switch elem := tok.(type) {
		case xml.StartElement:
			switch elem.Name.Local {
			case "row":
				currentRow = nil
				nextCol = 0
			case "c":
				currentVal.Reset()
				cellType = ""
				cellCol = -1
				for _, attr := range elem.Attr {
					switch attr.Name.Local {
					case "t":
						cellType = attr.Value
					case "r":
						cellCol = colIndex(attr.Value)
					}
				}
				if cellCol < 0 {
					cellCol = nextCol
				}
			case "v":
				inValue = true
			case "is":
				inInline = true
			}
		case xml.EndElement:
			switch elem.Name.Local {
			case "row":
				if hasContent(currentRow) {
					rows = append(rows, currentRow)
				}
			case "c":
				val := strings.TrimSpace(currentVal.String())
				switch cellType {
				case "s":
					if idx, err := strconv.Atoi(val); err == nil && idx >= 0 && idx < len(sharedStrings) {
						val = sharedStrings[idx]
					}
				case "b":
					if val == "1" {
						val = "TRUE"
					} else if val == "0" {
						val = "FALSE"
					}
				}
				// Sparse files omit empty cells: place by column, not by order.
				for len(currentRow) < cellCol {
					currentRow = append(currentRow, "")
				}
				currentRow = append(currentRow, val)
				nextCol = cellCol + 1
			case "v":
				inValue = false
			case "is":
				inInline = false
			}
		case xml.CharData:
			if inValue || inInline {
				currentVal.Write(elem)
			}
		}
	}

	if len(rows) == 0 {
		return "", nil
	}

	return rowsToMarkdown(rows), nil
}

// rowsToMarkdown renders sparse rows as a markdown table whose first row is the header.
func rowsToMarkdown(rows [][]string) string {
	width := 0
	for _, row := range rows {
		n := len(row)
		for n > 0 && row[n-1] == "" {
			n--
		}
		if n > width {
			width = n
		}
	}

	esc := func(s string) string {
		s = strings.ReplaceAll(s, "|", "\\|")
		s = strings.ReplaceAll(s, "\r\n", "<br>")
		s = strings.ReplaceAll(s, "\n", "<br>")
		return s
	}

	var sb strings.Builder
	for i, row := range rows {
		cells := make([]string, width)
		for c := 0; c < width && c < len(row); c++ {
			cells[c] = esc(row[c])
		}
		sb.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		if i == 0 {
			sb.WriteString("|" + strings.Repeat(" --- |", width) + "\n")
		}
	}

	return sb.String()
}

func hasContent(row []string) bool {
	for _, c := range row {
		if c != "" {
			return true
		}
	}
	return false
}
