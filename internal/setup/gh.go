package setup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type runner interface {
	Run(context.Context, []string, io.Reader) ([]byte, error)
}

type ghRunner struct{}

func (ghRunner) Run(ctx context.Context, args []string, stdin io.Reader) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Stdin = stdin
	cmd.Stderr = io.Discard
	// Do not inherit debug logging that could dump stdin or HTTP requests.
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		if name != "GH_DEBUG" && name != "DEBUG" && name != "GH_HOST" {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, "GH_HOST=github.com")
	var output bytes.Buffer
	if stdin == nil {
		cmd.Stdout = &output
	} else {
		cmd.Stdout = io.Discard
	}
	if err := cmd.Run(); err != nil {
		return nil, errors.New("gh command failed")
	}
	return output.Bytes(), nil
}

func openBrowser(ctx context.Context, uri string) error {
	// Only the validated GitHub verification URL ever reaches this function.
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", uri)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", uri)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", uri)
	}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run()
}

func copyToClipboard(ctx context.Context, text string) error {
	var commands [][]string
	switch runtime.GOOS {
	case "darwin":
		commands = [][]string{{"pbcopy"}}
	case "windows":
		commands = [][]string{{"clip.exe"}}
	default:
		commands = [][]string{
			{"wl-copy"},
			{"xclip", "-selection", "clipboard"},
			{"xsel", "--clipboard", "--input"},
		}
	}

	for _, command := range commands {
		if _, err := exec.LookPath(command[0]); err != nil {
			continue
		}
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		cmd.Stdin = strings.NewReader(text)
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	return errors.New("clipboard unavailable")
}
