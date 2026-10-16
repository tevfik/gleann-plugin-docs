package docparser

import (
	"regexp"
	"strings"
	"unicode"
)

var mdNoise = regexp.MustCompile("[#|*_`>-]+")

// NormalizeText lower-cases s, drops Markdown decoration and collapses
// whitespace, so OCR output and a PDF text layer can be compared.
func NormalizeText(s string) string {
	s = mdNoise.ReplaceAllString(s, " ")
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return unicode.ToLower(r)
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// CER is the character error rate of hyp against ref: Levenshtein distance
// divided by len(ref), on the normalised texts. Reading order matters.
func CER(ref, hyp string) float64 {
	r, h := []rune(NormalizeText(ref)), []rune(NormalizeText(hyp))
	if len(r) == 0 {
		if len(h) == 0 {
			return 0
		}
		return 1
	}
	prev := make([]int, len(h)+1)
	cur := make([]int, len(h)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(r); i++ {
		cur[0] = i
		for j := 1; j <= len(h); j++ {
			cost := 1
			if r[i-1] == h[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return float64(prev[len(h)]) / float64(len(r))
}

// WordScores compares the multisets of words in ref and hyp (order-insensitive,
// so a different reading order does not count against the parser). Recall is
// the share of reference words found, precision the share of hypothesis words
// that are correct.
func WordScores(ref, hyp string) (recall, precision float64) {
	rw, hw := strings.Fields(NormalizeText(ref)), strings.Fields(NormalizeText(hyp))
	if len(rw) == 0 || len(hw) == 0 {
		return 0, 0
	}
	count := make(map[string]int, len(rw))
	for _, w := range rw {
		count[w]++
	}
	match := 0
	for _, w := range hw {
		if count[w] > 0 {
			count[w]--
			match++
		}
	}
	return float64(match) / float64(len(rw)), float64(match) / float64(len(hw))
}

var turkishFold = strings.NewReplacer(
	"ı", "i", "İ", "i", "I", "i", "ş", "s", "Ş", "s", "ğ", "g", "Ğ", "g",
	"ç", "c", "Ç", "c", "ö", "o", "Ö", "o", "ü", "u", "Ü", "u",
)

// FoldDiacritics maps Turkish letters to their ASCII look-alikes. Comparing
// folded texts separates "the wrong dotted/dotless letter" from real misreads.
func FoldDiacritics(s string) string {
	return turkishFold.Replace(NormalizeText(s))
}
