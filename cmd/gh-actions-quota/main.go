package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/philippwallrafen/gh-actions-quota/internal/setup"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	args := os.Args[1:]
	if len(args) == 0 || (len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h")) {
		fmt.Println("Usage: gh actions-quota setup\n\nAuthorize the gh-actions-quota GitHub App with Plan read access, store its\nnon-expiring user token as ACTIONS_QUOTA_TOKEN, and create the reusable\n.github/workflows/gh-actions-quota.yml quota workflow in the current repository.\n\nSetup also scans .github/workflows and opens an interactive checklist.\nExisting quota callers start selected; use Space to toggle and Enter to apply.\nThen press y/n without Enter to optionally select individual jobs.\nEach new job gate has an editable 50% threshold in its workflow file.")
		return
	}
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("gh actions-quota " + version)
		return
	}

	if len(args) != 1 || args[0] != "setup" {
		fmt.Fprintln(os.Stderr, "Usage: gh actions-quota setup")
		os.Exit(1)
	}

	if err := setup.Run(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
