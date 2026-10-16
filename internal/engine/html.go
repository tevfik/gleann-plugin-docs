package engine

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"os"
	"strings"

	nethtml "golang.org/x/net/html"
)

// ExtractHTMLFile reads an HTML file and converts its readable content to clean Markdown.
func ExtractHTMLFile(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read html file %s: %w", filePath, err)
	}
	return ExtractHTML(bytes.NewReader(data))
}

// ExtractHTML parses HTML from an io.Reader and converts it to Markdown.
func ExtractHTML(r io.Reader) (string, error) {
	doc, err := nethtml.Parse(r)
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}

	var sb strings.Builder
	conv := &htmlConverter{
		buf: &sb,
	}
	conv.walk(doc)

	res := cleanMarkdown(sb.String())
	return res, nil
}

type htmlConverter struct {
	buf       *strings.Builder
	inPre     bool
	listStack []listState
}

type listState struct {
	ordered bool
	index   int
}

func (c *htmlConverter) walk(n *nethtml.Node) {
	if n == nil {
		return
	}

	switch n.Type {
	case nethtml.DocumentNode:
		c.walkChildren(n)
		return

	case nethtml.TextNode:
		text := n.Data
		if !c.inPre {
			// Normalize whitespace in non-pre text
			text = normalizeWhitespace(text)
		}
		c.buf.WriteString(html.UnescapeString(text))
		return

	case nethtml.ElementNode:
		tag := strings.ToLower(n.Data)

		// Elements to skip completely
		switch tag {
		case "script", "style", "head", "noscript", "svg", "iframe":
			return
		}

		// Handle headings
		if len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6' {
			level := int(tag[1] - '0')
			c.ensureNewline()
			c.buf.WriteString(strings.Repeat("#", level) + " ")
			c.walkChildren(n)
			c.buf.WriteString("\n\n")
			return
		}

		switch tag {
		case "p":
			c.ensureNewline()
			c.walkChildren(n)
			c.buf.WriteString("\n\n")

		case "br":
			c.buf.WriteString("\n")

		case "hr":
			c.ensureNewline()
			c.buf.WriteString("---\n\n")

		case "strong", "b":
			c.buf.WriteString("**")
			c.walkChildren(n)
			c.buf.WriteString("**")

		case "em", "i":
			c.buf.WriteString("*")
			c.walkChildren(n)
			c.buf.WriteString("*")

		case "del", "s", "strike":
			c.buf.WriteString("~~")
			c.walkChildren(n)
			c.buf.WriteString("~~")

		case "code":
			if c.inPre {
				c.walkChildren(n)
			} else {
				c.buf.WriteString("`")
				c.walkChildren(n)
				c.buf.WriteString("`")
			}

		case "pre":
			c.ensureNewline()
			c.buf.WriteString("```\n")
			c.inPre = true
			c.walkChildren(n)
			c.inPre = false
			c.ensureNewline()
			c.buf.WriteString("```\n\n")

		case "blockquote":
			c.ensureNewline()
			var inner strings.Builder
			innerConv := &htmlConverter{buf: &inner, inPre: c.inPre}
			innerConv.walkChildren(n)
			lines := strings.Split(strings.TrimSpace(inner.String()), "\n")
			for _, line := range lines {
				c.buf.WriteString("> " + line + "\n")
			}
			c.buf.WriteString("\n")

		case "ul":
			c.ensureNewline()
			c.listStack = append(c.listStack, listState{ordered: false})
			c.walkChildren(n)
			c.listStack = c.listStack[:len(c.listStack)-1]
			c.buf.WriteString("\n")

		case "ol":
			c.ensureNewline()
			c.listStack = append(c.listStack, listState{ordered: true, index: 1})
			c.walkChildren(n)
			c.listStack = c.listStack[:len(c.listStack)-1]
			c.buf.WriteString("\n")

		case "li":
			indent := strings.Repeat("  ", max(0, len(c.listStack)-1))
			prefix := "- "
			if len(c.listStack) > 0 && c.listStack[len(c.listStack)-1].ordered {
				curr := &c.listStack[len(c.listStack)-1]
				prefix = fmt.Sprintf("%d. ", curr.index)
				curr.index++
			}
			c.ensureNewline()
			c.buf.WriteString(indent + prefix)
			var itemBuf strings.Builder
			itemConv := &htmlConverter{buf: &itemBuf, inPre: c.inPre, listStack: c.listStack}
			itemConv.walkChildren(n)
			c.buf.WriteString(strings.TrimSpace(itemBuf.String()) + "\n")

		case "a":
			href := getAttr(n, "href")
			var linkText strings.Builder
			textConv := &htmlConverter{buf: &linkText, inPre: c.inPre}
			textConv.walkChildren(n)
			txt := strings.TrimSpace(linkText.String())
			if href != "" && txt != "" {
				c.buf.WriteString(fmt.Sprintf("[%s](%s)", txt, href))
			} else if txt != "" {
				c.buf.WriteString(txt)
			}

		case "img":
			src := getAttr(n, "src")
			alt := getAttr(n, "alt")
			if alt == "" {
				alt = "image"
			}
			if src != "" {
				c.buf.WriteString(fmt.Sprintf("![%s](%s)", alt, src))
			}

		case "table":
			c.ensureNewline()
			c.renderTable(n)
			c.buf.WriteString("\n\n")

		default:
			c.walkChildren(n)
		}
	}
}

