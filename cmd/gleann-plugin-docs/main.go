package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/tevfik/gleann-plugin-docs/internal/engine"
	"github.com/tevfik/gleann-plugin-docs/internal/httpserver"
	"github.com/tevfik/gleann-plugin-docs/internal/parser"
)

func main() {
	serveFlag := flag.Bool("serve", false, "Run HTTP plugin daemon for gleann integration")
	portFlag := flag.Int("port", 8765, "HTTP server port")
	modelFlag := flag.String("model", "", "Path to granite_docling_bitnet_dense.eifm")
	tokenizerFlag := flag.String("tokenizer", "", "Path to tokenizer.bin")
	installFlag := flag.Bool("install", false, "Register plugin into ~/.gleann/plugins.json")
	versionFlag := flag.Bool("version", false, "Show plugin version and exit")
	jsonFlag := flag.Bool("json", false, "Output parsed JSON structure during CLI convert")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "gleann-plugin-docs: High-performance Go/C Document Intelligence Plugin\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  gleann-plugin-docs --serve [--port 8765] [--model path.eifm]\n")
		fmt.Fprintf(os.Stderr, "  gleann-plugin-docs convert <document.pdf|png|csv> [--json]\n")
		fmt.Fprintf(os.Stderr, "  gleann-plugin-docs --install\n")
		fmt.Fprintf(os.Stderr, "  gleann-plugin-docs --version\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *versionFlag {
		fmt.Printf("gleann-plugin-docs v%s (schema_version=%d, backend=docling-eif)\n",
			httpserver.PluginVersion, httpserver.SchemaVersion)
		return
	}

	if *installFlag {
		if err := installPlugin(*portFlag); err != nil {
			log.Fatalf("install failed: %v", err)
		}
		fmt.Println("Plugin registered successfully into ~/.gleann/plugins.json")
		return
	}

	// CLI convert subcommand
	args := flag.Args()
	if len(args) > 0 && args[0] == "convert" {
		if len(args) < 2 {
			log.Fatalf("Usage: gleann-plugin-docs convert <file>")
		}
		filePath := args[1]
		eng, err := engine.NewEIFDoclingEngine(*modelFlag, *tokenizerFlag)
		if err != nil {
			log.Fatalf("Failed to initialize engine: %v", err)
		}
		defer eng.Close()

		md, pages, err := eng.Convert(context.Background(), filePath)
		if err != nil {
			log.Fatalf("Conversion failed: %v", err)
		}

		if *jsonFlag {
			ext := filepath.Ext(filePath)
			parsed := parser.ParseDocument(md, filePath, ext, pages)
			parsed.Markdown = md
			parsed.Backend = eng.BackendName()
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(parsed)
		} else {
			fmt.Println(md)
		}
		return
	}

	// Default or --serve mode: run HTTP server
	if *serveFlag || len(args) == 0 || (len(args) > 0 && args[0] == "serve") {
		eng, err := engine.NewEIFDoclingEngine(*modelFlag, *tokenizerFlag)
		if err != nil {
			log.Printf("[warning] EIF model failed to load (%v). Running text/fallback mode.", err)
		}

		srv := httpserver.New(eng, *portFlag)
		defer srv.Close()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			log.Println("[gleann-plugin-docs] Shutting down...")
			_ = srv.Close()
			os.Exit(0)
		}()

		if err := srv.Serve(); err != nil {
			log.Fatalf("server terminated: %v", err)
		}
		return
	}

	flag.Usage()
}

func installPlugin(port int) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	pluginsFile := filepath.Join(home, ".gleann", "plugins.json")

	execPath, err := os.Executable()
	if err != nil {
		return err
	}

	type pluginEntry struct {
		Name          string   `json:"name"`
		URL           string   `json:"url"`
		Command       []string `json:"command"`
		Capabilities  []string `json:"capabilities"`
		Extensions    []string `json:"extensions"`
		Timeout       int      `json:"timeout,omitempty"`
		Version       string   `json:"version,omitempty"`
		SchemaVersion int      `json:"schema_version,omitempty"`
		Priority      int      `json:"priority,omitempty"`
	}

	type registry struct {
		Plugins []pluginEntry `json:"plugins"`
	}

	var reg registry
	if data, err := os.ReadFile(pluginsFile); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &reg)
	}

	pluginURL := fmt.Sprintf("http://localhost:%d", port)
	var filtered []pluginEntry
	for _, p := range reg.Plugins {
		if p.Name != httpserver.PluginName && p.Name != "gleann-docs" && p.URL != pluginURL {
			filtered = append(filtered, p)
		}
	}

	filtered = append(filtered, pluginEntry{
		Name:          httpserver.PluginName,
		URL:           pluginURL,
		Command:       []string{execPath, "--serve", "--port", fmt.Sprintf("%d", port)},
		Capabilities:  httpserver.Capabilities,
		Extensions:    httpserver.SupportedExts,
		Timeout:       120,
		Version:       httpserver.PluginVersion,
		SchemaVersion: httpserver.SchemaVersion,
		Priority:      10, // Higher priority over python fallbacks
	})

	reg.Plugins = filtered
	out, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(pluginsFile), 0755); err != nil {
		return err
	}
	return os.WriteFile(pluginsFile, out, 0644)
}
