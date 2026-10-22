package parser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	headingRe     = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	numberedRe    = regexp.MustCompile(`^(\d+(?:\.\d+)*)\s+`)
	figureTableRe = regexp.MustCompile(`^(?i)(Figure|Table)\s+\d+`)
)

// Node represents a graph node for KuzuDB.
type Node struct {
	Type string         `json:"_type"`
	Data map[string]any `json:"-"`
}

func (n Node) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(n.Data)+1)
	for k, v := range n.Data {
		out[k] = v
	}
	out["_type"] = n.Type
	return json.Marshal(out)
}

// Edge represents a graph edge for KuzuDB.
type Edge struct {
	Type string `json:"_type"`
	From string `json:"from"`
	To   string `json:"to"`
}

// PluginResult represents the graph-ready response returned to gleann.
type PluginResult struct {
	Nodes    []Node `json:"nodes"`
	Edges    []Edge `json:"edges"`
	Markdown string `json:"markdown,omitempty"`
	Backend  string `json:"backend,omitempty"`
}

type headingInfo struct {
	lineIdx int
	level   int
	title   string
}

// ParseDocument parses Markdown into graph-ready nodes and edges matching section_parser.py.
func ParseDocument(markdown, sourcePath, docFormat string, pageCount int) *PluginResult {
	markdown = LinkifyURLs(markdown)
	lines := strings.Split(markdown, "\n")
	var nodes []Node
	var edges []Edge

	var headings []headingInfo
	for i, line := range lines {
		m := headingRe.FindStringSubmatch(line)
		if len(m) == 3 {
			headings = append(headings, headingInfo{
				lineIdx: i,
				level:   len(m[1]),
				title:   strings.TrimSpace(m[2]),
			})
		}
	}

	// Detect flat heading syndrome (e.g. all ##)
	if len(headings) > 0 {
		firstLevel := headings[0].level
		allSame := true
		for _, h := range headings[1:] {
			if h.level != firstLevel {
				allSame = false
				break
			}
		}
		if allSame {
			headings = inferLevelsFromNumbering(headings)
		}
	}

	docTitle := inferTitle(markdown, headings)
	docSummary := extractSummary(markdown, 200)

	docNode := Node{
		Type: "Document",
		Data: map[string]any{
			"path":       sourcePath,
			"title":      docTitle,
			"format":     docFormat,
			"summary":    docSummary,
			"word_count": len(strings.Fields(markdown)),
			"page_count": pageCount,
		},
	}
	nodes = append(nodes, docNode)

	// No headings -> single implicit section
	if len(headings) == 0 {
		secID := secID(sourcePath, "s0")
		secNode := Node{
			Type: "Section",
			Data: map[string]any{
				"id":       secID,
				"heading":  docTitle,
				"level":    0,
				"content":  markdown,
				"summary":  docSummary,
				"doc_path": sourcePath,
			},
		}
		nodes = append(nodes, secNode)
		edges = append(edges, Edge{
			Type: "HAS_SECTION",
			From: sourcePath,
			To:   secID,
		})
		return &PluginResult{
			Nodes:    nodes,
			Edges:    edges,
			Markdown: markdown,
		}
	}

	type stackItem struct {
		localID string
		level   int
	}
	var parentStack []stackItem
	childCounters := make(map[string]int)

	// Text before the first heading (title page, abstract, revision table)
	// belongs to the document; keep it as a level-0 section instead of
	// dropping it.
	if pre := strings.TrimSpace(strings.Join(lines[:headings[0].lineIdx], "\n")); pre != "" {
		preID := secID(sourcePath, "pre")
		nodes = append(nodes, Node{
			Type: "Section",
			Data: map[string]any{
				"id":       preID,
				"heading":  docTitle,
				"level":    0,
				"content":  pre,
				"summary":  extractSummary(pre, 200),
				"doc_path": sourcePath,
			},
		})
		edges = append(edges, Edge{Type: "HAS_SECTION", From: sourcePath, To: preID})
	}

	for idx, h := range headings {
		contentStart := h.lineIdx + 1
		contentEnd := len(lines)
		if idx+1 < len(headings) {
			contentEnd = headings[idx+1].lineIdx
		}
		content := ""
		if contentStart <= contentEnd && contentStart <= len(lines) {
			content = strings.TrimSpace(strings.Join(lines[contentStart:contentEnd], "\n"))
		}

		for len(parentStack) > 0 && parentStack[len(parentStack)-1].level >= h.level {
			parentStack = parentStack[:len(parentStack)-1]
		}

		parentLocalID := ""
		if len(parentStack) > 0 {
			parentLocalID = parentStack[len(parentStack)-1].localID
		}

		order := childCounters[parentLocalID]
		childCounters[parentLocalID] = order + 1

		var localID string
		if parentLocalID != "" {
			localID = fmt.Sprintf("%s.%d", parentLocalID, order)
		} else {
			localID = fmt.Sprintf("s%d", order)
		}

		sID := secID(sourcePath, localID)
		secNode := Node{
			Type: "Section",
			Data: map[string]any{
				"id":       sID,
				"heading":  h.title,
				"level":    h.level,
				"content":  content,
				"summary":  extractSummary(content, 200),
				"doc_path": sourcePath,
			},
		}
		nodes = append(nodes, secNode)

		if parentLocalID == "" {
			edges = append(edges, Edge{
				Type: "HAS_SECTION",
				From: sourcePath,
				To:   sID,
			})
		} else {
			edges = append(edges, Edge{
				Type: "HAS_SUBSECTION",
				From: secID(sourcePath, parentLocalID),
				To:   sID,
			})
		}

		parentStack = append(parentStack, stackItem{
			localID: localID,
			level:   h.level,
		})
	}

	return &PluginResult{
		Nodes:    nodes,
		Edges:    edges,
		Markdown: markdown,
	}
}

func secID(docPath, localID string) string {
	return fmt.Sprintf("doc:%s:%s", docPath, localID)
}

func inferTitle(markdown string, headings []headingInfo) string {
	if len(headings) > 0 {
		for _, h := range headings {
			if h.level == 1 {
				return h.title
			}
		}
		return headings[0].title
	}
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			if len(trimmed) > 100 {
				return trimmed[:100]
			}
			return trimmed
		}
	}
	return "Untitled"
}

func extractSummary(text string, maxChars int) string {
	paragraphs := strings.Split(text, "\n\n")
	for _, p := range paragraphs {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "<!--") {
			if len(trimmed) > maxChars {
				lastSpace := strings.LastIndex(trimmed[:maxChars], " ")
				if lastSpace > 0 {
					return trimmed[:lastSpace] + "..."
				}
				return trimmed[:maxChars] + "..."
			}
			return trimmed
		}
	}
	return ""
}

func inferLevelsFromNumbering(headings []headingInfo) []headingInfo {
	result := make([]headingInfo, 0, len(headings))
	prevLevel := 1

	for _, h := range headings {
		m := numberedRe.FindStringSubmatch(h.title)
		if len(m) > 1 {
			numStr := m[1]
			level := strings.Count(numStr, ".") + 1
			prevLevel = level
			result = append(result, headingInfo{
				lineIdx: h.lineIdx,
				level:   level,
				title:   h.title,
			})
		} else if figureTableRe.MatchString(h.title) {
			result = append(result, headingInfo{
				lineIdx: h.lineIdx,
				level:   prevLevel + 1,
				title:   h.title,
			})
		} else {
			result = append(result, headingInfo{
				lineIdx: h.lineIdx,
				level:   1,
				title:   h.title,
			})
			prevLevel = 1
		}
	}

	return result
}
