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
		fmt.Println("Usage: gh actions-quota setup\n\nAuthorize the actions-quota GitHub App with Plan read access and store its\nnon-expiring user token as ACTIONS_QUOTA_TOKEN in the current repository.")
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
	if err := setup.Run(ctx, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
