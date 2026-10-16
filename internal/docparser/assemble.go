package docparser

import (
	"image"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// listMarker matches a bullet (group 1), a number such as "3." or "3)" (group 2)
// or a bracketed reference number such as "[12]" (group 3) at the start of a line.
var listMarker = regexp.MustCompile(`^\s*(?:([•·●▪■◦]\s*|[-*–—]\s+)|(\d{1,3}[.)]\s+)|(\[\d{1,3}\]\s*))`)

// paragraphText joins OCR lines into one paragraph. A line ending in a hyphen
// followed by a lower-case continuation is glued back together.
func paragraphText(lines []TextLine) string {
	var cur string
	for _, ln := range lines {
		t := strings.TrimSpace(ln.Text)
		if t == "" {
			continue
		}
		if cur == "" {
			cur = t
			continue
		}
		cur = joinWrapped(cur, t)
	}
	return cur
}

// joinWrapped appends a continuation line to cur, undoing end-of-line hyphenation.
func joinWrapped(cur, next string) string {
	if strings.HasSuffix(cur, "-") && startsLower(next) && utf8.RuneCountInString(cur) > 1 && !strings.HasSuffix(cur, " -") {
		return strings.TrimSuffix(cur, "-") + next
	}
	return cur + " " + next
}

func startsLower(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsLower(r)
}

// listText turns OCR lines into Markdown list items: a line that starts with a
// bullet or number begins a new item, other lines continue the previous one.
func listText(lines []TextLine) string {
	var items []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln.Text)
		if t == "" {
			continue
		}
		if m := listMarker.FindStringSubmatchIndex(t); m != nil {
			switch {
			case m[2] >= 0: // bullet: normalise to "-"
				items = append(items, "- "+strings.TrimSpace(t[m[1]:]))
			case m[4] >= 0: // "3." / "3)": keep the number
				items = append(items, strings.TrimSpace(t[m[4]:m[5]])+" "+strings.TrimSpace(t[m[1]:]))
			default: // "[12]": keep the reference label as text
				items = append(items, "- "+t)
			}
			continue
		}
		if len(items) == 0 {
			items = append(items, "- "+t)
			continue
		}
		items[len(items)-1] = joinWrapped(items[len(items)-1], t)
	}
	return strings.Join(items, "\n")
}

// looksLikeList reports whether most lines start with a list marker.
func looksLikeList(lines []TextLine) bool {
	if len(lines) < 2 {
		return false
	}
	n := 0
	for _, l := range lines {
		if listMarker.MatchString(strings.TrimSpace(l.Text)) {
			n++
		}
	}
	return n >= 2 && float64(n) >= 0.4*float64(len(lines))
}

// gridMarkdown rebuilds a table from OCR line boxes: lines whose vertical
// centres are close form a row, and the gaps in the horizontal projection of
// all boxes separate the columns. It is a fallback for when no table
// recogniser is available, so it returns "" (the caller then emits plain text)
// when it cannot find at least two rows and two columns.
func gridMarkdown(lines []TextLine) string {
	if len(lines) < 4 {
		return ""
	}

	// Rows.
	sorted := append([]TextLine(nil), lines...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return centerY(sorted[i].Rect) < centerY(sorted[j].Rect)
	})
	heights := make([]int, len(sorted))
	for i, l := range sorted {
		heights[i] = l.Rect.Dy()
	}
	sort.Ints(heights)
	medH := heights[len(heights)/2]

	var rows [][]TextLine
	for _, l := range sorted {
		if n := len(rows); n > 0 {
			prev := rows[n-1]
			if abs(centerY(l.Rect)-rowCenter(prev)) <= medH*6/10 {
				rows[n-1] = append(rows[n-1], l)
				continue
			}
		}
		rows = append(rows, []TextLine{l})
	}
	if len(rows) < 2 {
		return ""
	}

	// Columns: merge the x-extents of all boxes, a gap wider than a character
	// starts a new column.
	type span struct{ lo, hi int }
	var spans []span
	for _, l := range sorted {
		spans = append(spans, span{l.Rect.Min.X, l.Rect.Max.X})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].lo < spans[j].lo })
	gap := max(medH/2, 6)
	cols := []span{spans[0]}
	for _, s := range spans[1:] {
		last := &cols[len(cols)-1]
		if s.lo-last.hi > gap {
			cols = append(cols, s)
		} else {
			last.hi = max(last.hi, s.hi)
		}
	}
	if len(cols) < 2 {
		return ""
	}

	colOf := func(r image.Rectangle) int {
		cx := (r.Min.X + r.Max.X) / 2
		for i, c := range cols {
			if cx >= c.lo && cx <= c.hi {
				return i
			}
		}
		return 0
	}

	var sb strings.Builder
	for ri, row := range rows {
		cells := make([]string, len(cols))
		sort.SliceStable(row, func(i, j int) bool { return row[i].Rect.Min.X < row[j].Rect.Min.X })
		for _, l := range row {
			c := colOf(l.Rect)
			if cells[c] != "" {
				cells[c] += " "
			}
			cells[c] += strings.ReplaceAll(strings.TrimSpace(l.Text), "|", `\|`)
		}
		sb.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		if ri == 0 {
			sb.WriteString("|" + strings.Repeat(" --- |", len(cols)) + "\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func centerY(r image.Rectangle) int { return r.Min.Y + r.Dy()/2 }

func rowCenter(row []TextLine) int {
	sum := 0
	for _, l := range row {
		sum += centerY(l.Rect)
	}
	return sum / len(row)
}

// plainLines is the last resort for a table that could not be rebuilt.
func plainLines(lines []TextLine) string {
	var parts []string
	for _, l := range lines {
		if t := strings.TrimSpace(l.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}
