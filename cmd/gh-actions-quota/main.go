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
		fmt.Println("Usage: gh actions-quota <setup|status>\n\nsetup: For private repositories, authorize the gh-actions-quota GitHub App with\nPlan read access, store its non-expiring user token as ACTIONS_QUOTA_TOKEN,\nand create the reusable .github/workflows/gh-actions-quota.yml quota workflow.\nPublic repositories need no setup; the command exits without making changes.\n\nPrivate setup also scans .github/workflows and opens an interactive checklist.\nExisting quota callers start selected; use Space to toggle and Enter to apply.\nThen press y/n without Enter to optionally select individual jobs.\nEach new job gate has an editable 50% threshold in its workflow file.\n\nstatus: Show repository visibility and a personal account's Actions quota\nwithout changing repository files or secrets. Public repositories show the\ncurrent gh account's private quota; private repositories show the owner's quota.\nAuthorization is reused from the OS credential store, with device flow and\nclipboard copying when a fresh authorization is needed.")
		return
	}
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("gh actions-quota " + version)
		return
	}

	if len(args) != 1 {
		usage()
		os.Exit(1)
	}

	var err error
	switch args[0] {
	case "setup":
		err = setup.Run(ctx, os.Stdin, os.Stdout)
	case "status":
		err = setup.Status(ctx, os.Stdin, os.Stdout)
	default:
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: gh actions-quota <setup|status>")
}
