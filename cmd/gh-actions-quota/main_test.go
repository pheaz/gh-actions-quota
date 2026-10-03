package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Run the CLI and a fake gh as subprocesses without accessing GitHub.
func TestMain(m *testing.M) {
	if os.Getenv("ACTIONS_QUOTA_PUBLIC_SETUP_TEST") == "1" {
		name := filepath.Base(os.Args[0])
		if name == "gh" || name == "gh.exe" {
			switch strings.Join(os.Args[1:], " ") {
			case "--version":
				fmt.Println("gh test")
			case "repo view --json nameWithOwner,url,isPrivate":
				fmt.Println(`{"nameWithOwner":"some-org/example","url":"https://github.com/some-org/example","isPrivate":false}`)
			default:
				os.Exit(2)
			}
			os.Exit(0)
		}
		os.Args = []string{"gh-actions-quota", "setup"}
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestPublicSetupExitsSuccessfullyWithoutChangingWorkflows(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	bin := t.TempDir()
	name := "gh"
	if strings.HasSuffix(exe, ".exe") {
		name += ".exe"
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, name), data, 0700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	workflow := []byte("# existing workflow\njobs:\n  build:\n    runs-on: ubuntu-latest\n")
	if err := os.WriteFile(filepath.Join(dir, "ci.yml"), workflow, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ACTIONS_QUOTA_PUBLIC_SETUP_TEST", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("public setup must exit 0: %v\n%s", err, output)
	}
	want := "Repository: some-org/example\nVisibility: Public (unmetered)\n\nSetup is not required for public repositories.\n"
	if string(output) != want {
		t.Fatalf("wrong public setup output: %s", output)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "ci.yml" {
		t.Fatalf("setup created workflow files: %v, %v", entries, err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "ci.yml"))
	if err != nil || !reflect.DeepEqual(after, workflow) {
		t.Fatalf("setup changed the existing workflow: %v", err)
	}
}
