"""Tests for section_parser — graph-ready document parsing."""

import pytest
from section_parser import parse_document, Node, Edge, PluginResult


class TestParseDocumentBasic:
    """Basic document parsing tests."""

    def test_empty_document(self):
        """Empty markdown produces a Document node and one implicit section."""
        result = parse_document("", "test.md", "md")
        assert isinstance(result, PluginResult)
        # Should have Document + 1 implicit section
        assert len(result.nodes) == 2
        assert result.nodes[0].type == "Document"
        assert result.nodes[1].type == "Section"

    def test_no_headings_single_section(self):
        """Document without headings → 1 Document + 1 implicit Section."""
        md = "This is a plain text document.\n\nWith some paragraphs."
        result = parse_document(md, "plain.txt", "txt")

        docs = [n for n in result.nodes if n.type == "Document"]
        sections = [n for n in result.nodes if n.type == "Section"]

        assert len(docs) == 1
        assert len(sections) == 1
        assert sections[0].data["level"] == 0
        assert "plain text" in sections[0].data["content"]

        # Should have one HAS_SECTION edge
        assert len(result.edges) == 1
        assert result.edges[0].type == "HAS_SECTION"

    def test_document_node_fields(self):
        """Document node has all required fields."""
        md = "# My Report\n\nSome content here."
        result = parse_document(md, "report.pdf", "pdf", page_count=5)

        doc = result.nodes[0]
        assert doc.type == "Document"
        assert doc.data["path"] == "report.pdf"
        assert doc.data["title"] == "My Report"
        assert doc.data["format"] == "pdf"
        assert doc.data["page_count"] == 5
        assert doc.data["word_count"] > 0
        assert isinstance(doc.data["summary"], str)

    def test_single_heading(self):
        """Single H1 heading → 1 Document + 1 Section + 1 HAS_SECTION edge."""
        md = "# Introduction\n\nWelcome to the report."
        result = parse_document(md, "doc.md", "md")

        docs = [n for n in result.nodes if n.type == "Document"]
        sections = [n for n in result.nodes if n.type == "Section"]

        assert len(docs) == 1
        assert len(sections) == 1
        assert sections[0].data["heading"] == "Introduction"
        assert sections[0].data["level"] == 1
        assert "Welcome" in sections[0].data["content"]
        assert sections[0].data["doc_path"] == "doc.md"

        # Edge: Document → Section
        assert len(result.edges) == 1
        assert result.edges[0].type == "HAS_SECTION"
        assert result.edges[0].from_id == "doc.md"


class TestParseDocumentHierarchy:
    """Tests for section hierarchy and edge generation."""

    def test_two_h1_sections(self):
        """Two H1 sections → 2 HAS_SECTION edges, no subsections."""
        md = "# Chapter 1\n\nContent one.\n\n# Chapter 2\n\nContent two."
        result = parse_document(md, "book.md", "md")

        sections = [n for n in result.nodes if n.type == "Section"]
        assert len(sections) == 2
        assert sections[0].data["heading"] == "Chapter 1"
        assert sections[1].data["heading"] == "Chapter 2"

        has_section = [e for e in result.edges if e.type == "HAS_SECTION"]
        has_subsection = [e for e in result.edges if e.type == "HAS_SUBSECTION"]
        assert len(has_section) == 2
        assert len(has_subsection) == 0

    def test_nested_sections(self):
        """H1 → H2 → H3 hierarchy produces subsection edges."""
        md = (
            "# Introduction\n\nIntro text.\n\n"
            "## Background\n\nBg text.\n\n"
            "### Details\n\nDetail text."
        )
        result = parse_document(md, "nested.md", "md")

        sections = [n for n in result.nodes if n.type == "Section"]
        assert len(sections) == 3

        has_section = [e for e in result.edges if e.type == "HAS_SECTION"]
        has_subsection = [e for e in result.edges if e.type == "HAS_SUBSECTION"]

        # Only the H1 is a direct child of Document
        assert len(has_section) == 1
        assert has_section[0].from_id == "nested.md"

        # H2 is child of H1, H3 is child of H2
        assert len(has_subsection) == 2

    def test_section_ids_are_globally_unique(self):
        """Section IDs include the document path for global uniqueness."""
        md = "# Title\n\nContent.\n\n## Sub\n\nMore."
        result = parse_document(md, "docs/report.pdf", "pdf")

        sections = [n for n in result.nodes if n.type == "Section"]
        for sec in sections:
            assert sec.data["id"].startswith("doc:docs/report.pdf:")

    def test_section_id_format(self):
        """Section IDs follow doc:<path>:s<local_id> pattern."""
        md = (
            "# A\n\nText A.\n\n"
            "## A1\n\nText A1.\n\n"
            "## A2\n\nText A2.\n\n"
            "# B\n\nText B."
        )
        result = parse_document(md, "f.md", "md")

        sections = [n for n in result.nodes if n.type == "Section"]
        ids = [s.data["id"] for s in sections]

        # Root sections: s0, s1
        assert "doc:f.md:s0" in ids
        assert "doc:f.md:s1" in ids
        # Children of s0: s0.0, s0.1
        assert "doc:f.md:s0.0" in ids
        assert "doc:f.md:s0.1" in ids

    def test_mixed_heading_levels(self):
        """H1 → H3 (skipping H2) still creates proper hierarchy."""
        md = "# Title\n\nText.\n\n### Deep\n\nDeep text."
        result = parse_document(md, "skip.md", "md")

        sections = [n for n in result.nodes if n.type == "Section"]
        assert len(sections) == 2

        # H3 should be a subsection of H1 (parent by level, not by heading number)
        has_subsection = [e for e in result.edges if e.type == "HAS_SUBSECTION"]
        assert len(has_subsection) == 1


