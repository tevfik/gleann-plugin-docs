package parser

import (
	"strings"
	"testing"
)

func TestParseDocument_Empty(t *testing.T) {
	res := ParseDocument("", "test.md", "md", 0)
	if len(res.Nodes) != 2 {
		t.Fatalf("expected 2 nodes (Document + Section), got %d", len(res.Nodes))
	}
	if res.Nodes[0].Type != "Document" {
		t.Errorf("expected node 0 to be Document, got %s", res.Nodes[0].Type)
	}
	if res.Nodes[1].Type != "Section" {
		t.Errorf("expected node 1 to be Section, got %s", res.Nodes[1].Type)
	}
	if len(res.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(res.Edges))
	}
	if res.Edges[0].Type != "HAS_SECTION" {
		t.Errorf("expected HAS_SECTION edge, got %s", res.Edges[0].Type)
	}
}

func TestParseDocument_NoHeadings(t *testing.T) {
	md := "This is a plain text document.\n\nWith some paragraphs."
	res := ParseDocument(md, "plain.txt", "txt", 1)

	var doc, sec *Node
	for i := range res.Nodes {
		if res.Nodes[i].Type == "Document" {
			doc = &res.Nodes[i]
		}
		if res.Nodes[i].Type == "Section" {
			sec = &res.Nodes[i]
		}
	}
	if doc == nil || sec == nil {
		t.Fatalf("expected both Document and Section nodes")
	}

	if sec.Data["level"].(int) != 0 {
		t.Errorf("expected section level 0, got %v", sec.Data["level"])
	}
	if !strings.Contains(sec.Data["content"].(string), "plain text") {
		t.Errorf("expected content in section")
	}
	if len(res.Edges) != 1 || res.Edges[0].Type != "HAS_SECTION" {
		t.Errorf("expected HAS_SECTION edge")
	}
}

func TestParseDocument_Hierarchy(t *testing.T) {
	md := `# Chapter 1
Content 1

## Section 1.1
Content 1.1

### Section 1.1.1
Content 1.1.1

# Chapter 2
Content 2`

	res := ParseDocument(md, "book.md", "md", 10)

	var sections []Node
	for _, n := range res.Nodes {
		if n.Type == "Section" {
			sections = append(sections, n)
		}
	}

	if len(sections) != 4 {
		t.Fatalf("expected 4 sections, got %d", len(sections))
	}

	var hasSection, hasSubsection []Edge
	for _, e := range res.Edges {
		if e.Type == "HAS_SECTION" {
			hasSection = append(hasSection, e)
		} else if e.Type == "HAS_SUBSECTION" {
			hasSubsection = append(hasSubsection, e)
		}
	}

	if len(hasSection) != 2 {
		t.Errorf("expected 2 root HAS_SECTION edges (Chapter 1, Chapter 2), got %d", len(hasSection))
	}
	if len(hasSubsection) != 2 {
		t.Errorf("expected 2 HAS_SUBSECTION edges (1.1, 1.1.1), got %d", len(hasSubsection))
	}
}

func TestParseDocument_FlatHeadingsNumbered(t *testing.T) {
	// Docling sometimes emits all headings as ##, but numbered like 1, 2.1, 2.1.1
	md := `## 1 Introduction
Intro text

## 2 System Overview
Overview text

## 2.1 Hardware Architecture
HW text

## 2.1.1 Accelerator Core
Core text`

	res := ParseDocument(md, "doc.pdf", "pdf", 4)

	var sections []Node
	for _, n := range res.Nodes {
		if n.Type == "Section" {
			sections = append(sections, n)
		}
	}
	if len(sections) != 4 {
		t.Fatalf("expected 4 sections, got %d", len(sections))
	}

	// Levels should be inferred: 1 -> 1, 2 -> 1, 2.1 -> 2, 2.1.1 -> 3
	if sections[0].Data["level"].(int) != 1 {
		t.Errorf("expected level 1 for '1 Introduction', got %v", sections[0].Data["level"])
	}
	if sections[1].Data["level"].(int) != 1 {
		t.Errorf("expected level 1 for '2 System Overview', got %v", sections[1].Data["level"])
	}
	if sections[2].Data["level"].(int) != 2 {
		t.Errorf("expected level 2 for '2.1 Hardware Architecture', got %v", sections[2].Data["level"])
	}
	if sections[3].Data["level"].(int) != 3 {
		t.Errorf("expected level 3 for '2.1.1 Accelerator Core', got %v", sections[3].Data["level"])
	}
}

func TestParseDocument_FiguresAndTables(t *testing.T) {
	md := `## 3 Performance
Performance text

## Figure 4. Throughput vs Latency
Figure details`

	res := ParseDocument(md, "perf.md", "md", 1)
	var sections []Node
	for _, n := range res.Nodes {
		if n.Type == "Section" {
			sections = append(sections, n)
		}
	}

	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	// Figure 4 should be a subsection of section 3
	var hasSub []Edge
	for _, e := range res.Edges {
		if e.Type == "HAS_SUBSECTION" {
			hasSub = append(hasSub, e)
		}
	}
	if len(hasSub) != 1 {
		t.Errorf("expected 1 HAS_SUBSECTION edge attaching Figure to Section, got %d", len(hasSub))
	}
}
