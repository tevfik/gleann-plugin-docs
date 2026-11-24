package engine

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// budgetExemptions lists the places in the engine package that may use a raw
// strings.Builder, bytes.Buffer or strings.Join. Everything that holds OUTPUT
// text (documents, slides, cells, list items, sheets, pages) must use
// mdBuilder, which draws on the conversion's single outBudget. The key is
// "file:declaration:expression"; each entry says why it cannot multiply output.
var budgetExemptions = map[string]string{
	"limits.go:mdBuilder:strings.Builder":              "the budgeted builder type itself",
	"headings.go:promoteNumberedHeadings:strings.Join": "re-joins the lines of a page whose text was already charged; same size",
	"xls.go:isDateFormat:strings.Builder":              "scratch for one number-format string, bounded by the workbook's format record (input)",
	"xls.go:readSST:strings.Builder":                   "scratch for one shared string, bounded by its record (input); the sheet text is charged when rendered",
	"pdftable.go:text:strings.Join":                    "words of one text line, bounded by the page; the page result is charged by convertPDF",
	"pdftable.go:spliceTables:strings.Join":            "working copy of one page, bounded by page geometry; convertPDF charges the page result to the document budget",
	"pptx.go:pptxPara:strings.Builder":                 "text of one paragraph, copied 1:1 from XML already charged to the zip budget; the table/body writes are charged",
	"pptx.go:extractSlideContent:strings.Join":         "joins the paragraphs of one cell, 1:1 input text; the table is charged by writeTable",
	"html.go:normalizeWhitespace:strings.Join":         "collapses whitespace of one text node, never larger than the node",
	"html.go:cleanMarkdown:strings.Join":               "post-processes the document buffer after it was charged; never larger than it",
	"xlsx.go:readSharedStrings:strings.Builder":        "one shared string, bounded by the member read through the zip budget; cells that use it are charged per use",
	"xlsx.go:parseWorksheet:strings.Builder":           "text of one cell, bounded by the member read through the zip budget; cells are charged per use",
	"docx.go:docxPara:strings.Builder":                 "text of one paragraph, copied 1:1 from XML already charged to the zip budget",
	"docx.go:parseDocumentXMLWithStyles:strings.Join":  "joins the paragraphs of one cell, 1:1 input text; the table is charged by writeTable",
}

// repeatExemptions lists strings.Repeat / bytes.Repeat calls whose count is not
// a constant. A count that comes from document input must be bounded first;
// each entry says what bounds it.
var repeatExemptions = map[string]string{
	"html.go:walk:strings.Repeat":                        "heading level is the digit of an h1..h6 tag (1..6); list indent uses clampListLevel",
	"docx.go:parseDocumentXMLWithStyles:strings.Repeat":  "heading is 1..6 (style and outline level checked) and listLvl is clamped by clampListLevel",
	"pptx.go:extractSlideContent:strings.Repeat":         "level is clamped by clampListLevel when read",
	"headings.go:promoteNumberedHeadings:strings.Repeat": "depth is capped at 5 just before the call",
}

func TestOutputTextUsesTheBudgetedBuilder(t *testing.T) {
	fset := token.NewFileSet()
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]*ast.File{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = f
	}
	seen := map[string]bool{}
	seenRepeat := map[string]bool{}
	var bad []string
	{
		for fname, file := range files {
			fname = filepath.Base(fname)
			for _, decl := range file.Decls {
				name := declName(decl)
				ast.Inspect(decl, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok {
						if s, ok := call.Fun.(*ast.SelectorExpr); ok && len(call.Args) == 2 {
							if x, ok := s.X.(*ast.Ident); ok && s.Sel.Name == "Repeat" && (x.Name == "strings" || x.Name == "bytes") {
								if _, isLit := call.Args[1].(*ast.BasicLit); !isLit {
									key := fname + ":" + name + ":" + x.Name + ".Repeat"
									seenRepeat[key] = true
									if _, ok := repeatExemptions[key]; !ok {
										bad = append(bad, fmt.Sprintf("%s: non-constant repeat count (%s)", key, fset.Position(call.Pos())))
									}
								}
							}
						}
					}
					sel, ok := n.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					x, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					expr := x.Name + "." + sel.Sel.Name
					if expr != "strings.Builder" && expr != "bytes.Buffer" && expr != "strings.Join" {
						return true
					}
					key := fname + ":" + name + ":" + expr
					seen[key] = true
					if _, ok := budgetExemptions[key]; !ok {
						bad = append(bad, fmt.Sprintf("%s (%s)", key, fset.Position(sel.Pos())))
					}
					return true
				})
			}
		}
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("output text must go through mdBuilder (one budget per conversion); not allowlisted: %s", b)
	}
	for key := range repeatExemptions {
		if !seenRepeat[key] {
			t.Errorf("stale repeat exemption %q: remove it", key)
		}
	}
	for key := range budgetExemptions {
		if !seen[key] {
			t.Errorf("stale exemption %q: remove it", key)
		}
	}
}

// declName names a top-level declaration: the function, or the type for a type
// declaration.
func declName(d ast.Decl) string {
	switch d := d.(type) {
	case *ast.FuncDecl:
		return d.Name.Name
	case *ast.GenDecl:
		for _, sp := range d.Specs {
			if ts, ok := sp.(*ast.TypeSpec); ok {
				return ts.Name.Name
			}
		}
		return "<decl>"
	}
	return "<decl>"
}
