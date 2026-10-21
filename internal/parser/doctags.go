package parser

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	// Spatial location tokens to strip
	locRegex      = regexp.MustCompile(`<loc_[0-9]+>|<loc_[^>]+>|<locate="[^"]*">|<locate=[0-9]+>|</locate>`)
	extraSpaceRe  = regexp.MustCompile(`\n{3,}`)
	trailingSpace = regexp.MustCompile(`[ \t]+\n`)
	// Lower-case snake_case tag names only, so text such as "a<b" or "<5" survives.
	leftoverTagRe = regexp.MustCompile(`</?[a-z][a-z_0-9]*>`)
)

// dedupeAdjacentBlocks removes blank-line separated blocks that repeat the
// previous block verbatim, a common artefact of greedy VLM decoding.
func dedupeAdjacentBlocks(s string) string {
	blocks := strings.Split(s, "\n\n")
	out := blocks[:0]
	prev := ""
	for _, b := range blocks {
		t := strings.TrimSpace(b)
		if t != "" && t == prev {
			continue
		}
		if t != "" {
			prev = t
		}
		out = append(out, b)
	}
	return strings.Join(out, "\n\n")
}

var headingNumberRe = regexp.MustCompile(`^(\d+(?:\.\d+)*)\.?\s`)

// numberedHeadingLevel returns the Markdown heading prefix implied by a
// section number ("1." -> "##", "2.1" -> "###"), or "## " when the text is not
// numbered. Used for <section_header> tags, which carry no level.
func numberedHeadingLevel(text string) string {
	return headingPrefix(text, "## ")
}

// headingPrefix returns the prefix implied by the section number in text, or
// fallback when the text is not numbered. Models tag every heading with the
// same level, so the number is the better signal.
func headingPrefix(text, fallback string) string {
	m := headingNumberRe.FindStringSubmatch(text)
	if m == nil {
		return fallback
	}
	depth := strings.Count(m[1], ".") + 1
	if depth > 4 {
		depth = 4
	}
	return strings.Repeat("#", depth+1) + " "
}

func wordSet(s string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		set[w] = struct{}{}
	}
	return set
}

// nearDuplicate reports whether two paragraphs share almost all their words
// (Jaccard >= 0.85, at least 12 words): a VLM re-reading the same region with
// a slightly different transcription.
func nearDuplicate(a, b map[string]struct{}) bool {
	if len(a) < 12 || len(b) < 12 {
		return false
	}
	inter := 0
	for w := range a {
		if _, ok := b[w]; ok {
			inter++
		}
	}
	return float64(inter)/float64(len(a)+len(b)-inter) >= 0.85
}

// dedupeNearDuplicateParagraphs drops plain paragraphs that near-duplicate an
// earlier one, together with a heading left orphaned directly above it.
func dedupeNearDuplicateParagraphs(s string) string {
	blocks := strings.Split(s, "\n\n")
	var out []string
	var seen []map[string]struct{}
	for _, b := range blocks {
		t := strings.TrimSpace(b)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "\x00") ||
			strings.HasPrefix(t, "|") || strings.HasPrefix(t, "-") || strings.HasPrefix(t, "```") {
			out = append(out, b)
			continue
		}
		ws := wordSet(t)
		dup := false
		for _, prev := range seen {
			if nearDuplicate(ws, prev) {
				dup = true
				break
			}
		}
		if !dup {
			seen = append(seen, ws)
			out = append(out, b)
			continue
		}
		// Drop a heading that now has no body.
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		if n := len(out); n > 0 && strings.HasPrefix(strings.TrimSpace(out[n-1]), "#") {
			out = out[:n-1]
		}
	}
	return strings.Join(out, "\n\n")
}

