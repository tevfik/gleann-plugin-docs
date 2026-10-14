package engine

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
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

	// 1. Read shared strings if present
	var sharedStrings []string
	for _, f := range r.File {
		if filepath.ToSlash(f.Name) == "xl/sharedStrings.xml" {
			sharedStrings, _ = readSharedStrings(f)
			break
		}
	}

	// 2. Read sheet1.xml (and subsequent sheets)
	var sb strings.Builder
	sheetCount := 0
	for _, f := range r.File {
		name := filepath.ToSlash(f.Name)
		if strings.HasPrefix(name, "xl/worksheets/sheet") && strings.HasSuffix(name, ".xml") {
			sheetCount++
			sheetMD, err := parseWorksheet(f, sharedStrings)
			if err == nil && strings.TrimSpace(sheetMD) != "" {
				fmt.Fprintf(&sb, "## Sheet %d\n\n%s\n\n", sheetCount, sheetMD)
			}
		}
	}

	if sheetCount == 0 {
		return "", 0, fmt.Errorf("no worksheets found in xlsx")
	}

	return strings.TrimSpace(sb.String()), sheetCount, nil
}

func readSharedStrings(f *zip.File) ([]string, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	decoder := xml.NewDecoder(rc)
	var list []string
	var inText bool
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
			if elem.Name.Local == "t" {
				inText = true
				current.Reset()
			}
		case xml.EndElement:
			if elem.Name.Local == "t" {
				inText = false
				list = append(list, current.String())
				current.Reset()
			}
		case xml.CharData:
			if inText {
				current.WriteString(string(elem))
			}
		}
	}

	return list, nil
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
	var isSharedString bool
	var inValue bool

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
			if elem.Name.Local == "row" {
				currentRow = nil
			} else if elem.Name.Local == "c" {
				currentVal.Reset()
				isSharedString = false
				for _, attr := range elem.Attr {
					if attr.Name.Local == "t" && attr.Value == "s" {
						isSharedString = true
					}
				}
			} else if elem.Name.Local == "v" {
				inValue = true
			}
		case xml.EndElement:
			if elem.Name.Local == "row" {
				if len(currentRow) > 0 {
					rows = append(rows, currentRow)
				}
			} else if elem.Name.Local == "c" {
				val := strings.TrimSpace(currentVal.String())
				if isSharedString {
					if idx, err := strconv.Atoi(val); err == nil && idx >= 0 && idx < len(sharedStrings) {
						val = sharedStrings[idx]
					}
				}
				currentRow = append(currentRow, val)
			} else if elem.Name.Local == "v" {
				inValue = false
			}
		case xml.CharData:
			if inValue {
				currentVal.WriteString(string(elem))
			}
		}
	}

	if len(rows) == 0 {
		return "", nil
	}

	// Render as Markdown table
	var sb strings.Builder
	header := rows[0]
	sb.WriteString("| ")
	sb.WriteString(strings.Join(header, " | "))
	sb.WriteString(" |\n|")
	for range header {
		sb.WriteString(" --- |")
	}
	sb.WriteString("\n")

	for _, row := range rows[1:] {
		// Pad row if needed
		for len(row) < len(header) {
			row = append(row, "")
		}
		sb.WriteString("| ")
		sb.WriteString(strings.Join(row[:len(header)], " | "))
		sb.WriteString(" |\n")
	}

	return sb.String(), nil
}
