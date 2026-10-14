package engine

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// ExtractDocx reads word/document.xml from a .docx file and produces Markdown.
func ExtractDocx(filePath string) (string, error) {
	r, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("open docx zip: %w", err)
	}
	defer r.Close()

	var docFile *zip.File
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return "", fmt.Errorf("word/document.xml not found in %s", filePath)
	}

	rc, err := docFile.Open()
	if err != nil {
		return "", fmt.Errorf("open word/document.xml: %w", err)
	}
	defer rc.Close()

	return parseDocumentXML(rc)
}

func parseDocumentXML(r io.Reader) (string, error) {
	decoder := xml.NewDecoder(r)

	var sb strings.Builder
	var inTable bool
	var inCell bool
	var inHeadingLevel int
	var currentCellText strings.Builder
	var currentRowCells []string

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
			name := elem.Name.Local
			switch name {
			case "tbl":
				inTable = true
				sb.WriteString("\n\n")
			case "tr":
				currentRowCells = nil
			case "tc":
				inCell = true
				currentCellText.Reset()
			case "pStyle":
				for _, attr := range elem.Attr {
					if attr.Name.Local == "val" {
						val := strings.ToLower(attr.Value)
						if strings.Contains(val, "heading1") || val == "1" {
							inHeadingLevel = 1
						} else if strings.Contains(val, "heading2") || val == "2" {
							inHeadingLevel = 2
						} else if strings.Contains(val, "heading3") || val == "3" {
							inHeadingLevel = 3
						}
					}
				}
			}

		case xml.EndElement:
			name := elem.Name.Local
			switch name {
			case "tbl":
				inTable = false
				sb.WriteString("\n")
			case "tr":
				if len(currentRowCells) > 0 {
					sb.WriteString("| ")
					sb.WriteString(strings.Join(currentRowCells, " | "))
					sb.WriteString(" |\n")
					// If first row in table, add separator
					if !strings.Contains(sb.String(), " --- |") {
						sb.WriteString("|")
						for range currentRowCells {
							sb.WriteString(" --- |")
						}
						sb.WriteString("\n")
					}
				}
			case "tc":
				inCell = false
				cellStr := strings.TrimSpace(currentCellText.String())
				cellStr = strings.ReplaceAll(cellStr, "|", "\\|")
				currentRowCells = append(currentRowCells, cellStr)
			case "p":
				if !inTable {
					if inHeadingLevel > 0 {
						prefix := strings.Repeat("#", inHeadingLevel)
						sb.WriteString(fmt.Sprintf("\n\n%s ", prefix))
						inHeadingLevel = 0
					} else {
						sb.WriteString("\n\n")
					}
				}
			}

		case xml.CharData:
			text := string(elem)
			if inCell {
				currentCellText.WriteString(text)
			} else {
				sb.WriteString(text)
			}
		}
	}

	return strings.TrimSpace(sb.String()), nil
}
