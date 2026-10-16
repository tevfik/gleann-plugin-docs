package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/tevfik/gleann-plugin-docs/internal/engine"
	"github.com/tevfik/gleann-plugin-docs/internal/parser"
)

type DocBenchResult struct {
	Filename       string  `json:"filename"`
	Format         string  `json:"format"`
	SizeBytes      int64   `json:"size_bytes"`
	Pages          int     `json:"pages"`
	WordCount      int     `json:"word_count"`
	NodesCount     int     `json:"nodes_count"`
	EdgesCount     int     `json:"edges_count"`
	AvgDurationMs  float64 `json:"avg_duration_ms"`
	MinDurationMs  float64 `json:"min_duration_ms"`
	MaxDurationMs  float64 `json:"max_duration_ms"`
	WordsPerSec    float64 `json:"words_per_sec"`
	ThroughputKBps float64 `json:"throughput_kb_s"`
	AllocKB        uint64  `json:"alloc_kb_per_op"`
}

func main() {
	dir := flag.String("dir", "testdata/documents", "directory containing sample documents")
	iterations := flag.Int("iter", 5, "number of iterations per document for timing")
	outputJSON := flag.Bool("json", false, "output results as JSON")
	flag.Parse()

	if *iterations < 1 {
		*iterations = 1
	}

	files, err := os.ReadDir(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading directory %s: %v\n", *dir, err)
		os.Exit(1)
	}

	eng, err := engine.NewEIFDoclingEngine("", "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing engine: %v\n", err)
		os.Exit(1)
	}
	defer eng.Close()

	var results []DocBenchResult
	ctx := context.Background()

	supportedExts := map[string]bool{
		".pdf": true, ".docx": true, ".xlsx": true, ".pptx": true,
		".epub": true, ".html": true, ".htm": true, ".csv": true,
		".txt": true, ".md": true, ".png": true, ".jpg": true, ".jpeg": true,
	}

	for _, fi := range files {
		if fi.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(fi.Name()))
		if !supportedExts[ext] {
			continue
		}

		filePath := filepath.Join(*dir, fi.Name())
		info, err := fi.Info()
		if err != nil {
			continue
		}

		// Warmup run
		_, _, err = eng.Convert(ctx, filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed converting %s: %v\n", fi.Name(), err)
			continue
		}

		var durations []time.Duration
		var lastMd string
		var lastPages int

		var m1, m2 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m1)

		for i := 0; i < *iterations; i++ {
			start := time.Now()
			md, pages, err := eng.Convert(ctx, filePath)
			elapsed := time.Since(start)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error during run %d of %s: %v\n", i, fi.Name(), err)
				break
			}
			durations = append(durations, elapsed)
			lastMd = md
			lastPages = pages
		}

		runtime.ReadMemStats(&m2)
		allocPerOp := uint64(0)
		if len(durations) > 0 && m2.TotalAlloc > m1.TotalAlloc {
			allocPerOp = (m2.TotalAlloc - m1.TotalAlloc) / uint64(len(durations)) / 1024
		}

		if len(durations) == 0 {
			continue
		}

		// Compute metrics
		var totalDur time.Duration
		minDur := durations[0]
		maxDur := durations[0]
		for _, d := range durations {
			totalDur += d
			if d < minDur {
				minDur = d
			}
			if d > maxDur {
				maxDur = d
			}
		}
		avgDur := totalDur / time.Duration(len(durations))

		// Parse graph nodes and edges
		parsed := parser.ParseDocument(lastMd, fi.Name(), strings.TrimPrefix(ext, "."), lastPages)
		words := len(strings.Fields(parsed.Markdown))

		avgSec := avgDur.Seconds()
		wordsPerSec := 0.0
		throughputKBps := 0.0
		if avgSec > 0 {
			wordsPerSec = float64(words) / avgSec
			throughputKBps = (float64(info.Size()) / 1024.0) / avgSec
		}

		results = append(results, DocBenchResult{
			Filename:       fi.Name(),
			Format:         ext,
			SizeBytes:      info.Size(),
			Pages:          lastPages,
			WordCount:      words,
			NodesCount:     len(parsed.Nodes),
			EdgesCount:     len(parsed.Edges),
			AvgDurationMs:  float64(avgDur.Microseconds()) / 1000.0,
			MinDurationMs:  float64(minDur.Microseconds()) / 1000.0,
			MaxDurationMs:  float64(maxDur.Microseconds()) / 1000.0,
			WordsPerSec:    wordsPerSec,
			ThroughputKBps: throughputKBps,
			AllocKB:        allocPerOp,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].AvgDurationMs < results[j].AvgDurationMs
	})

	if *outputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(results)
		return
	}

	// Print Markdown Table
	fmt.Printf("\n### Document Extraction Performance Benchmark (%d Iterations Average)\n\n", *iterations)
	fmt.Printf("| %-24s | %-6s | %10s | %6s | %7s | %8s | %12s | %11s | %10s |\n",
		"Document", "Format", "Size", "Pages", "Nodes", "Edges", "Avg Latency", "Words/sec", "Mem/Op")
	fmt.Printf("|%s|%s|%s|%s|%s|%s|%s|%s|%s|\n",
		strings.Repeat("-", 26), strings.Repeat("-", 8), strings.Repeat("-", 12),
		strings.Repeat("-", 8), strings.Repeat("-", 9), strings.Repeat("-", 10),
		strings.Repeat("-", 14), strings.Repeat("-", 13), strings.Repeat("-", 12))

	var totalWords int
	var totalSize int64
	var totalTimeMs float64

	for _, r := range results {
		totalWords += r.WordCount
		totalSize += r.SizeBytes
		totalTimeMs += r.AvgDurationMs

		sizeStr := fmt.Sprintf("%d B", r.SizeBytes)
		if r.SizeBytes >= 1024 {
			sizeStr = fmt.Sprintf("%.1f KB", float64(r.SizeBytes)/1024.0)
		}

		latStr := fmt.Sprintf("%.2f ms", r.AvgDurationMs)
		if r.AvgDurationMs < 1.0 {
			latStr = fmt.Sprintf("%.3f ms", r.AvgDurationMs)
		}

		memStr := fmt.Sprintf("%d KB", r.AllocKB)

		fmt.Printf("| %-24s | %-6s | %10s | %6d | %7d | %8d | %12s | %9.0f w/s | %10s |\n",
			truncate(r.Filename, 24), r.Format, sizeStr, r.Pages, r.NodesCount, r.EdgesCount, latStr, r.WordsPerSec, memStr)
	}

	fmt.Printf("\n**Summary Statistics:**\n")
	fmt.Printf("- Total documents tested: %d\n", len(results))
	fmt.Printf("- Total document corpus: %.2f KB\n", float64(totalSize)/1024.0)
	fmt.Printf("- Cumulative extraction time: %.2f ms\n", totalTimeMs)
	if totalTimeMs > 0 {
		fmt.Printf("- Overall processing rate: %.0f words/sec (%.1f KB/sec)\n",
			float64(totalWords)/(totalTimeMs/1000.0), (float64(totalSize)/1024.0)/(totalTimeMs/1000.0))
	}
	fmt.Println()
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}
