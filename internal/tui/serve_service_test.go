package tui

import (
	"strings"
	"testing"
)

func TestServeServiceQuoting(t *testing.T) {
	if got := quoteSystemd("plain"); got != "plain" {
		t.Errorf("quoteSystemd(plain) = %q", got)
	}
	if got := quoteSystemd(`/home/a b/bin "x"`); got != `"/home/a b/bin \"x\""` {
		t.Errorf("quoteSystemd = %q", got)
	}
	if got := xmlEscape("a&b<c>"); got != "a&amp;b&lt;c&gt;" {
		t.Errorf("xmlEscape = %q", got)
	}
}

func TestServeMenuItemIsToggle(t *testing.T) {
	it := serveMenuItem(DefaultServePort)
	if !strings.HasPrefix(it.title, "Serve (") {
		t.Errorf("serve item title %q should show Enable/Disable", it.title)
	}
}
