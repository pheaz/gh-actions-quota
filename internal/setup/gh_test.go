package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The test executable acts as a fake gh subprocess, with no network or real tokens.
func TestMain(m *testing.M) {
	if os.Getenv("ACTIONS_QUOTA_TEST_HELPER") == "1" {
		data, _ := io.ReadAll(os.Stdin)
		if os.Getenv("GH_DEBUG") != "" || os.Getenv("DEBUG") != "" || os.Getenv("GH_HOST") != "github.com" {
			os.Exit(2)
		}
		for _, arg := range os.Args[1:] {
			if strings.Contains(arg, fakeToken) {
				os.Exit(3)
			}
		}
		if len(data) > 0 {
			if string(data) != fakeToken {
				os.Exit(4)
			}
			// Simulate a command that echoes secrets: production runner must discard both streams.
			fmt.Fprintln(os.Stdout, string(data))
			fmt.Fprintln(os.Stderr, string(data))
		} else {
			json.NewEncoder(os.Stdout).Encode(os.Args[1:])
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRealRunnerKeepsTokenOutOfArgvAndDiscardsLogs(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "gh"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ACTIONS_QUOTA_TEST_HELPER", "1")
	t.Setenv("GH_DEBUG", "api")
	t.Setenv("DEBUG", "1")
	t.Setenv("GH_HOST", "example.com")
	args := []string{"secret", "set", secretName, "--repo", "owner/repo"}
	result, err := (ghRunner{}).Run(context.Background(), args, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(args)
	if !bytes.Equal(bytes.TrimSpace(result), want) {
		t.Fatal("argv changed")
	}
	result, err = (ghRunner{}).Run(context.Background(), args, strings.NewReader(fakeToken))
	if err != nil || len(result) != 0 {
		t.Fatal("stdin token write failed or leaked subprocess output")
	}
}
