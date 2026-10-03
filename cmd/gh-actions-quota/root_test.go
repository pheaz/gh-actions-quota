package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRootCommandTree(t *testing.T) {
	var output, errOutput bytes.Buffer
	root := newRootCommand("v1.2.3", strings.NewReader(""), &output, &errOutput)

	for _, path := range [][]string{
		{"setup"},
		{"status"},
		{"uninstall"},
		{"auth", "login"},
		{"auth", "status"},
		{"auth", "logout"},
		{"completion", "bash"},
		{"completion", "zsh"},
		{"completion", "fish"},
		{"completion", "powershell"},
	} {
		command, _, err := root.Find(path)
		if err != nil || command == root {
			t.Fatalf("command %v not found: %v", path, err)
		}
	}
}

func TestRootVersion(t *testing.T) {
	var output, errOutput bytes.Buffer
	root := newRootCommand("v1.2.3", strings.NewReader(""), &output, &errOutput)
	root.SetArgs([]string{"--version"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if output.String() != "gh actions-quota v1.2.3\n" {
		t.Fatalf("wrong version output: %q", output.String())
	}
}

func TestZshCompletionUsesExtensionDisplayName(t *testing.T) {
	var output, errOutput bytes.Buffer
	root := newRootCommand("dev", strings.NewReader(""), &output, &errOutput)
	root.SetArgs([]string{"completion", "zsh", "--no-descriptions"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	completion := output.String()
	if completion == "" || !strings.Contains(completion, "actions-quota") {
		t.Fatal("zsh completion script was not generated for the extension")
	}
}
