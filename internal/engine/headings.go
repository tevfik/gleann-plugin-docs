package engine

import (
	"regexp"
	"strings"
	"unicode"
)

var unitWord = regexp.MustCompile(`^(?i:[kmgt]?b(ps|it|its|yte|ytes)?|[kmg]?hz|[mµu]?[swa]|[mµnk]?(v|f|h|m|g)|fps|db|dpi|rpm|kwh|wh|mah|ah)$`)

// tocLeader matches a table-of-contents entry ("1 Overview ..... 4").
var tocLeader = regexp.MustCompile(`\.{4,}|(?:\. ){4,}`)

var numberedHeadingLine = regexp.MustCompile(`^(\d{1,2}(?:\.\d{1,2}){0,3})\.?\s+(\S.*)$`)

// numberedHeading parses a numbered section title ("2.1 Test Corpus") and
// returns its depth (1 for "1.", 2 for "2.1", ...).
func numberedHeading(line string) (depth int, ok bool) {
	line = strings.TrimSpace(line)
	m := numberedHeadingLine.FindStringSubmatch(line)
	if m == nil || len(strings.Fields(line)) > 10 {
		return 0, false
	}
	if tocLeader.MatchString(m[2]) {
		return 0, false
	}
	first := []rune(m[2])[0]
	if !unicode.IsUpper(first) {
		return 0, false
	}
	// A quantity with a unit ("14 KB", "3 V", "2 MHz") is a table cell or a
	// caption fragment, not a section title: a one-word title that short or
	// an all-caps/unit token is rejected.
	if words := strings.Fields(m[2]); len(words) == 1 && (len([]rune(words[0])) <= 3 || unitWord.MatchString(words[0])) {
		return 0, false
	}
	if strings.ContainsAny(line[len(line)-1:], ".,;:!?)") {
		return 0, false
	}
	return strings.Count(m[1], ".") + 1, true
}

// promoteNumberedHeadings turns numbered section titles in a text layer
// (which carries no styling) into Markdown headings so downstream chunking can
// build the section hierarchy. A line qualifies when it looks like a short
// title and follows a blank line, another heading, or a finished sentence;
// consecutive top-level numbered lines are left alone because they are
// usually a numbered list.
func promoteNumberedHeadings(text string) string {
	lines := strings.Split(text, "\n")
	prevIsBoundary := true
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			prevIsBoundary = true
			continue
		}
		if d, ok := numberedHeading(t); ok && prevIsBoundary {
			list := false
			if d == 1 {
				for j := i + 1; j < len(lines); j++ {
					if strings.TrimSpace(lines[j]) == "" {
						continue
					}
					if d2, ok2 := numberedHeading(lines[j]); ok2 && d2 == 1 {
						list = true
					}
					break
				}
			}
			if !list {
				lines[i] = strings.Repeat("#", d+1) + " " + t
				prevIsBoundary = true
				continue
			}
		}
		prevIsBoundary = strings.ContainsAny(t[len(t)-1:], ".!?")
	}
	return strings.Join(lines, "\n")
}
