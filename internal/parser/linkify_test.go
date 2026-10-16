package parser

import (
	"testing"
)

func TestLinkifyURLs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "bare https url",
			input:    "Visit https://github.com for code.",
			expected: "Visit [https://github.com](https://github.com) for code.",
		},
		{
			name:     "bare www url",
			input:    "Check www.example.com for more info.",
			expected: "Check [www.example.com](https://www.example.com) for more info.",
		},
		{
			name:     "url with trailing comma and question",
			input:    "Have you seen https://gleann.ai? Also check https://test.org, right?",
			expected: "Have you seen [https://gleann.ai](https://gleann.ai)? Also check [https://test.org](https://test.org), right?",
		},
		{
			name:     "url inside parentheses",
			input:    "Source code available here (https://github.com/tevfik/bitllm.c).",
			expected: "Source code available here ([https://github.com/tevfik/bitllm.c](https://github.com/tevfik/bitllm.c)).",
		},
		{
			name:     "wikipedia url with parentheses inside",
			input:    "Read https://en.wikipedia.org/wiki/Go_(programming_language) for info.",
			expected: "Read [https://en.wikipedia.org/wiki/Go_(programming_language)](https://en.wikipedia.org/wiki/Go_(programming_language)) for info.",
		},
		{
			name:     "preserve existing markdown link",
			input:    "Already linked [My Project](https://example.com/project) and [https://direct.link](https://direct.link).",
			expected: "Already linked [My Project](https://example.com/project) and [https://direct.link](https://direct.link).",
		},
		{
			name:     "preserve inline code and code block",
			input:    "Run `curl https://api.site.com` or:\n```bash\ncurl https://other.com\n```\nDone.",
			expected: "Run `curl https://api.site.com` or:\n```bash\ncurl https://other.com\n```\nDone.",
		},
		{
			name:     "preserve html tags",
			input:    `<a href="https://example.com">Site</a>`,
			expected: `<a href="https://example.com">Site</a>`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LinkifyURLs(tc.input)
			if got != tc.expected {
				t.Errorf("\nInput:    %q\nExpected: %q\nGot:      %q", tc.input, tc.expected, got)
			}
		})
	}
}
