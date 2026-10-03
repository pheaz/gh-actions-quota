package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
)

var version = "dev"

type silentError struct {
	err error
}

func (e silentError) Error() string { return e.err.Error() }
func (e silentError) Unwrap() error { return e.err }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cmd := newRootCommand(version, os.Stdin, os.Stdout, os.Stderr)
	if err := cmd.ExecuteContext(ctx); err != nil {
		var silent silentError
		if !errors.As(err, &silent) {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		os.Exit(1)
	}
}
