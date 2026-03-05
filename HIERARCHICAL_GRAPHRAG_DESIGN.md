# Hiyerarşik Doküman GraphRAG — Tasarım Belgesi

> **Hazırlayan:** Yaver AI  
> **Tarih:** Haziran 2026  
> **Kapsam:** gleann-plugin-docs + gleann chunking modülü + KuzuDB şeması  

---

## 1. Mevcut Durum Analizi

### 1.1 Plugin Akışı (Bugün)

```
Dosya (.pdf, .docx, ...)
    │
    ▼  multipart POST /convert
gleann-plugin-docs (Python)
    │
    ├── PDF → Docling (varsa) → Markdown
    └── Diğer → MarkItDown → Markdown
    │
    ▼  {"markdown": "<düz metin>"}
gleann build pipeline (Go)
    │
    ├── SentenceSplitter.ChunkWithMetadata()  ← paragraph/sentence bölme
    │       metadata = {source, hash, chunk_index, total_chunks}
    │
    ├── embedder.Compute(texts) → [][]float32
    │
    └── HNSW index + passages.jsonl
```

### 1.2 KuzuDB Şeması (Bugün)

Sadece **kod** için tasarlanmış, doküman desteği yok:

| Tablo Tipi | Tablolar | Amaç |
|-----------|----------|------|
| **Node** | `CodeFile(path, lang)` | Kaynak dosya |
| **Node** | `Symbol(fqn, kind, file, line, name, doc)` | Fonksiyon, struct, interface, vb. |
| **Rel** | `DECLARES(CodeFile→Symbol)` | Dosya bir sembol tanımlar |
| **Rel** | `CALLS(Symbol→Symbol)` | Fonksiyon çağrısı |
| **Rel** | `IMPLEMENTS(Symbol→Symbol)` | Arayüz implementasyonu |
| **Rel** | `REFERENCES(Symbol→Symbol)` | Sembol referansı |

### 1.3 Chunking Modülü (Bugün)

| Chunker | Dosya | Kullanım | Sorun |
|---------|-------|----------|-------|
| `SentenceSplitter` | `chunking.go` | Dokümanlar, markdown | Heading farkındalığı **YOK** |
| `CodeChunker` | `chunking.go` | Kod dosyaları | Dokümanla ilgisi yok |
| `TreeSitterChunker` | `treesitter.go` | AST-tabanlı kod bölme | Dokümanla ilgisi yok |

---

## 2. Tespit Edilen Eksiklikler (Gap Analysis)

### 🔴 Kritik Eksiklikler

| # | Eksiklik | Etki | Önem |
|---|---------|------|------|
| G1 | **Plugin düz metin döndürüyor** | Heading hiyerarşisi, tablo yapısı, bölüm sınırları kaybolıyor | 🔴 Kritik |
| G2 | **Heading-farkında chunking yok** | `## Giriş` altındaki paragraflar bağlamlarını kaybediyor | 🔴 Kritik |
| G3 | **KuzuDB'de doküman şeması yok** | Dokümanlar graf dünyasında hiç temsil edilmiyor | 🔴 Kritik |
| G4 | **Chunk metadata çok zayıf** | Sadece `source` + `chunk_index` — bölüm bilgisi, başlık yok | 🔴 Kritik |

### 🟡 Önemli Eksiklikler

| # | Eksiklik | Etki | Önem |
|---|---------|------|------|
| G5 | **Dokümanlar arası çapraz referans yok** | "Bkz. Mimari Dokümanı" gibi referanslar izlenemiyor | 🟡 Önemli |
| G6 | **Doküman↔Kod köprüsü yok** | Bir chunk hangi fonksiyonu açıklıyor, bilinemez | 🟡 Önemli |
| G7 | **Bölüm/doküman özeti yok** | Global arama (GraphRAG) yapılamaz | 🟡 Önemli |
| G8 | **Arama sonuçlarında bağlam yok** | "Bu chunk nereye ait?" sorusu cevaplanamaz | 🟡 Önemli |

### 🟢 İyileştirme Fırsatları

| # | Eksiklik | Etki | Önem |
|---|---------|------|------|
| G9 | **Tablo düzgün çıkarılmıyor** | Docling tablolar için iyi ama MarkItDown zayıf | 🟢 İyileştirme |
| G10 | **30s timeout kısıtı** | Uzun PDF'ler timeout alıyor (gleann plugin.go:214) | 🟢 İyileştirme |
| G11 | **Geriye dönük uyumluluk** | Eski `{"markdown":"..."}` formatı korunmalı | 🟢 İyileştirme |

---

## 3. Çözüm Tasarımı — Hiyerarşik Doküman GraphRAG

### Yüksek Seviye Akış (Hedef)