class TestParseDocumentContent:
    """Tests for content extraction and metadata."""

    def test_section_content_excludes_heading(self):
        """Section content should not include the heading line itself."""
        md = "# Title\n\nParagraph one.\n\nParagraph two."
        result = parse_document(md, "t.md", "md")

        sections = [n for n in result.nodes if n.type == "Section"]
        content = sections[0].data["content"]
        assert not content.startswith("#")
        assert "Paragraph one" in content

    def test_section_content_between_headings(self):
        """Content between two headings is correctly assigned."""
        md = "# A\n\nContent A.\n\n# B\n\nContent B."
        result = parse_document(md, "t.md", "md")

        sections = [n for n in result.nodes if n.type == "Section"]
        assert "Content A" in sections[0].data["content"]
        assert "Content B" in sections[1].data["content"]
        assert "Content B" not in sections[0].data["content"]

    def test_summary_extraction(self):
        """Summaries are extracted from first non-heading paragraph."""
        md = "# Title\n\nThis is the summary paragraph.\n\n## Details\n\nMore details here."
        result = parse_document(md, "t.md", "md")

        doc = result.nodes[0]
        assert "summary paragraph" in doc.data["summary"]

    def test_title_inference_h1(self):
        """Title is inferred from first H1 heading."""
        md = "## Sub\n\nText.\n\n# Main Title\n\nContent."
        result = parse_document(md, "t.md", "md")

        doc = result.nodes[0]
        assert doc.data["title"] == "Main Title"

    def test_title_inference_fallback(self):
        """If no H1, title falls back to first heading."""
        md = "## Only H2\n\nContent."
        result = parse_document(md, "t.md", "md")

        doc = result.nodes[0]
        assert doc.data["title"] == "Only H2"

    def test_doc_path_in_sections(self):
        """All sections carry the doc_path field."""
        md = "# A\n\nText.\n\n## B\n\nMore."
        result = parse_document(md, "report.pdf", "pdf")

        sections = [n for n in result.nodes if n.type == "Section"]
        for sec in sections:
            assert sec.data["doc_path"] == "report.pdf"


class TestPluginResultSerialization:
    """Tests for the to_dict() serialization."""

    def test_to_dict_structure(self):
        """to_dict() produces the expected JSON structure."""
        md = "# Title\n\nContent.\n\n## Sub\n\nMore."
        result = parse_document(md, "t.md", "md")
        d = result.to_dict()

        assert "nodes" in d
        assert "edges" in d
        assert isinstance(d["nodes"], list)
        assert isinstance(d["edges"], list)

    def test_node_serialization(self):
        """Nodes serialize with _type field and all data fields."""
        md = "# Hello\n\nWorld."
        result = parse_document(md, "t.md", "md")
        d = result.to_dict()

        doc_node = d["nodes"][0]
        assert doc_node["_type"] == "Document"
        assert "path" in doc_node
        assert "title" in doc_node

        sec_node = d["nodes"][1]
        assert sec_node["_type"] == "Section"
        assert "id" in sec_node
        assert "heading" in sec_node
        assert "content" in sec_node

    def test_edge_serialization(self):
        """Edges serialize with _type, from, to fields."""
        md = "# A\n\nText.\n\n## B\n\nMore."
        result = parse_document(md, "t.md", "md")
        d = result.to_dict()

        for edge in d["edges"]:
            assert "_type" in edge
            assert "from" in edge
            assert "to" in edge

    def test_no_extra_fields_in_serialization(self):
        """Serialized nodes should not leak internal fields like 'type'."""
        node = Node(type="Document", data={"path": "x", "title": "y"})
        d = node.to_dict()
        # Should have _type, path, title — not 'type'
        assert "_type" in d
        assert "type" not in d


class TestEdgeCases:
    """Edge cases and robustness tests."""

    def test_empty_sections(self):
        """Heading with no content produces empty content field."""
        md = "# A\n\n# B\n\nSome text."
        result = parse_document(md, "t.md", "md")

        sections = [n for n in result.nodes if n.type == "Section"]
        # First section has empty content (next heading follows immediately)
        assert sections[0].data["content"] == ""

    def test_many_heading_levels(self):
        """All heading levels 1-6 are parsed correctly."""
        md = "\n".join(
            f"{'#' * i} Level {i}\n\nContent {i}."
            for i in range(1, 7)
        )
        result = parse_document(md, "t.md", "md")

        sections = [n for n in result.nodes if n.type == "Section"]
        assert len(sections) == 6
        for i, sec in enumerate(sections):
            assert sec.data["level"] == i + 1

    def test_special_characters_in_heading(self):
        """Headings with special characters are handled."""
        md = "# Hello & World <Test>\n\nContent."
        result = parse_document(md, "t.md", "md")

        sections = [n for n in result.nodes if n.type == "Section"]
        assert sections[0].data["heading"] == "Hello & World <Test>"

    def test_large_document(self):
        """Large document with many sections doesn't crash."""
        parts = [f"# Section {i}\n\nContent for section {i}.\n" for i in range(100)]
        md = "\n".join(parts)
        result = parse_document(md, "big.md", "md")

        sections = [n for n in result.nodes if n.type == "Section"]
        assert len(sections) == 100
        assert len(result.edges) == 100  # all HAS_SECTION (root level)

    def test_word_count(self):
        """Word count is calculated from the full markdown."""
        md = "# Title\n\nOne two three four five."
        result = parse_document(md, "t.md", "md")
        doc = result.nodes[0]
        assert doc.data["word_count"] >= 6  # "# Title One two three four five."
