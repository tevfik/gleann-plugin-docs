package parser

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// OTSL (Optimized Table Structure Language) describes a table as a stream of
// cell tokens, each followed by its text:
//
//	<fcel>  cell with content            <ecel>  empty cell
//	<lcel>  merged with the cell on its left (column span)
//	<ucel>  merged with the cell above (row span)
//	<xcel>  merged both ways (inside a 2-D span)
//	<ched>  column header cell           <rhed>  row header cell
//	<srow>  section row cell             <nl>    end of row
//
// parseOTSL reads that stream into a grid; renderOTSL turns the grid into a
// Markdown table, or into HTML when a cell spans rows or columns (Markdown
// tables cannot express that).

type otslKind int

const (
	otslFull otslKind = iota
	otslEmpty
	otslLeft
	otslUp
	otslCross
	otslColHead
	otslRowHead
	otslSection
)

type otslCell struct {
	kind otslKind
	text string
}

var (
	otslTokenRe   = regexp.MustCompile(`<(fcel|ecel|lcel|ucel|xcel|ched|rhed|srow|nl)>`)
	otslCaptionRe = regexp.MustCompile(`(?s)<caption>(.*?)</caption>`)
)

var otslKinds = map[string]otslKind{
	"fcel": otslFull, "ecel": otslEmpty, "lcel": otslLeft, "ucel": otslUp,
	"xcel": otslCross, "ched": otslColHead, "rhed": otslRowHead, "srow": otslSection,
}

// isOTSL reports whether s uses OTSL cell tokens (as opposed to <tr>/<td>).
func isOTSL(s string) bool {
	return otslTokenRe.MatchString(s) && !strings.Contains(s, "<trow>") && !strings.Contains(s, "<tr>")
}

// parseOTSL reads an OTSL stream. It returns the grid (ragged rows padded with
// empty cells) and the caption found inside the table, if any.
func parseOTSL(s string) (grid [][]otslCell, caption string) {
	if m := otslCaptionRe.FindStringSubmatch(s); m != nil {
		caption = strings.TrimSpace(stripRemainingTags(m[1]))
		s = otslCaptionRe.ReplaceAllString(s, "")
	}

	idx := otslTokenRe.FindAllStringSubmatchIndex(s, -1)
	var row []otslCell
	flush := func() {
		if len(row) > 0 {
			grid = append(grid, row)
			row = nil
		}
	}
	for i, m := range idx {
		tok := s[m[2]:m[3]]
		if tok == "nl" {
			flush()
			continue
		}
		end := len(s)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		text := strings.TrimSpace(stripRemainingTags(s[m[1]:end]))
		row = append(row, otslCell{kind: otslKinds[tok], text: text})
	}
	flush()

	width := 0
	for _, r := range grid {
		width = max(width, len(r))
	}
	for i, r := range grid {
		for len(r) < width {
			r = append(r, otslCell{kind: otslEmpty})
		}
		grid[i] = r
	}
	return grid, caption
}

// span returns how many columns and rows the anchor cell at (r, c) covers.
func otslSpan(grid [][]otslCell, r, c int) (cols, rows int) {
	cols, rows = 1, 1
	for cc := c + 1; cc < len(grid[r]) && grid[r][cc].kind == otslLeft; cc++ {
		cols++
	}
	for rr := r + 1; rr < len(grid) && grid[rr][c].kind == otslUp; rr++ {
		rows++
	}
	return cols, rows
}

// covered reports whether the cell is part of another cell's span.
func otslCovered(k otslKind) bool { return k == otslLeft || k == otslUp || k == otslCross }

// renderOTSL renders the grid. The caption, if any, is placed above the table.
func renderOTSL(grid [][]otslCell, caption string) string {
	if len(grid) == 0 || len(grid[0]) == 0 {
		return ""
	}

	spans := false
	for r := range grid {
		for c := range grid[r] {
			if otslCovered(grid[r][c].kind) {
				spans = true
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("\n\n")
	if caption != "" {
		sb.WriteString("*" + caption + "*\n\n")
	}
	if spans {
		renderOTSLHTML(&sb, grid)
	} else {
		renderOTSLMarkdown(&sb, grid)
	}
	sb.WriteString("\n")
	return sb.String()
}

func renderOTSLMarkdown(sb *strings.Builder, grid [][]otslCell) {
	cols := len(grid[0])
	for i, row := range grid {
		cells := make([]string, cols)
		for c, cell := range row {
			cells[c] = strings.ReplaceAll(cell.text, "|", `\|`)
		}
		sb.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		if i == 0 {
			sb.WriteString("|" + strings.Repeat(" --- |", cols) + "\n")
		}
	}
}

func renderOTSLHTML(sb *strings.Builder, grid [][]otslCell) {
	sb.WriteString("<table>\n")
	for r, row := range grid {
		sb.WriteString("<tr>")
		for c, cell := range row {
			if otslCovered(cell.kind) {
				continue
			}
			tag := "td"
			if cell.kind == otslColHead || cell.kind == otslRowHead || (r == 0 && cell.kind != otslEmpty && !hasHeaderTokens(grid)) {
				tag = "th"
			}
			cs, rs := otslSpan(grid, r, c)
			sb.WriteString("<" + tag)
			if cs > 1 {
				sb.WriteString(` colspan="` + strconv.Itoa(cs) + `"`)
			}
			if rs > 1 {
				sb.WriteString(` rowspan="` + strconv.Itoa(rs) + `"`)
			}
			sb.WriteString(">" + html.EscapeString(cell.text) + "</" + tag + ">")
		}
		sb.WriteString("</tr>\n")
	}
	sb.WriteString("</table>")
}

// hasHeaderTokens reports whether the model marked any header cell explicitly;
// if not, the first row is treated as the header.
func hasHeaderTokens(grid [][]otslCell) bool {
	for _, row := range grid {
		for _, c := range row {
			if c.kind == otslColHead || c.kind == otslRowHead {
				return true
			}
		}
	}
	return false
}