```
Dosya (.pdf, .docx, ...)
    │
    ▼  multipart POST /convert
gleann-plugin-docs (Python, Refactored)
    │
    ├── Markdown çıktısı (mevcut backend'ler)
    ├── Markdown → Yapısal JSON parse
    │     ├── Başlık hiyerarşisi çıkarma (H1→H2→H3)
    │     ├── Bölüm sınırlarını belirleme
    │     └── Bölüm özetleri (ilk cümle veya LLM)
    │
    ▼  Structured JSON Response
    {
      "document": {...},
      "sections": [...],
      "markdown": "..." (geriye dönük uyumluluk)
    }
    │
    ▼  gleann build (Go)
    │
    ├── MarkdownChunker (YENİ)
    │     ├── Bölüm bazlı chunking
    │     ├── context_header enjeksiyonu ("# Giriş > ## Arka Plan")
    │     └── Zengin metadata (section_path, heading_level, doc_title)
    │
    ├── doc_indexer.go (YENİ)
    │     ├── Document, Section, Chunk node'ları → KuzuDB
    │     ├── HAS_SECTION, HAS_SUBSECTION, HAS_CHUNK edge'leri
    │     └── EXPLAINS edge'leri (chunk↔kod köprüsü)
    │
    ├── embedder.Compute() → HNSW index (mevcut)
    │
    └── Graph + Vector = Hybrid Search
```

---

## STEP 1: Python Plugin Refactoring

### 1.1 Yeni Endpoint: `/convert` (v2 — geriye uyumlu)

Mevcut `/convert` endpoint'i **iki format** destekleyecek:

**İstek (değişmez):**
```
POST /convert
Content-Type: multipart/form-data
  file: <binary>
```

**Yanıt — varsayılan (geriye uyumlu):**
```json
{
  "markdown": "<full flat markdown>"
}
```

**Yanıt — `Accept: application/json+structured` header'ı ile:**
```json
{
  "version": 2,
  "document": {
    "title": "KuzuDB Architecture Guide",
    "format": "pdf",
    "page_count": 42,
    "word_count": 8500,
    "summary": "This document describes the architecture of KuzuDB..."
  },
  "sections": [
    {
      "id": "s1",
      "heading": "Introduction",
      "level": 1,
      "content": "KuzuDB is an embedded property graph database...",
      "summary": "KuzuDB is an embedded property graph database designed for...",
      "parent_id": null,
      "order": 0,
      "children": ["s1.1", "s1.2"]
    },
    {
      "id": "s1.1",
      "heading": "Background",
      "level": 2,
      "content": "Property graphs have been widely adopted...",
      "summary": "Overview of property graph adoption trends.",
      "parent_id": "s1",
      "order": 0,
      "children": []
    },
    {
      "id": "s1.2",
      "heading": "Motivation",
      "level": 2,
      "content": "Traditional RDBMSs struggle with...",
      "summary": "Why embedded graph databases are needed.",
      "parent_id": "s1",
      "order": 1,
      "children": []
    }
  ],
  "markdown": "<full flat markdown>"
}
```

### 1.2 Markdown Section Parser (Python)

`section_parser.py` — yeni dosya:

