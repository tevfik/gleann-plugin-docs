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

	"github.com/tevfik/gleann-plugin-docs/internal/config"
	"github.com/tevfik/gleann-plugin-docs/internal/docparser"
	"github.com/tevfik/gleann-plugin-docs/internal/engine"
	"github.com/tevfik/gleann-plugin-docs/internal/httpserver"
	"github.com/tevfik/gleann-plugin-docs/internal/indra"
	"github.com/tevfik/gleann-plugin-docs/internal/parser"
	"github.com/tevfik/gleann-plugin-docs/internal/tui"
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
		fmt.Fprintf(os.Stderr, "  gleann-plugin-docs models <download|verify|list> [--indra|--vlm|--ort] [--dir DIR]\n")
		fmt.Fprintf(os.Stderr, "  gleann-plugin-docs tui                 interactive setup (writes ~/.gleann/docs.json)\n")
		fmt.Fprintf(os.Stderr, "  gleann-plugin-docs config [show|set key value]\n")
		fmt.Fprintf(os.Stderr, "  gleann-plugin-docs --install\n")
		fmt.Fprintf(os.Stderr, "  gleann-plugin-docs --version\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	// ~/.gleann/docs.json (written by `tui`) fills the GLEANN_DOCLING_* variables
	// that are not set; the setup commands themselves must not depend on it.
	if len(os.Args) < 2 || (os.Args[1] != "tui" && os.Args[1] != "config") {
		config.Apply()
	}

	if len(os.Args) > 1 && os.Args[1] == "tui" {
		os.Exit(runTUI(*portFlag))
	}
	if len(os.Args) > 1 && os.Args[1] == "config" {
		os.Exit(runConfig(os.Args[2:]))
	}

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
		Timeout:       httpserver.TimeoutSec,
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
	indraFlag := fs.Bool("indra", false, "manage the Indra OCR models (datalab-to/indra-ocr, own license)")
	ortFlag := fs.Bool("ort", false, "only install the ONNX Runtime library into ~/.gleann/lib")
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gleann-plugin-docs models <download|verify|list> [--vlm|--indra|--ort] [--profile latin|chinese|all|vlm|indra] [--dir DIR]")
		return 2
	}
	action := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	if *ortFlag {
		return installORTRuntime()
	}
	isVLM := *vlmFlag || strings.EqualFold(*profile, "vlm")
	isIndra := *indraFlag || strings.EqualFold(*profile, "indra")

	targetDir := *dir
	if targetDir == "" {
		if isIndra {
			targetDir = indra.ModelDir()
		} else if isVLM {
			targetDir = docparser.VLMModelDir()
		} else {
			targetDir = docparser.ModelDir()
		}
	}

	var specs []docparser.ModelSpec
	var err error
	if isIndra {
		specs = indra.Models()
		if action == "download" {
			fmt.Println(indra.License)
		}
	} else if isVLM {
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
		if !isVLM {
			if p, err := docparser.FindORTLibrary(); err != nil {
				fmt.Printf("FAIL onnxruntime: %v\n", err)
				bad++
			} else {
				fmt.Printf("ok   onnxruntime (%s)\n", p)
			}
		}
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
		if !isVLM {
			// The ONNX models are useless without the runtime library.
			if code := installORTRuntime(); code != 0 {
				return code
			}
		}
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

// installORTRuntime makes sure libonnxruntime is available (see docparser.InstallORT).
func installORTRuntime() int {
	p, err := docparser.InstallORT(context.Background(), "", func(s string) { fmt.Println(s) })
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: onnxruntime: %v\n", err)
		return 1
	}
	fmt.Printf("onnxruntime: %s\n", p)
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

// runTUI opens the setup screen. The Install hook registers the plugin in
// ~/.gleann/plugins.json like `--install`.
func runTUI(port int) int {
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		fmt.Fprintln(os.Stderr, "gleann-plugin-docs tui needs an interactive terminal; use `gleann-plugin-docs config set <key> <value>` instead")
		return 1
	}
	cfg, saved, err := tui.Run(tui.Hooks{Install: func() error { return installPlugin(port) }})
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui: %v\n", err)
		return 1
	}
	if saved {
		fmt.Printf("saved %s (engine=%s, profile=%s, vlm=%v)\n", config.Path(), cfg.Engine, cfg.Profile, cfg.VLM)
	} else {
		fmt.Println("not saved")
	}
	return 0
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// runConfig is the non-interactive twin of the TUI: show the settings or set one.
func runConfig(args []string) int {
	cfg := config.Load()
	if len(args) == 0 || args[0] == "show" {
		out, _ := json.MarshalIndent(cfg, "", "  ")
		fmt.Printf("%s\n%s\n", config.Path(), out)
		return 0
	}
	if args[0] != "set" || len(args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: gleann-plugin-docs config [show | set <engine|ocr_profile|dpi|vlm|deep_tables|indra_license_accepted> <value>]")
		return 2
	}
	key, val := args[1], args[2]
	b := func() bool { v := strings.ToLower(val); return v == "1" || v == "true" || v == "on" || v == "yes" }
	switch key {
	case "engine":
		cfg.Engine = val
	case "ocr_profile":
		cfg.Profile = val
	case "dpi":
		if _, err := fmt.Sscanf(val, "%d", &cfg.DPI); err != nil {
			fmt.Fprintf(os.Stderr, "dpi: %v\n", err)
			return 2
		}
	case "vlm":
		cfg.VLM = b()
	case "deep_tables":
		cfg.Deep = b()
	case "indra_license_accepted":
		cfg.IndraLicenseAccepted = b()
	default:
		fmt.Fprintf(os.Stderr, "unknown key %q\n", key)
		return 2
	}
	cfg.Completed = true
	if err := config.Save(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}
	fmt.Printf("%s = %s\n", key, val)
	return 0
}
