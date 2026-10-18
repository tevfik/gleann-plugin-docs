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
	"strings"
	"syscall"

	"github.com/tevfik/gleann-plugin-docs/internal/docparser"
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

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "gleann-plugin-docs: High-performance Go/C Document Intelligence Plugin\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  gleann-plugin-docs --serve [--port 8765] [--model path.eifm]\n")
		fmt.Fprintf(os.Stderr, "  gleann-plugin-docs convert <document.pdf|png|csv> [--json]\n")
		fmt.Fprintf(os.Stderr, "  gleann-plugin-docs models <download|verify|list> [--dir DIR]\n")
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

	if len(os.Args) > 1 && os.Args[1] == "models" {
		os.Exit(runModels(os.Args[2:]))
	}

	// CLI convert subcommand: handle before global flag.Parse to allow flexible flag positioning
	if len(os.Args) > 1 && os.Args[1] == "convert" {
		convertFlags := flag.NewFlagSet("convert", flag.ExitOnError)
		formatFlag := convertFlags.String("format", "markdown", "Output format: markdown, json, text")
		jsonBoolFlag := convertFlags.Bool("json", false, "Output parsed JSON structure (alias for --format json)")
		cModelFlag := convertFlags.String("model", "", "Path to granite_docling_bitnet_dense.eifm")
		cTokenizerFlag := convertFlags.String("tokenizer", "", "Path to tokenizer.bin")

		convertFlags.Usage = func() {
			fmt.Fprintf(os.Stderr, "Usage: gleann-plugin-docs convert <document-path> [--format json|markdown] [--model path.eifm]\n")
			convertFlags.PrintDefaults()
		}

		var flagArgs []string
		var posArgs []string
		rawArgs := os.Args[2:]
		for i := 0; i < len(rawArgs); i++ {
			arg := rawArgs[i]
			if strings.HasPrefix(arg, "-") {
				flagArgs = append(flagArgs, arg)
				if !strings.Contains(arg, "=") && i+1 < len(rawArgs) && !strings.HasPrefix(rawArgs[i+1], "-") {
					flagName := strings.TrimLeft(arg, "-")
					if flagName == "format" || flagName == "model" || flagName == "tokenizer" {
						flagArgs = append(flagArgs, rawArgs[i+1])
						i++
					}
				}
			} else {
				posArgs = append(posArgs, arg)
			}
		}

		_ = convertFlags.Parse(flagArgs)
		var filePath string
		if len(posArgs) > 0 {
			filePath = posArgs[0]
		} else {
			for _, arg := range convertFlags.Args() {
				if !strings.HasPrefix(arg, "-") && filePath == "" {
					filePath = arg
				}
			}
		}
		if filePath == "" {
			convertFlags.Usage()
			os.Exit(1)
		}

		eng, err := engine.NewEIFDoclingEngine(*cModelFlag, *cTokenizerFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize engine: %v\n", err)
			os.Exit(1)
		}
		defer eng.Close()

		md, pages, err := eng.Convert(context.Background(), filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Conversion failed: %v\n", err)
			os.Exit(1)
		}

		isJSON := *jsonBoolFlag || strings.ToLower(*formatFlag) == "json"
		if isJSON {
			ext := filepath.Ext(filePath)
			parsed := parser.ParseDocument(md, filePath, ext, pages)
			parsed.Backend = eng.BackendName()
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(parsed)
		} else {
			fmt.Println(parser.LinkifyURLs(md))
		}
		return
	}

	// Default or --serve mode: run HTTP server
	remaining := flag.Args()
	if *serveFlag || len(remaining) == 0 || (len(remaining) > 0 && remaining[0] == "serve") {
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

// runModels implements `gleann-plugin-docs models <download|verify|list>`: the
// ONNX models of the hybrid page parser (~23 MB) or the Granite VLM model (~264 MB).
func runModels(args []string) int {
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	dir := fs.String("dir", "", "model directory (default: ~/.gleann/models/docparser, or ~/.gleann/models/granite-docling for --vlm)")
	profile := fs.String("profile", "", "model set: latin (default), chinese, all, or vlm")
	vlmFlag := fs.Bool("vlm", false, "manage Granite Docling BitNet VLM model (tevfikk/granite-docling-258M-bitnet)")
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gleann-plugin-docs models <download|verify|list> [--vlm] [--profile latin|chinese|all|vlm] [--dir DIR]")
		return 2
	}
	action := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	isVLM := *vlmFlag || strings.EqualFold(*profile, "vlm")

	targetDir := *dir
	if targetDir == "" {
		if isVLM {
			targetDir = docparser.VLMModelDir()
		} else {
			targetDir = docparser.ModelDir()
		}
	}

	var specs []docparser.ModelSpec
	var err error
	if isVLM {
		specs = docparser.VLMModels()
	} else {
		specs, err = modelSpecs(*profile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 2
		}
	}

	switch action {
	case "list":
		for _, m := range specs {
			status := "missing"
			if err := docparser.VerifyModel(targetDir, m); err == nil {
				status = "ok"
			}
			fmt.Printf("%-8s %-35s %10d bytes  %s\n", m.Name, m.File, m.Size, status)
		}
	case "verify":
		bad := 0
		for _, m := range specs {
			if err := docparser.VerifyModel(targetDir, m); err != nil {
				fmt.Printf("FAIL %s: %v\n", m.File, err)
				bad++
			} else {
				fmt.Printf("ok   %s\n", m.File)
			}
		}
		if bad > 0 {
			return 1
		}
	case "download":
		err := docparser.EnsureModels(context.Background(), targetDir, specs, func(s string) { fmt.Println(s) })
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("models are in %s\n", targetDir)
	default:
		fmt.Fprintf(os.Stderr, "unknown models action %q\n", action)
		return 2
	}
	return 0
}

// modelSpecs returns the model files of the requested OCR profile: "" selects
// the default, "all" every profile (shared files listed once).
func modelSpecs(profile string) ([]docparser.ModelSpec, error) {
	if strings.EqualFold(profile, "all") {
		seen := map[string]bool{}
		var all []docparser.ModelSpec
		for _, p := range []docparser.OCRProfile{docparser.ProfileLatin, docparser.ProfileChinese} {
			for _, m := range docparser.DefaultModels(p) {
				if !seen[m.File] {
					seen[m.File] = true
					all = append(all, m)
				}
			}
		}
		return all, nil
	}
	p, err := docparser.ProfileByName(profile)
	if err != nil {
		return nil, err
	}
	return docparser.DefaultModels(p), nil
}