```python
"""Markdown section parser — extracts heading hierarchy from markdown text."""

import re
from dataclasses import dataclass, field

@dataclass
class Section:
    id: str
    heading: str
    level: int
    content: str  # raw markdown content (without heading line)
    parent_id: str | None = None
    order: int = 0
    children: list[str] = field(default_factory=list)
    summary: str = ""

    def to_dict(self) -> dict:
        return {
            "id": self.id,
            "heading": self.heading,
            "level": self.level,
            "content": self.content,
            "summary": self.summary,
            "parent_id": self.parent_id,
            "order": self.order,
            "children": self.children,
        }

@dataclass
class DocumentStructure:
    title: str
    format: str
    page_count: int | None
    word_count: int
    summary: str
    sections: list[Section]

    def to_dict(self) -> dict:
        return {
            "version": 2,
            "document": {
                "title": self.title,
                "format": self.format,
                "page_count": self.page_count,
                "word_count": self.word_count,
                "summary": self.summary,
            },
            "sections": [s.to_dict() for s in self.sections],
        }


# Heading pattern: # Title, ## Title, ### Title, etc.
_HEADING_RE = re.compile(r"^(#{1,6})\s+(.+)$", re.MULTILINE)


def parse_markdown_sections(markdown: str, doc_format: str = "unknown",
                             page_count: int | None = None) -> DocumentStructure:
    """Parse markdown into a hierarchical section tree.

    Strategy:
    1. Split markdown by heading boundaries
    2. Build a flat list of sections with parent pointers
    3. Infer document title from first H1 (or first heading)
    4. Generate summary as first non-empty paragraph
    """
    lines = markdown.split("\n")
    
    # Find all heading positions
    headings: list[tuple[int, int, str]] = []  # (line_idx, level, title)
    for i, line in enumerate(lines):
        m = _HEADING_RE.match(line)
        if m:
            headings.append((i, len(m.group(1)), m.group(2).strip()))
    
    if not headings:
        # No headings found — treat entire text as a single section
        summary = _extract_summary(markdown)
        title = _infer_title(markdown)
        section = Section(
            id="s0", heading=title, level=0,
            content=markdown, summary=summary,
        )
        return DocumentStructure(
            title=title, format=doc_format,
            page_count=page_count,
            word_count=len(markdown.split()),
            summary=summary,
            sections=[section],
        )
    
    # Extract title from first H1, or first heading
    doc_title = next(
        (title for _, level, title in headings if level == 1),
        headings[0][2]
    )
    
    # Build sections
    sections: list[Section] = []
    parent_stack: list[tuple[str, int]] = []  # (section_id, level)
    child_counters: dict[str | None, int] = {}  # parent_id → child count
    
    for idx, (line_idx, level, title) in enumerate(headings):
        # Determine content range: from heading+1 to next heading (or EOF)
        content_start = line_idx + 1
        content_end = headings[idx + 1][0] if idx + 1 < len(headings) else len(lines)
        content = "\n".join(lines[content_start:content_end]).strip()
        
        # Find parent: walk stack backwards to find a heading with level < current
        while parent_stack and parent_stack[-1][1] >= level:
            parent_stack.pop()
        
        parent_id = parent_stack[-1][0] if parent_stack else None
        
        # Generate section ID
        order = child_counters.get(parent_id, 0)
        child_counters[parent_id] = order + 1
        
        if parent_id:
            section_id = f"{parent_id}.{order}"
        else:
            section_id = f"s{order}"
        
        section = Section(
            id=section_id,
            heading=title,
            level=level,
            content=content,
            parent_id=parent_id,
            order=order,
            summary=_extract_summary(content),
        )
        sections.append(section)
        parent_stack.append((section_id, level))
        
        # Update parent's children list
        if parent_id:
            for s in sections:
                if s.id == parent_id:
                    s.children.append(section_id)
                    break
    
    doc_summary = _extract_summary(markdown)
    
    return DocumentStructure(
        title=doc_title,
        format=doc_format,
        page_count=page_count,
        word_count=len(markdown.split()),
        summary=doc_summary,
        sections=sections,
    )


def _extract_summary(text: str, max_chars: int = 200) -> str:
    """Extract the first non-empty paragraph as summary."""
    for para in text.split("\n\n"):
        para = para.strip()
        # Skip headings and empty lines
        if para and not para.startswith("#"):
            if len(para) > max_chars:
                # Cut at word boundary
                cut = para[:max_chars].rsplit(" ", 1)[0]
                return cut + "..."
            return para
    return ""


def _infer_title(text: str) -> str:
    """Infer document title from first line or content."""
    for line in text.split("\n"):
        line = line.strip()
        if line:
            return line[:100]
    return "Untitled"
```

### 1.3 `/convert` Endpoint Güncelleme

```python
@app.post("/convert")
async def convert_document(
    file: UploadFile = File(...),
    request: Request,  # Accept header kontrolü için
):
    ext = os.path.splitext(file.filename)[1].lower()
    # ... mevcut validation ...
    
    markdown = _do_convert(tmp.name, ext)  # mevcut çıkarma mantığı
    
    # Accept header'a göre format seçimi
    accept = request.headers.get("accept", "")
    if "application/json+structured" in accept:
        structure = section_parser.parse_markdown_sections(
            markdown, doc_format=ext.lstrip("."),
        )
        result = structure.to_dict()
        result["markdown"] = markdown  # geriye uyumluluk
        return result
    
    # Varsayılan: düz markdown (mevcut davranış)
    return {"markdown": markdown}
```

### 1.4 Değişiklik Özeti

| Dosya | Değişiklik | Yeni Satır |
|-------|-----------|------------|
| `section_parser.py` | **YENİ** — Markdown hiyerarşi parser | ~150 |
| `main.py` | `/convert` v2 desteği, `Accept` header kontrolü | ~15 |
| `requirements.txt` | Değişiklik yok (pure Python) | 0 |

**Geriye uyumluluk:** `Accept` header'ı gönderilmezse eski format döner. Mevcut `gleann build` kodu bozulmaz.

---

## STEP 2: Go MarkdownChunker

### 2.1 Yeni Dosya: `modules/chunking/markdown_chunker.go`

