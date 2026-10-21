package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/tevfik/gleann-plugin-docs/internal/httpserver"
	"github.com/tevfik/gleann-plugin-docs/internal/selfupdate"
)

// runUpdate implements `gleann-plugin-docs update [--check]`: it replaces this
// binary with the latest GitHub release, verified against checksums.txt.
func runUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "only report whether a newer release exists")
	force := fs.Bool("force", false, "reinstall even when already up to date")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rel, err := selfupdate.Latest(ctx, "tevfik", "gleann-plugin-docs")
	if err != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		return 1
	}
	current := httpserver.PluginVersion
	newer, ok := selfupdate.IsNewer(current, rel.Tag)
	if ok && !newer && !*force {
		fmt.Printf("gleann-plugin-docs v%s is up to date (latest %s)\n", current, rel.Tag)
		return 0
	}
	if *check {
		fmt.Printf("update available: v%s -> %s\n", current, rel.Tag)
		return 0
	}

	dest, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		return 1
	}
	verified, err := selfupdate.Install(ctx, rel, "gleann-plugin-docs", dest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		return 1
	}
	if !verified {
		fmt.Fprintln(os.Stderr, "warning: no checksum published for this release; archive not verified")
	}
	fmt.Printf("updated to %s. Restart gleann (or --serve) to use it.\n", rel.Tag)
	return 0
}
