package engine

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// containerXML defines the structure of META-INF/container.xml in EPUB.
type containerXML struct {
	XMLName   xml.Name   `xml:"container"`
	RootFiles []rootFile `xml:"rootfiles>rootfile"`
}

type rootFile struct {
	FullPath  string `xml:"full-path,attr"`
	MediaType string `xml:"media-type,attr"`
}

// opfPackage defines the structure of the OPF package document.
type opfPackage struct {
	XMLName  xml.Name      `xml:"package"`
	Manifest []manifestItem `xml:"manifest>item"`
	Spine    []spineItemref `xml:"spine>itemref"`
}

type manifestItem struct {
	ID        string `xml:"id,attr"`
	Href      string `xml:"href,attr"`
	MediaType string `xml:"media-type,attr"`
}

type spineItemref struct {
	IDRef string `xml:"idref,attr"`
}

// ExtractEPUB extracts reading chapters from an EPUB file into Markdown.
// Returns the concatenated markdown, chapter count, and any error.
func ExtractEPUB(filePath string) (string, int, error) {
	z, err := zip.OpenReader(filePath)
	if err != nil {
		return "", 0, fmt.Errorf("open epub zip %s: %w", filePath, err)
	}
	defer z.Close()

	// 1. Locate the OPF package file
	opfPath := findOPFPath(z)

	var chapterFiles []string
	if opfPath != "" {
		chapterFiles = getSpineFiles(z, opfPath)
	}

	// 2. Fallback if OPF spine wasn't resolved: sort all HTML/XHTML entries
	if len(chapterFiles) == 0 {
		chapterFiles = findHTMLFiles(z)
	}

	if len(chapterFiles) == 0 {
		return "", 0, fmt.Errorf("no readable chapters found in epub %s", filePath)
	}

	// Index zip entries by normalized path
	fileMap := make(map[string]*zip.File)
	for _, f := range z.File {
		norm := path.Clean(strings.ReplaceAll(f.Name, "\\", "/"))
		fileMap[norm] = f
	}

	var chapters []string
	validCount := 0

	for _, chPath := range chapterFiles {
		norm := path.Clean(strings.ReplaceAll(chPath, "\\", "/"))
		zf, ok := fileMap[norm]
		if !ok {
			// Try case-insensitive lookup
			for k, v := range fileMap {
				if strings.EqualFold(k, norm) {
					zf = v
					ok = true
					break
				}
			}
		}
		if !ok {
			continue
		}

		rc, err := zf.Open()
		if err != nil {
			continue
		}
		md, err := ExtractHTML(rc)
		_ = rc.Close()

		if err == nil && strings.TrimSpace(md) != "" {
			chapters = append(chapters, md)
			validCount++
		}
	}

	if len(chapters) == 0 {
		return "", 0, fmt.Errorf("failed to extract content from epub %s", filePath)
	}

	return strings.Join(chapters, "\n\n---\n\n"), validCount, nil
}

func findOPFPath(z *zip.ReadCloser) string {
	// Look in META-INF/container.xml
	for _, f := range z.File {
		if strings.EqualFold(f.Name, "META-INF/container.xml") {
			rc, err := f.Open()
			if err == nil {
				data, _ := io.ReadAll(rc)
				_ = rc.Close()

				var c containerXML
				if err := xml.Unmarshal(data, &c); err == nil {
					for _, rf := range c.RootFiles {
						if rf.FullPath != "" {
							return rf.FullPath
						}
					}
				}
			}
		}
	}

	// Fallback: look for any .opf file
	for _, f := range z.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".opf") {
			return f.Name
		}
	}

	return ""
}

func getSpineFiles(z *zip.ReadCloser, opfPath string) []string {
	var opfFile *zip.File
	for _, f := range z.File {
		if strings.EqualFold(f.Name, opfPath) {
			opfFile = f
			break
		}
	}
	if opfFile == nil {
		return nil
	}

	rc, err := opfFile.Open()
	if err != nil {
		return nil
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()

	var pkg opfPackage
	if err := xml.Unmarshal(data, &pkg); err != nil {
		return nil
	}

	// Map item ID -> Href
	baseDir := path.Dir(opfPath)
	itemMap := make(map[string]string)
	for _, it := range pkg.Manifest {
		itemMap[it.ID] = it.Href
	}

	var ordered []string
	for _, sp := range pkg.Spine {
		href, ok := itemMap[sp.IDRef]
		if ok && href != "" {
			full := path.Join(baseDir, href)
			ordered = append(ordered, full)
		}
	}

	return ordered
}

func findHTMLFiles(z *zip.ReadCloser) []string {
	var htmls []string
	for _, f := range z.File {
		lower := strings.ToLower(f.Name)
		if strings.HasSuffix(lower, ".xhtml") || strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm") {
			// Skip TOC and nav if possible, but keep content
			if !strings.Contains(lower, "toc.ncx") {
				htmls = append(htmls, f.Name)
			}
		}
	}
	sort.Strings(htmls)
	return htmls
}