```go
package chunking

// MarkdownSection represents a heading-delimited section from structured JSON.
type MarkdownSection struct {
    ID        string            `json:"id"`
    Heading   string            `json:"heading"`
    Level     int               `json:"level"`
    Content   string            `json:"content"`
    Summary   string            `json:"summary"`
    ParentID  string            `json:"parent_id,omitempty"`
    Order     int               `json:"order"`
    Children  []string          `json:"children,omitempty"`
}

// DocumentMeta holds top-level document metadata from the plugin.
type DocumentMeta struct {
    Title     string `json:"title"`
    Format    string `json:"format"`
    PageCount *int   `json:"page_count,omitempty"`
    WordCount int    `json:"word_count"`
    Summary   string `json:"summary"`
}

// StructuredDocument is the full response from /convert v2.
type StructuredDocument struct {
    Version  int               `json:"version"`
    Document DocumentMeta      `json:"document"`
    Sections []MarkdownSection `json:"sections"`
    Markdown string            `json:"markdown"`
}

// MarkdownChunk is a chunk with hierarchical context.
type MarkdownChunk struct {
    Text         string            // Chunk text WITH context header prepended
    RawText      string            // Chunk text WITHOUT context header
    SectionID    string            // Parent section ID
    SectionPath  []string          // ["Introduction", "Background"]
    HeadingLevel int               // Heading level of parent section
    DocTitle     string            // Document title
    Metadata     map[string]any    // Full metadata for indexing
}

// MarkdownChunker splits structured documents into context-aware chunks.
type MarkdownChunker struct {
    ChunkSize    int
    ChunkOverlap int
    splitter     *SentenceSplitter // reuse existing splitting logic
}

// NewMarkdownChunker creates a new markdown-aware chunker.
func NewMarkdownChunker(chunkSize, chunkOverlap int) *MarkdownChunker {
    return &MarkdownChunker{
        ChunkSize:    chunkSize,
        ChunkOverlap: chunkOverlap,
        splitter:     NewSentenceSplitter(chunkSize, chunkOverlap),
    }
}
```

### 2.2 Core Chunking Logic

```go
// ChunkDocument splits a structured document into context-aware chunks.
func (mc *MarkdownChunker) ChunkDocument(doc *StructuredDocument) []MarkdownChunk {
    if doc == nil || len(doc.Sections) == 0 {
        return nil
    }

    // Build section lookup for path resolution
    sectionMap := make(map[string]*MarkdownSection, len(doc.Sections))
    for i := range doc.Sections {
        sectionMap[doc.Sections[i].ID] = &doc.Sections[i]
    }

    var chunks []MarkdownChunk

    for _, section := range doc.Sections {
        if section.Content == "" {
            continue
        }

        // Build heading path: "Introduction > Background > Implementation"
        path := mc.buildSectionPath(section.ID, sectionMap)
        contextHeader := buildContextHeader(path)

        // Split section content into sub-chunks
        textChunks := mc.splitter.Chunk(section.Content)

        for i, text := range textChunks {
            // Prepend context header to each chunk
            enrichedText := contextHeader + "\n\n" + text

            metadata := map[string]any{
                "doc_title":     doc.Document.Title,
                "doc_format":    doc.Document.Format,
                "section_id":    section.ID,
                "section_title": section.Heading,
                "section_path":  strings.Join(path, " > "),
                "heading_level": section.Level,
                "chunk_index":   i,
                "total_chunks":  len(textChunks),
            }
            if section.Summary != "" {
                metadata["section_summary"] = section.Summary
            }

            chunks = append(chunks, MarkdownChunk{
                Text:         enrichedText,
                RawText:      text,
                SectionID:    section.ID,
                SectionPath:  path,
                HeadingLevel: section.Level,
                DocTitle:     doc.Document.Title,
                Metadata:     metadata,
            })
        }
    }

    return chunks
}

// buildSectionPath walks up the parent chain to build the full path.
// Example: ["Architecture Guide", "Core Components", "Query Engine"]
func (mc *MarkdownChunker) buildSectionPath(
    sectionID string, lookup map[string]*MarkdownSection,
) []string {
    var path []string
    current := sectionID

    for current != "" {
        sec, ok := lookup[current]
        if !ok {
            break
        }
        path = append([]string{sec.Heading}, path...)
        current = sec.ParentID
    }

    return path
}

// buildContextHeader formats the section path as a breadcrumb header.
// Example: "# Architecture Guide > ## Core Components > ### Query Engine"
func buildContextHeader(path []string) string {
    if len(path) == 0 {
        return ""
    }

    var parts []string
    for i, heading := range path {
        prefix := strings.Repeat("#", i+1)
        parts = append(parts, prefix+" "+heading)
    }
    return strings.Join(parts, " > ")
}
```

