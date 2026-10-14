package engine

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeZip(t *testing.T, name string, parts map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for n, c := range parts {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(c))
	}
	zw.Close()
	f.Close()
	return p
}

const wNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

func TestExtractDocx_HeadingsListsTables(t *testing.T) {
	doc := `<w:document ` + wNS + `><w:body>
<w:p><w:pPr><w:pStyle w:val="Balk1"/></w:pPr><w:r><w:t>Giriş</w:t></w:r></w:p>
<w:p><w:r><w:t>Body text</w:t></w:r><w:del><w:r><w:delText>deleted</w:delText></w:r></w:del></w:p>
<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>one</w:t></w:r></w:p>
<w:p><w:pPr><w:numPr><w:ilvl w:val="1"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>two</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>A</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>B|x</w:t></w:r></w:p><w:p><w:r><w:t>B2</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p><w:r><w:t>1</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>C</w:t></w:r></w:p></w:tc></w:tr><w:tr><w:tc><w:p><w:r><w:t>2</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
</w:body></w:document>`
	styles := `<w:styles ` + wNS + `><w:style w:styleId="Balk1"><w:name w:val="heading 1"/></w:style></w:styles>`
	p := writeZip(t, "a.docx", map[string]string{"word/document.xml": doc, "word/styles.xml": styles})

	got, err := ExtractDocx(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Giriş\n\nBody text\n\n- one\n  - two\n\n| A | B\\|x<br>B2 |\n| --- | --- |\n| 1 |  |",
		"| C |\n| --- |\n| 2 |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "deleted") {
		t.Errorf("tracked deletion leaked into output:\n%s", got)
	}
}

func TestExtractXLSX_RichStringsAndSparseCells(t *testing.T) {
	sst := `<sst><si><t>Name</t></si><si><r><t>Hel</t></r><r><t>lo</t></r></si><si><t>Z</t><rPh><t>ignored</t></rPh></si><si><t>Last</t></si></sst>`
	sheet := `<worksheet><sheetData>
<row r="1"><c r="A1" t="s"><v>0</v></c><c r="C1" t="s"><v>2</v></c></row>
<row r="2"><c r="A2" t="s"><v>1</v></c><c r="B2"><v>42</v></c><c r="C2" t="s"><v>3</v></c></row>
<row r="3"><c r="B3" t="inlineStr"><is><t>inline</t></is></c><c r="C3" t="b"><v>1</v></c></row>
</sheetData></worksheet>`
	wb := `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Second" r:id="rId2"/><sheet name="First" r:id="rId1"/></sheets></workbook>`
	rels := `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Target="worksheets/sheet2.xml"/></Relationships>`
	p := writeZip(t, "a.xlsx", map[string]string{
		"xl/workbook.xml":            wb,
		"xl/_rels/workbook.xml.rels": rels,
		"xl/sharedStrings.xml":       sst,
		"xl/worksheets/sheet1.xml":   `<worksheet><sheetData><row r="1"><c r="A1"><v>x</v></c></row></sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml":   sheet,
	})

	got, n, err := ExtractXLSX(p)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("sheet count = %d, want 2", n)
	}
	want := "## Second\n\n| Name |  | Z |\n| --- | --- | --- |\n| Hello | 42 | Last |\n|  | inline | TRUE |"
	if !strings.Contains(got, want) {
		t.Errorf("missing table:\n%s\n--- got ---\n%s", want, got)
	}
	if strings.Index(got, "## Second") > strings.Index(got, "## First") {
		t.Errorf("sheets not in workbook order:\n%s", got)
	}
}

func TestExtractPPTX_OrderTitleTableNotes(t *testing.T) {
	const aNS = `xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"`
	pres := `<p:presentation ` + aNS + `><p:sldIdLst><p:sldId id="256" r:id="rId2" xmlns:r="x"/><p:sldId id="257" r:id="rId1" xmlns:r="x"/></p:sldIdLst></p:presentation>`
	rels := `<Relationships><Relationship Id="rId1" Target="slides/slide1.xml"/><Relationship Id="rId2" Target="slides/slide2.xml"/></Relationships>`
	slide1 := `<p:sld ` + aNS + `><p:cSld><p:spTree>
<p:sp><p:nvSpPr><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>Intro</a:t></a:r></a:p></p:txBody></p:sp>
<p:sp><p:txBody><a:p><a:pPr lvl="1"><a:buChar char="•"/></a:pPr><a:r><a:t>point</a:t></a:r><a:br/><a:r><a:t>cont</a:t></a:r></a:p></p:txBody></p:sp>
<p:sp><p:nvSpPr><p:nvPr><p:ph type="sldNum"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>7</a:t></a:r></a:p></p:txBody></p:sp>
</p:spTree></p:cSld></p:sld>`
	slide2 := `<p:sld ` + aNS + `><p:cSld><p:spTree><p:graphicFrame><a:tbl>
<a:tr><a:tc><a:txBody><a:p><a:r><a:t>H1</a:t></a:r></a:p></a:txBody></a:tc><a:tc><a:txBody><a:p><a:r><a:t>H2</a:t></a:r></a:p></a:txBody></a:tc></a:tr>
<a:tr><a:tc><a:txBody><a:p><a:r><a:t>v1</a:t></a:r></a:p></a:txBody></a:tc><a:tc><a:txBody><a:p><a:r><a:t>v2</a:t></a:r></a:p></a:txBody></a:tc></a:tr>
</a:tbl></p:graphicFrame></p:spTree></p:cSld></p:sld>`
	p := writeZip(t, "a.pptx", map[string]string{
		"ppt/presentation.xml":            pres,
		"ppt/_rels/presentation.xml.rels": rels,
		"ppt/slides/slide1.xml":           slide1,
		"ppt/slides/slide2.xml":           slide2,
	})

	got, n, err := ExtractPPTX(p)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("slide count = %d, want 2", n)
	}
	// presentation order puts slide2.xml (the table) first
	if strings.Index(got, "| H1 | H2 |") > strings.Index(got, "Intro") {
		t.Errorf("slides not in presentation order:\n%s", got)
	}
	for _, want := range []string{"| H1 | H2 |\n| --- | --- |\n| v1 | v2 |", "## Slide 2: Intro", "- point cont"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\n7") {
		t.Errorf("slide number placeholder leaked:\n%s", got)
	}
}
