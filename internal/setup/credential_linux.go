//go:build linux

package setup

import (
	"context"
	"io"
	"os/exec"
	"strings"
)

type linuxCredentialStore struct{}

func newCredentialStore() credentialStore {
	return linuxCredentialStore{}
}

func secretTool() (string, error) {
	path, err := exec.LookPath("secret-tool")
	if err != nil {
		return "", errCredentialStoreUnavailable
	}
	return path, nil
}

func (linuxCredentialStore) Load(ctx context.Context, owner string) (string, error) {
	path, err := secretTool()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, path, "lookup",
		"service", credentialService,
		"host", "github.com",
		"account", strings.ToLower(owner))
	cmd.Stderr = io.Discard
	data, err := cmd.Output()
	if err != nil {
		return "", errCredentialNotFound
	}
	token := strings.TrimSpace(string(data))
	if !validCachedToken(token) {
		return "", errCredentialNotFound
	}
	return token, nil
}

func (store linuxCredentialStore) Save(ctx context.Context, owner, token string) error {
	if !validCachedToken(token) {
		return errCredentialStoreUnavailable
	}
	path, err := secretTool()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, "store",
		"--label="+credentialService,
		"service", credentialService,
		"host", "github.com",
		"account", strings.ToLower(owner))
	cmd.Stdin = strings.NewReader(token)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return errCredentialStoreUnavailable
	}
	saved, err := store.Load(ctx, owner)
	if err != nil || saved != token {
		return errCredentialStoreUnavailable
	}
	return nil
}

func (linuxCredentialStore) Delete(ctx context.Context, owner string) error {
	path, err := secretTool()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, "clear",
		"service", credentialService,
		"host", "github.com",
		"account", strings.ToLower(owner))
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return errCredentialNotFound
	}
	return nil
}
