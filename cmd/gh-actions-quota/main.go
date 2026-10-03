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
		fmt.Println("Usage: gh actions-quota setup [--init]\n\nAuthorize the actions-quota GitHub App with Plan read access, store its\nnon-expiring user token as ACTIONS_QUOTA_TOKEN, and create the reusable\n.github/workflows/gh-actions-quota.yml quota workflow in the current repository.\n\n--init additionally scans .github/workflows and asks which existing workflow\nfiles should receive the reusable quota caller job.")
		return
	}
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("gh actions-quota " + version)
		return
	}

	initialize := false
	switch {
	case len(args) == 1 && args[0] == "setup":
	case len(args) == 2 && args[0] == "setup" && args[1] == "--init":
		initialize = true
	default:
		fmt.Fprintln(os.Stderr, "Usage: gh actions-quota setup [--init]")
		os.Exit(1)
	}

	if err := setup.Run(ctx, os.Stdin, os.Stdout, initialize); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
