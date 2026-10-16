package docparser

import (
	"math"
	"testing"
)

func TestNormalizeText(t *testing.T) {
	got := NormalizeText("## Hello,  WORLD\n\n- item | cell *x*")
	if got != "hello, world item cell x" {
		t.Errorf("got %q", got)
	}
}

func TestCER(t *testing.T) {
	if got := CER("same text", "same text"); got != 0 {
		t.Errorf("identical: %v", got)
	}
	// One substitution in 8 characters.
	if got := CER("abcdefgh", "abcdefgx"); math.Abs(got-0.125) > 1e-9 {
		t.Errorf("one substitution: %v", got)
	}
	// Markdown decoration and case do not count as errors.
	if got := CER("Hello World", "## hello   world"); got != 0 {
		t.Errorf("normalisation: %v", got)
	}
	if got := CER("", ""); got != 0 {
		t.Errorf("both empty: %v", got)
	}
	if got := CER("", "x"); got != 1 {
		t.Errorf("empty reference: %v", got)
	}
	if got := CER("abcd", ""); got != 1 {
		t.Errorf("empty hypothesis: %v", got)
	}
}

func TestWordScores(t *testing.T) {
	recall, precision := WordScores("a b c d", "d c x")
	if math.Abs(recall-0.5) > 1e-9 || math.Abs(precision-2.0/3) > 1e-9 {
		t.Errorf("recall=%v precision=%v", recall, precision)
	}
	// Multiset semantics: a repeated word is not matched twice.
	if recall, _ := WordScores("a b", "a a a"); math.Abs(recall-0.5) > 1e-9 {
		t.Errorf("repeats: %v", recall)
	}
	if r, p := WordScores("", "x"); r != 0 || p != 0 {
		t.Errorf("empty: %v %v", r, p)
	}
}

func TestFoldDiacritics(t *testing.T) {
	if got := FoldDiacritics("Çok güzel şehirde yaşıyoruz, İstanbul'da ığdır"); got != "cok guzel sehirde yasiyoruz, istanbul'da igdir" {
		t.Errorf("got %q", got)
	}
}
