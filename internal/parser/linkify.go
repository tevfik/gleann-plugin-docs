package parser

import (
	"regexp"
	"strings"
)

// linkifyPattern matches code blocks, inline code, existing markdown links,
// HTML tags, and bare URLs (http://, https://, www.).
var linkifyPattern = regexp.MustCompile("(?s)```.*?```|`[^`\n]+`|!?\\[[^\\]]*\\]\\([^)]+\\)|<[^>]+>|\\b(?:https?://|www\\.)[a-zA-Z0-9._/~:?#\\[\\]@!$&'()*+,;=-]+")

// LinkifyURLs converts bare URLs and www. links in markdown into proper markdown links,
// while leaving existing markdown links, inline code, code fences, and HTML tags untouched.
func LinkifyURLs(markdown string) string {
	return linkifyPattern.ReplaceAllStringFunc(markdown, func(match string) string {
		// Preserve code blocks, inline code, existing markdown links, and HTML tags
		if strings.HasPrefix(match, "```") ||
			strings.HasPrefix(match, "`") ||
			strings.HasPrefix(match, "[") ||
			strings.HasPrefix(match, "![") ||
			strings.HasPrefix(match, "<") {
			return match
		}

		cleanURL, trailing := trimTrailingPunctuation(match)
		if strings.HasPrefix(cleanURL, "www.") {
			return "[" + cleanURL + "](https://" + cleanURL + ")" + trailing
		}
		if strings.HasPrefix(cleanURL, "http://") || strings.HasPrefix(cleanURL, "https://") {
			return "[" + cleanURL + "](" + cleanURL + ")" + trailing
		}

		return match
	})
}

// trimTrailingPunctuation strips trailing punctuation marks that often trail a URL
// at the end of a sentence or parentheses.
func trimTrailingPunctuation(url string) (string, string) {
	var stripped []byte
	for len(url) > 0 {
		last := url[len(url)-1]
		if last == '.' || last == ',' || last == '!' || last == '?' || last == ':' || last == ';' || last == '\'' || last == '"' {
			stripped = append(stripped, last)
			url = url[:len(url)-1]
			continue
		}
		// Trailing ')' only stripped if unbalanced with '(' in the URL
		if last == ')' && strings.Count(url, ")") > strings.Count(url, "(") {
			stripped = append(stripped, last)
			url = url[:len(url)-1]
			continue
		}
		break
	}

	// Reverse stripped back to original order
	for i, j := 0, len(stripped)-1; i < j; i, j = i+1, j-1 {
		stripped[i], stripped[j] = stripped[j], stripped[i]
	}

	return url, string(stripped)
}