func (c *htmlConverter) walkChildren(n *nethtml.Node) {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.walk(ch)
	}
}

func (c *htmlConverter) ensureNewline() {
	s := c.buf.String()
	if len(s) > 0 && !strings.HasSuffix(s, "\n") {
		c.buf.WriteString("\n")
	}
}

func (c *htmlConverter) renderTable(tableNode *nethtml.Node) {
	var rows [][]string
	var findRows func(*nethtml.Node)
	findRows = func(n *nethtml.Node) {
		if n.Type == nethtml.ElementNode && strings.ToLower(n.Data) == "tr" {
			var row []string
			for cell := n.FirstChild; cell != nil; cell = cell.NextSibling {
				if cell.Type == nethtml.ElementNode {
					tag := strings.ToLower(cell.Data)
					if tag == "td" || tag == "th" {
						var cellBuf strings.Builder
						cellConv := &htmlConverter{buf: &cellBuf}
						cellConv.walkChildren(cell)
						// In markdown table cells, replace newlines and pipes
						cellText := strings.TrimSpace(cellBuf.String())
						cellText = strings.ReplaceAll(cellText, "|", "\\|")
						cellText = strings.ReplaceAll(cellText, "\n", " ")
						row = append(row, cellText)
					}
				}
			}
			if len(row) > 0 {
				rows = append(rows, row)
			}
			return
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			findRows(ch)
		}
	}
	findRows(tableNode)

	if len(rows) == 0 {
		return
	}

	maxCols := 0
	for _, r := range rows {
		if len(r) > maxCols {
			maxCols = len(r)
		}
	}
	if maxCols == 0 {
		return
	}

	// Pad rows to maxCols
	for i := range rows {
		for len(rows[i]) < maxCols {
			rows[i] = append(rows[i], "")
		}
	}

	// Render Header Row
	c.buf.WriteString("| " + strings.Join(rows[0], " | ") + " |\n")
	// Render Separator
	seps := make([]string, maxCols)
	for i := range seps {
		seps[i] = "---"
	}
	c.buf.WriteString("| " + strings.Join(seps, " | ") + " |\n")

	// Render Data Rows
	for _, r := range rows[1:] {
		c.buf.WriteString("| " + strings.Join(r, " | ") + " |\n")
	}
}

func getAttr(n *nethtml.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func normalizeWhitespace(s string) string {
	var words []string
	for _, f := range strings.Fields(s) {
		words = append(words, f)
	}
	if len(words) == 0 {
		if len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\t') {
			return " "
		}
		return ""
	}
	res := strings.Join(words, " ")
	if s[0] == ' ' || s[0] == '\n' || s[0] == '\t' {
		res = " " + res
	}
	if s[len(s)-1] == ' ' || s[len(s)-1] == '\n' || s[len(s)-1] == '\t' {
		res = res + " "
	}
	return res
}

func cleanMarkdown(md string) string {
	lines := strings.Split(md, "\n")
	var result []string
	emptyCount := 0

	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			emptyCount++
			if emptyCount <= 1 {
				result = append(result, "")
			}
		} else {
			emptyCount = 0
			result = append(result, trimmed)
		}
	}

	return strings.TrimSpace(strings.Join(result, "\n"))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
