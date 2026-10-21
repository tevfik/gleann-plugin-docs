package parser

import (
	"strings"
	"testing"
)

func TestDocTagsToMarkdown_Empty(t *testing.T) {
	if got := DocTagsToMarkdown(""); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestDocTagsToMarkdown_StripLocations(t *testing.T) {
	input := `<doctag><title><loc_12><loc_34>Financial Report 2026</title><paragraph><loc_55>Net income grew by 20%.</paragraph></doctag>`
	got := DocTagsToMarkdown(input)

	if !strings.Contains(got, "# Financial Report 2026") {
		t.Errorf("expected '# Financial Report 2026', got:\n%s", got)
	}
	if !strings.Contains(got, "Net income grew by 20%.") {
		t.Errorf("expected paragraph content, got:\n%s", got)
	}
	if strings.Contains(got, "loc_") {
		t.Errorf("expected no loc tags in output, got:\n%s", got)
	}
}

func TestDocTagsToMarkdown_HeadingsHierarchy(t *testing.T) {
	input := `<doctag>
<title>Doc Title</title>
<section_header_level_1>Chapter 1</section_header_level_1>
<paragraph>Intro paragraph</paragraph>
<section_header_level_2>Section 1.1</section_header_level_2>
<paragraph>Section details</paragraph>
</doctag>`

	got := DocTagsToMarkdown(input)
	if !strings.Contains(got, "# Doc Title") {
		t.Errorf("missing H1: %s", got)
	}
	if !strings.Contains(got, "## Chapter 1") {
		t.Errorf("missing H2: %s", got)
	}
	if !strings.Contains(got, "### Section 1.1") {
		t.Errorf("missing H3: %s", got)
	}
}

func TestDocTagsToMarkdown_OTSLTable(t *testing.T) {
	input := `<doctag>
<otsl>
<trow><chead>Product</chead><chead>Price</chead><chead>Stock</chead></trow>
<trow><fcel>Accelerator EIF</fcel><fcel>$199</fcel><fcel>50</fcel></trow>
<trow><fcel>Edge TPU</fcel><fcel>$79</fcel><fcel>120</fcel></trow>
</otsl>
</doctag>`

	got := DocTagsToMarkdown(input)

	expectedRows := []string{
		"| Product | Price | Stock |",
		"| --- | --- | --- |",
		"| Accelerator EIF | $199 | 50 |",
		"| Edge TPU | $79 | 120 |",
	}

	for _, expected := range expectedRows {
		if !strings.Contains(got, expected) {
			t.Errorf("expected table row %q, got:\n%s", expected, got)
		}
	}
}

func TestDocTagsToMarkdown_CodeAndLists(t *testing.T) {
	input := `<doctag>
<paragraph>Here is some code:</paragraph>
<code>def hello():
    print("world")</code>
<paragraph>List of items:</paragraph>
<li>Item 1</li>
<li>Item 2</li>
</doctag>`

	got := DocTagsToMarkdown(input)

	if !strings.Contains(got, "```\ndef hello():\n    print(\"world\")\n```") {
		t.Errorf("code block missing: %s", got)
	}
	if !strings.Contains(got, "- Item 1") || !strings.Contains(got, "- Item 2") {
		t.Errorf("list items missing: %s", got)
	}
}

func TestDocTagsToMarkdown_NearDuplicateSectionDropped(t *testing.T) {
	in := "<doctag><section_header>2.1 Test Corpus</section_header><text>The test corpus consists of documents with known structure: headings at multiple levels, paragraphs with technical content, tables with numerical data, and inline formatting.</text>" +
		"<section_header>1. Introduction</section_header><text>The test corpus consists of documents with knowledge structure: headings at multiple levels, paragraphs with technical content, tables with numerical data, and inline formatting.</text>" +
		"<section_header>2.2 Metrics</section_header><text>We measure detection.</text></doctag>"
	got := DocTagsToMarkdown(in)
	if strings.Count(got, "test corpus consists") != 1 {
		t.Fatalf("near-duplicate paragraph kept:\n%s", got)
	}
	if strings.Contains(got, "1. Introduction") {
		t.Fatalf("orphan heading kept:\n%s", got)
	}
	if !strings.Contains(got, "### 2.1 Test Corpus") || !strings.Contains(got, "### 2.2 Metrics") {
		t.Fatalf("numbered headings should be level 3:\n%s", got)
	}
}

func TestNumberedHeadingLevel(t *testing.T) {
	for in, want := range map[string]string{"1. Intro": "## ", "2.1 Corpus": "### ", "3.2.1 X": "#### ", "Abstract": "## ", "2024 results": "## "} {
		if got := numberedHeadingLevel(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}

func TestDocTagsToMarkdown_NumberOverridesTaggedLevel(t *testing.T) {
	in := "<section_header_level_1>2. Methodology</section_header_level_1><section_header_level_1>2.1 Test Corpus</section_header_level_1><section_header_level_1>Appendix</section_header_level_1>"
	got := DocTagsToMarkdown(in)
	for _, want := range []string{"## 2. Methodology", "### 2.1 Test Corpus", "## Appendix"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}