### 2.3 Fallback: Heading-based Chunking (yapısal JSON yokken)

Eğer plugin eski formatta (`{"markdown":"..."}`) dönerse, Go tarafında da heading parse yapabilmeliyiz:

```go
// ChunkMarkdown parses raw markdown by headings when structured JSON is unavailable.
// This is the FALLBACK path for non-plugin markdown files and old-format responses.
func (mc *MarkdownChunker) ChunkMarkdown(markdown string, source string) []MarkdownChunk {
    sections := parseMarkdownHeadings(markdown)
    if len(sections) == 0 {
        // No headings — fall back to SentenceSplitter behavior
        textChunks := mc.splitter.Chunk(markdown)
        chunks := make([]MarkdownChunk, len(textChunks))
        for i, text := range textChunks {
            chunks[i] = MarkdownChunk{
                Text:    text,
                RawText: text,
                Metadata: map[string]any{
                    "source":      source,
                    "chunk_index": i,
                    "total_chunks": len(textChunks),
                },
            }
        }
        return chunks
    }
    // ... heading-aware chunking (similar to ChunkDocument)
    return chunks
}

// parseMarkdownHeadings extracts heading structure from raw markdown.
func parseMarkdownHeadings(markdown string) []MarkdownSection {
    // Regex: ^#{1,6}\s+.+$
    // Split by heading boundaries, build section tree
    // ...
}
```

### 2.4 Entegrasyon: `readDocuments()` pipeline'a bağlama

`cmd/gleann/main.go` içindeki `readDocuments()` fonksiyonunda:

```go
// Mevcut akış:
// plugin.Process() → markdown string → SentenceSplitter.Chunk()

// Yeni akış:
// plugin.Process()   → markdown string (eski format)
// plugin.ProcessV2() → StructuredDocument (yeni format, Accept header ile)
//   ├── başarılı → MarkdownChunker.ChunkDocument()
//   └── fallback → MarkdownChunker.ChunkMarkdown()
```

### 2.5 Değişiklik Özeti

| Dosya | Değişiklik | Yeni Satır |
|-------|-----------|------------|
| `modules/chunking/markdown_chunker.go` | **YENİ** — MarkdownChunker | ~250 |
| `modules/chunking/markdown_chunker_test.go` | **YENİ** — testler | ~200 |
| `pkg/gleann/plugin.go` | `ProcessV2()` — structured JSON desteği | ~30 |
| `cmd/gleann/main.go` | `readDocuments()` — MarkdownChunker entegrasyonu | ~20 |

---

## STEP 3: KuzuDB Doküman Şeması & `doc_indexer.go`

### 3.1 Yeni Node Tabloları

```cypher
-- Doküman node'u (en üst seviye)
CREATE NODE TABLE IF NOT EXISTS Document(
    path         STRING,    -- dosya yolu (PRIMARY KEY)
    title        STRING,    -- çıkarılmış/çıkarsanmış başlık
    format       STRING,    -- "pdf", "docx", "xlsx", ...
    page_count   INT64,     -- sayfa sayısı (nullable → 0)
    word_count   INT64,     -- kelime sayısı
    summary      STRING,    -- ilk paragraf veya LLM özeti
    indexed_at   STRING,    -- ISO 8601 timestamp
    PRIMARY KEY (path)
)

-- Bölüm node'u (hiyerarşik)
CREATE NODE TABLE IF NOT EXISTS Section(
    id           STRING,    -- "s0", "s0.1", "s0.1.2" gibi
    heading      STRING,    -- bölüm başlığı
    level        INT64,     -- heading seviyesi (1-6)
    summary      STRING,    -- bölüm özeti
    doc_path     STRING,    -- ait olduğu dokümanın path'i
    word_count   INT64,     -- bölüm kelime sayısı
    PRIMARY KEY (id)
)

-- Chunk node'u (vektör indeks ile eşleşir)
CREATE NODE TABLE IF NOT EXISTS DocChunk(
    id           STRING,    -- "doc:<path>:chunk:<idx>" formatı
    text         STRING,    -- chunk text (context header dahil)
    chunk_index  INT64,     -- bölüm içindeki sıra
    section_id   STRING,    -- ait olduğu section'ın ID'si
    passage_id   INT64,     -- passages.jsonl'deki ID (HNSW eşleşmesi)
    PRIMARY KEY (id)
)
```

### 3.2 Yeni Edge Tabloları