// DocTagsToMarkdown converts IBM Docling DocTags XML into clean Markdown.
func DocTagsToMarkdown(doctags string) string {
	if strings.TrimSpace(doctags) == "" {
		return ""
	}

	// 1. Strip spatial location coordinates
	cleaned := locRegex.ReplaceAllString(doctags, "")

	// 2. Tables first: OTSL ends its rows with <nl>, which the break
	// normalisation below would turn into plain newlines.
	// Rendered tables are parked behind placeholders: the clean-up passes
	// below strip lower-case tags, which would destroy an HTML table.
	var tables []string
	cleaned = convertOTSLTables(cleaned, &tables)

	// 3. Normalize breaks
	cleaned = strings.ReplaceAll(cleaned, "<nl>", "\n")
	cleaned = strings.ReplaceAll(cleaned, "</nl>", "\n")
	cleaned = strings.ReplaceAll(cleaned, "<br>", "\n")
	cleaned = strings.ReplaceAll(cleaned, "</br>", "\n")
	cleaned = strings.ReplaceAll(cleaned, "<pb>", "\n\n---\n\n")
	cleaned = strings.ReplaceAll(cleaned, "<page_break>", "\n\n---\n\n")

	// 4. Process headings and blocks
	cleaned = convertHeadingsAndBlocks(cleaned)

	// 5. Drop tags the model left unclosed or that have no Markdown form
	// (<page_header>, a dangling "<text>" when generation stopped early, ...)
	cleaned = leftoverTagRe.ReplaceAllString(cleaned, "")
	cleaned = dedupeAdjacentBlocks(cleaned)
	cleaned = dedupeNearDuplicateParagraphs(cleaned)

	// 6. Final clean up
	cleaned = trailingSpace.ReplaceAllString(cleaned, "\n")
	cleaned = extraSpaceRe.ReplaceAllString(cleaned, "\n\n")
	for i, tbl := range tables {
		cleaned = strings.Replace(cleaned, tablePlaceholder(i), strings.TrimSpace(tbl), 1)
	}
	return strings.TrimSpace(cleaned)
}

// tablePlaceholder is the stand-in for the i-th rendered table. The NUL bytes
// cannot occur in model output and survive every regexp pass below.
func tablePlaceholder(i int) string { return "\x00TABLE" + strconv.Itoa(i) + "\x00" }

