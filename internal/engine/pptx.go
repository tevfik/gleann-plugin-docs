package engine

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ExtractPPTX extracts structured text and slides from a .pptx file.
func ExtractPPTX(filePath string) (string, int, error) {
	r, err := zip.OpenReader(filePath)
	if err != nil {
		return "", 0, fmt.Errorf("open pptx zip: %w", err)
	}
	defer r.Close()

	// Find all slide XML files
	var slideFiles []*zip.File
	for _, f := range r.File {
		name := filepath.ToSlash(f.Name)
		if strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml") {
			slideFiles = append(slideFiles, f)
		}
	}

	if len(slideFiles) == 0 {
		return "", 0, fmt.Errorf("no slides found in pptx")
	}

	// Sort slides numerically: slide1.xml, slide2.xml, ..., slide10.xml
	sort.Slice(slideFiles, func(i, j int) bool {
		numI := parseSlideNumber(slideFiles[i].Name)
		numJ := parseSlideNumber(slideFiles[j].Name)
		return numI < numJ
	})

	var sb strings.Builder
	for idx, sf := range slideFiles {
		slideNum := idx + 1
		text, err := extractSlideText(sf)
		if err != nil {
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}

		fmt.Fprintf(&sb, "## Slide %d\n\n%s\n\n", slideNum, text)
	}

	return strings.TrimSpace(sb.String()), len(slideFiles), nil
}

func parseSlideNumber(name string) int {
	base := filepath.Base(name)
	base = strings.TrimPrefix(base, "slide")
	base = strings.TrimSuffix(base, ".xml")
	n, _ := strconv.Atoi(base)
	return n
}

func extractSlideText(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	decoder := xml.NewDecoder(rc)
	var sb strings.Builder
	var inParagraph bool
	var inText bool
	var currentPara strings.Builder

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
			local := elem.Name.Local
			if local == "p" {
				inParagraph = true
				currentPara.Reset()
			} else if local == "t" {
				inText = true
			}
		case xml.EndElement:
			local := elem.Name.Local
			if local == "p" {
				inParagraph = false
				line := strings.TrimSpace(currentPara.String())
				if line != "" {
					sb.WriteString(line)
					sb.WriteString("\n\n")
				}
				currentPara.Reset()
			} else if local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText {
				text := string(elem)
				if inParagraph {
					currentPara.WriteString(text)
				} else {
					sb.WriteString(text)
				}
			}
		}
	}

	return sb.String(), nil
}