```cypher
-- Document → Section (doküman bir bölüm içerir)
CREATE REL TABLE IF NOT EXISTS HAS_SECTION(
    FROM Document TO Section,
    MANY_MANY
)

-- Section → Section (bölüm bir alt bölüm içerir)
CREATE REL TABLE IF NOT EXISTS HAS_SUBSECTION(
    FROM Section TO Section,
    MANY_MANY
)

-- Section → DocChunk (bölüm bir chunk içerir)
CREATE REL TABLE IF NOT EXISTS HAS_CHUNK(
    FROM Section TO DocChunk,
    MANY_MANY
)

-- Document → Document (çapraz referans)
CREATE REL TABLE IF NOT EXISTS CITES(
    FROM Document TO Document,
    MANY_MANY
)

-- DocChunk → Symbol (doküman-kod köprüsü)
-- Bir chunk bir kod sembolünü açıklıyor/referans veriyor
CREATE REL TABLE IF NOT EXISTS EXPLAINS(
    FROM DocChunk TO Symbol,
    MANY_MANY
)
```

### 3.3 Tam Şema Diyagramı

```
┌─────────────────────────────────────────────────────────────────┐
│                     KuzuDB Unified Schema                       │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────┐  CITES   ┌──────────────┐                   │
│  │   Document   │─────────▶│   Document   │                   │
│  │  (path, PK)  │          │  (path, PK)  │                   │
│  │  title       │          └──────────────┘                   │
│  │  format      │                                              │
│  │  summary     │                                              │
│  └──────┬───────┘                                              │
│         │ HAS_SECTION                                          │
│         ▼                                                      │
│  ┌──────────────┐  HAS_SUBSECTION  ┌──────────────┐           │
│  │   Section    │─────────────────▶│   Section    │           │
│  │  (id, PK)    │                  │  (id, PK)    │           │
│  │  heading     │                  └──────────────┘           │
│  │  level       │                                              │
│  │  summary     │                                              │
│  └──────┬───────┘                                              │
│         │ HAS_CHUNK                                            │
│         ▼                                                      │
│  ┌──────────────┐  EXPLAINS  ┌──────────────┐                 │
│  │   DocChunk   │───────────▶│   Symbol     │  (mevcut)       │
│  │  (id, PK)    │            │  (fqn, PK)   │                 │
│  │  text        │            │  kind, name   │                 │
│  │  passage_id  │            └──────┬───────┘                 │
│  └──────────────┘                   │ DECLARES, CALLS, ...    │
│                                     ▼                          │
│                              ┌──────────────┐                 │
│                              │   CodeFile   │  (mevcut)       │
│                              │  (path, PK)  │                 │
│                              └──────────────┘                 │
└─────────────────────────────────────────────────────────────────┘
```

### 3.4 `doc_indexer.go` — Doküman Endeksleyici

Dosya: `internal/graph/indexer/doc_indexer.go`

```go
package indexer

import (
    "encoding/csv"
    "fmt"
    "os"
    "path/filepath"
    "time"

    "github.com/tevfik/gleann/modules/chunking"
    kuzu "github.com/tevfik/gleann/internal/graph/kuzu"
)

// DocIndexer indexes structured documents into KuzuDB.
type DocIndexer struct {
    db *kuzu.DB
}

// NewDocIndexer creates a new document indexer.
func NewDocIndexer(db *kuzu.DB) *DocIndexer {
    return &DocIndexer{db: db}
}

// IndexDocument indexes a structured document into the graph.
// Uses CSV COPY FROM for bulk performance (same pattern as code indexer).
func (di *DocIndexer) IndexDocument(
    doc *chunking.StructuredDocument,
    sourcePath string,
    chunks []chunking.MarkdownChunk,
) error {
    tmpDir := os.TempDir()
    conn, err := di.db.NewConn()
    if err != nil {
        return fmt.Errorf("new conn: %w", err)
    }
    defer conn.Close()

    // 1. Write Document node CSV
    docCSV := filepath.Join(tmpDir, "doc_documents.csv")
    if err := writeDocumentCSV(docCSV, doc, sourcePath); err != nil {
        return fmt.Errorf("write doc csv: %w", err)
    }

    // 2. Write Section node CSV
    secCSV := filepath.Join(tmpDir, "doc_sections.csv")
    if err := writeSectionCSV(secCSV, doc.Sections, sourcePath); err != nil {
        return fmt.Errorf("write section csv: %w", err)
    }

    // 3. Write DocChunk node CSV
    chunkCSV := filepath.Join(tmpDir, "doc_chunks.csv")
    if err := writeChunkCSV(chunkCSV, chunks, sourcePath); err != nil {
        return fmt.Errorf("write chunk csv: %w", err)
    }

    // 4. Bulk load via COPY FROM
    queries := []string{
        fmt.Sprintf(`COPY Document FROM %q (HEADER=true)`, docCSV),
        fmt.Sprintf(`COPY Section FROM %q (HEADER=true)`, secCSV),
        fmt.Sprintf(`COPY DocChunk FROM %q (HEADER=true)`, chunkCSV),
    }

    // 5. Create edges
    // HAS_SECTION: Document → root sections
    for _, sec := range doc.Sections {
        if sec.ParentID == "" {
            queries = append(queries, fmt.Sprintf(
                `MATCH (d:Document {path: %q}), (s:Section {id: %q})
                 MERGE (d)-[:HAS_SECTION]->(s)`,
                sourcePath, makeGlobalSectionID(sourcePath, sec.ID),
            ))
        }
    }

    // HAS_SUBSECTION: Section → child sections
    for _, sec := range doc.Sections {
        if sec.ParentID != "" {
            queries = append(queries, fmt.Sprintf(
                `MATCH (p:Section {id: %q}), (c:Section {id: %q})
                 MERGE (p)-[:HAS_SUBSECTION]->(c)`,
                makeGlobalSectionID(sourcePath, sec.ParentID),
                makeGlobalSectionID(sourcePath, sec.ID),
            ))
        }
    }

    // HAS_CHUNK: Section → DocChunk
    for _, chunk := range chunks {
        queries = append(queries, fmt.Sprintf(
            `MATCH (s:Section {id: %q}), (c:DocChunk {id: %q})
             MERGE (s)-[:HAS_CHUNK]->(c)`,
            makeGlobalSectionID(sourcePath, chunk.SectionID),
            makeChunkID(sourcePath, chunk.SectionID, chunk.Metadata["chunk_index"].(int)),
        ))
    }

    // Execute all in a transaction
    return kuzu.ExecTxOn(conn, queries)
}
```