func convertOTSLTables(text string, stash *[]string) string {
	// Pattern for OTSL or <table> blocks
	tableRegex := regexp.MustCompile(`(?s)<(?:otsl|table)>(.*?)</(?:otsl|table)>`)

	return tableRegex.ReplaceAllStringFunc(text, func(tableContent string) string {
		inner := strings.TrimPrefix(tableContent, "<otsl>")
		inner = strings.TrimPrefix(inner, "<table>")
		inner = strings.TrimSuffix(inner, "</otsl>")
		inner = strings.TrimSuffix(inner, "</table>")

		if isOTSL(inner) {
			grid, caption := parseOTSL(inner)
			md := renderOTSL(grid, caption)
			if md == "" {
				return ""
			}
			*stash = append(*stash, md)
			return "\n\n" + tablePlaceholder(len(*stash)-1) + "\n\n"
		}

		inner = strings.ReplaceAll(inner, "<nl>", "\n")
		rows := parseTableRows(inner)
		if len(rows) == 0 {
			return ""
		}

		// Find max column count
		maxCols := 0
		for _, r := range rows {
			if len(r) > maxCols {
				maxCols = len(r)
			}
		}
		if maxCols == 0 {
			return ""
		}

		var sb strings.Builder
		sb.WriteString("\n\n")

		// Render rows
		for i, row := range rows {
			// Pad row if needed
			padded := make([]string, maxCols)
			for c := 0; c < maxCols; c++ {
				if c < len(row) {
					// Clean cell content from remaining tags and pipes
					val := strings.ReplaceAll(row[c], "|", "\\|")
					val = strings.TrimSpace(stripRemainingTags(val))
					padded[c] = val
				} else {
					padded[c] = ""
				}
			}

			sb.WriteString("| ")
			sb.WriteString(strings.Join(padded, " | "))
			sb.WriteString(" |\n")

			// Add separator after first row
			if i == 0 {
				sb.WriteString("|")
				for c := 0; c < maxCols; c++ {
					sb.WriteString(" --- |")
				}
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n")
		return sb.String()
	})
}

func parseTableRows(inner string) [][]string {
	var rows [][]string

	// If explicit <trow> or <tr> tags exist
	if strings.Contains(inner, "<trow>") || strings.Contains(inner, "<tr>") {
		rowRe := regexp.MustCompile(`(?s)<(?:trow|tr)>(.*?)</(?:trow|tr)>`)
		matches := rowRe.FindAllStringSubmatch(inner, -1)
		for _, m := range matches {
			rowContent := m[1]
			cells := extractCells(rowContent)
			if len(cells) > 0 {
				rows = append(rows, cells)
			}
		}
		if len(rows) > 0 {
			return rows
		}
	}

	// Fallback: split by newline or consecutive cell streams
	lines := strings.Split(inner, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cells := extractCells(line)
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	}

	return rows
}

func extractCells(content string) []string {
	var cells []string
	cellRe := regexp.MustCompile(`(?s)<(?:chead|fcel|ecel|cel|th|td)>([^<]*)`)
	matches := cellRe.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		txt := strings.TrimSpace(m[1])
		cells = append(cells, txt)
	}
	return cells
}

func convertHeadingsAndBlocks(text string) string {
	// Headings
	text = regexp.MustCompile(`(?s)<title>(.*?)</title>`).ReplaceAllStringFunc(text, func(m string) string {
		inner := strings.TrimSpace(m[7 : len(m)-8])
		return "\n\n# " + stripRemainingTags(inner) + "\n\n"
	})

	text = regexp.MustCompile(`(?s)<section_header_level_1>(.*?)</section_header_level_1>`).ReplaceAllStringFunc(text, func(m string) string {
		inner := stripRemainingTags(strings.TrimSpace(m[24 : len(m)-25]))
		return "\n\n" + headingPrefix(inner, "## ") + inner + "\n\n"
	})

	text = regexp.MustCompile(`(?s)<section_header_level_2>(.*?)</section_header_level_2>`).ReplaceAllStringFunc(text, func(m string) string {
		inner := stripRemainingTags(strings.TrimSpace(m[24 : len(m)-25]))
		return "\n\n" + headingPrefix(inner, "### ") + inner + "\n\n"
	})

	text = regexp.MustCompile(`(?s)<section_header_level_3>(.*?)</section_header_level_3>`).ReplaceAllStringFunc(text, func(m string) string {
		inner := stripRemainingTags(strings.TrimSpace(m[24 : len(m)-25]))
		return "\n\n" + headingPrefix(inner, "#### ") + inner + "\n\n"
	})

	text = regexp.MustCompile(`(?s)<section_header>(.*?)</section_header>`).ReplaceAllStringFunc(text, func(m string) string {
		inner := stripRemainingTags(strings.TrimSpace(m[16 : len(m)-17]))
		return "\n\n" + numberedHeadingLevel(inner) + inner + "\n\n"
	})

	// Code blocks
	text = regexp.MustCompile(`(?s)<(?:code_block|code)>(.*?)</(?:code_block|code)>`).ReplaceAllStringFunc(text, func(m string) string {
		re := regexp.MustCompile(`(?s)<(?:code_block|code)>(.*?)</(?:code_block|code)>`)
		matches := re.FindStringSubmatch(m)
		if len(matches) > 1 {
			return "\n\n```\n" + strings.TrimSpace(stripRemainingTags(matches[1])) + "\n```\n\n"
		}
		return m
	})

	// Lists
	text = regexp.MustCompile(`(?s)<(?:list_item|li)>([^<]*)(?:</(?:list_item|li)>)?`).ReplaceAllStringFunc(text, func(m string) string {
		re := regexp.MustCompile(`(?s)<(?:list_item|li)>([^<]*)`)
		matches := re.FindStringSubmatch(m)
		if len(matches) > 1 {
			val := strings.TrimSpace(stripRemainingTags(matches[1]))
			if val != "" {
				return "\n- " + val
			}
		}
		return ""
	})

	// Pictures / Diagrams
	text = regexp.MustCompile(`(?s)<picture>(.*?)</picture>`).ReplaceAllString(text, "\n\n![Figure](figure)\n\n")

	// Formulas
	text = regexp.MustCompile(`(?s)<formula>(.*?)</formula>`).ReplaceAllStringFunc(text, func(m string) string {
		inner := strings.TrimSpace(m[9 : len(m)-10])
		return "\n\n$$ " + stripRemainingTags(inner) + " $$\n\n"
	})

	// Paragraphs / Text
	text = regexp.MustCompile(`(?s)<(?:paragraph|text)>(.*?)</(?:paragraph|text)>`).ReplaceAllStringFunc(text, func(m string) string {
		re := regexp.MustCompile(`(?s)<(?:paragraph|text)>(.*?)</(?:paragraph|text)>`)
		matches := re.FindStringSubmatch(m)
		if len(matches) > 1 {
			val := strings.TrimSpace(stripRemainingTags(matches[1]))
			if val != "" {
				return "\n\n" + val + "\n\n"
			}
		}
		return ""
	})

	// Strip wrapper <doctag> tags
	text = strings.ReplaceAll(text, "<doctag>", "")
	text = strings.ReplaceAll(text, "</doctag>", "")

	return text
}

func stripRemainingTags(s string) string {
	tagRe := regexp.MustCompile(`<[^>]+>`)
	return tagRe.ReplaceAllString(s, "")
}
