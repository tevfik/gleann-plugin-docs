package engine

import (
	"testing"
	"unicode/utf8"
)

func TestCleanVLMTextDropsInvalidUTF8(t *testing.T) {
	cases := map[string]string{
		"SADAKAT: G\xc4\xb1m\xc3\xbcl\xc3\xbc": "SADAKAT: G\u0131m\u00fcl\u00fc", // valid Turkish stays as is
		"G\xc4m\xc3\xbcl\xc3\xbc":              "Gm\u00fcl\u00fc",                // truncated sequence (seen on a real page)
		"<text>\x80abc</text>":                 "<text>abc</text>",               // lone continuation byte
	}
	for in, want := range cases {
		got := cleanVLMText(in)
		if !utf8.ValidString(got) || got != want {
			t.Errorf("cleanVLMText(%q) = %q, want %q", in, got, want)
		}
	}
}