### 3.5 CSV Yardımcı Fonksiyonlar

```go
func writeDocumentCSV(path string, doc *chunking.StructuredDocument, sourcePath string) error {
    f, err := os.Create(path)
    if err != nil {
        return err
    }
    defer f.Close()

    w := csv.NewWriter(f)
    w.Write([]string{"path", "title", "format", "page_count", "word_count", "summary", "indexed_at"})
    
    pageCount := int64(0)
    if doc.Document.PageCount != nil {
        pageCount = int64(*doc.Document.PageCount)
    }
    
    w.Write([]string{
        sourcePath,
        doc.Document.Title,
        doc.Document.Format,
        fmt.Sprintf("%d", pageCount),
        fmt.Sprintf("%d", doc.Document.WordCount),
        doc.Document.Summary,
        time.Now().UTC().Format(time.RFC3339),
    })
    w.Flush()
    return w.Error()
}

func writeSectionCSV(path string, sections []chunking.MarkdownSection, docPath string) error {
    f, err := os.Create(path)
    if err != nil {
        return err
    }
    defer f.Close()

    w := csv.NewWriter(f)
    w.Write([]string{"id", "heading", "level", "summary", "doc_path", "word_count"})
    
    for _, sec := range sections {
        w.Write([]string{
            makeGlobalSectionID(docPath, sec.ID),
            sec.Heading,
            fmt.Sprintf("%d", sec.Level),
            sec.Summary,
            docPath,
            fmt.Sprintf("%d", len(strings.Fields(sec.Content))),
        })
    }
    w.Flush()
    return w.Error()
}

func writeChunkCSV(path string, chunks []chunking.MarkdownChunk, docPath string) error {
    f, err := os.Create(path)
    if err != nil {
        return err
    }
    defer f.Close()

    w := csv.NewWriter(f)
    w.Write([]string{"id", "text", "chunk_index", "section_id", "passage_id"})
    
    for _, chunk := range chunks {
        idx := chunk.Metadata["chunk_index"].(int)
        w.Write([]string{
            makeChunkID(docPath, chunk.SectionID, idx),
            chunk.Text,
            fmt.Sprintf("%d", idx),
            makeGlobalSectionID(docPath, chunk.SectionID),
            "0", // passage_id assigned later during HNSW build
        })
    }
    w.Flush()
    return w.Error()
}

// makeGlobalSectionID ensures section IDs are globally unique across documents.
func makeGlobalSectionID(docPath, sectionID string) string {
    return fmt.Sprintf("doc:%s:sec:%s", docPath, sectionID)
}

func makeChunkID(docPath, sectionID string, chunkIndex int) string {
    return fmt.Sprintf("doc:%s:sec:%s:chunk:%d", docPath, sectionID, chunkIndex)
}
```

### 3.6 Sorgu Örnekleri (Cypher)

```cypher
-- 1. Bir dokümanın tüm bölümlerini getir (hiyerarşik)
MATCH (d:Document {path: "architecture.pdf"})-[:HAS_SECTION]->(s:Section)
RETURN s.heading, s.level, s.summary
ORDER BY s.id

-- 2. Bir bölümün alt bölümlerini getir (recursive)
MATCH (s:Section {heading: "Core Components"})-[:HAS_SUBSECTION*1..3]->(sub:Section)
RETURN sub.heading, sub.level, sub.summary

-- 3. Bir chunk'ın bağlamını getir (sectionPath)
MATCH (c:DocChunk {id: "doc:arch.pdf:sec:s1.2:chunk:0"})
      <-[:HAS_CHUNK]-(s:Section)
      <-[:HAS_SUBSECTION*0..5]-(parent:Section)
RETURN parent.heading, s.heading, c.text

-- 4. Kod-doküman köprüsü: Bir fonksiyonu açıklayan chunk'lar
MATCH (c:DocChunk)-[:EXPLAINS]->(sym:Symbol {name: "QueryEngine"})
RETURN c.text, c.section_id

-- 5. Doküman özeti (Global Search / GraphRAG)
MATCH (d:Document)
RETURN d.title, d.summary, d.word_count
ORDER BY d.indexed_at DESC
LIMIT 10

-- 6. Cross-document references
MATCH (d1:Document)-[:CITES]->(d2:Document)
RETURN d1.title, d2.title
```

### 3.7 Değişiklik Özeti

| Dosya | Değişiklik | Yeni Satır |
|-------|-----------|------------|
| `internal/graph/kuzu/db.go` | `initSchema()` — 3 yeni node + 4 yeni rel table | ~40 |
| `internal/graph/indexer/doc_indexer.go` | **YENİ** — Document indexer | ~250 |
| `internal/graph/indexer/doc_indexer_test.go` | **YENİ** — testler | ~200 |

---

## 4. Uygulama Sıralaması

### Faz 1: Python Plugin (1-2 gün)
1. `section_parser.py` oluştur (pure Python, bağımlılık yok)
2. `/convert` endpoint'ini güncelle (`Accept` header kontrolü)
3. Unit testleri yaz
4. Mevcut davranışın bozulmadığını doğrula

### Faz 2: Go MarkdownChunker (1-2 gün)
1. `modules/chunking/markdown_chunker.go` oluştur
2. `ChunkDocument()` ve `ChunkMarkdown()` implementasyonu
3. Context header enjeksiyonu
4. Unit testleri yaz
5. `pkg/gleann/plugin.go` — `ProcessV2()` ekle 

### Faz 3: KuzuDB Şeması (1 gün)
1. `db.go` — `initSchema()` güncellemesi (yeni tablolar)
2. `doc_indexer.go` — CSV bulk load
3. Edge'leri oluşturma (HAS_SECTION, HAS_SUBSECTION, HAS_CHUNK)
4. Unit testleri yaz

### Faz 4: Pipeline Entegrasyonu (1 gün)
1. `cmd/gleann/main.go` — `readDocuments()` güncelleme
2. Plugin → MarkdownChunker → DocIndexer akışı
3. EXPLAINS edge'leri (chunk↔symbol eşleştirme) — ileride
4. Entegrasyon testleri

### Faz 5: GraphRAG Sorguları (gelecek)
1. Global search → Doküman özetleri üzerinden
2. Local search → Section/chunk traversal
3. DRIFT search → Hybrid (vektör + graf)
4. EXPLAINS edge oluşturma (NLP-based veya LLM-based eşleşme)

---

## 5. Riskler ve Mitigasyon

| Risk | Etki | Mitigasyon |
|------|------|------------|
| Markdown heading formatı tutarsız | Parser hatalı parse yapar | Regex + heuristic fallback |
| Docling markdown'da heading olmayabilir | Yapısal bilgi çıkarılamaz | Sayfa numarasını section olarak kullan |
| KuzuDB single-writer | Concurrent indexing sorun | Gleann service mode zaten single-writer |
| Büyük dokümanlar (>100 sayfa) | Bellek ve zaman sorunu | Streaming parser, sayfa bazlı chunking |
| EXPLAINS edge doğruluğu | Yanlış code↔doc eşleştirme | İlk aşamada basit keyword matching, ileride LLM |

---

## 6. Beklenen Kazanımlar

| Metrik | Bugün | Hedef |
|--------|-------|-------|
| **Chunk bağlam kalitesi** | ❌ Yok (düz metin) | ✅ Section path + heading context |
| **Arama doğruluğu** | Orta (sadece vektör) | Yüksek (vektör + graf traversal) |
| **Doküman navigasyonu** | ❌ Yok | ✅ Heading hiyerarşisi ile drill-down |
| **Kod↔Doküman köprüsü** | ❌ Yok | ✅ EXPLAINS edge'leri |
| **Global arama (GraphRAG)** | ❌ Yok | ✅ Doküman/section özetleri üzerinden |
| **Geriye uyumluluk** | N/A | ✅ Accept header ile opt-in |
