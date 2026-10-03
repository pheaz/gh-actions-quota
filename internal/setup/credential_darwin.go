//go:build darwin

package setup

import (
	"context"
	"io"
	"os/exec"
	"strings"
)

type darwinCredentialStore struct{}

func newCredentialStore() credentialStore {
	return darwinCredentialStore{}
}

func (darwinCredentialStore) Load(ctx context.Context, owner string) (string, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password",
		"-a", credentialAccount(owner), "-s", credentialService, "-w")
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

func (store darwinCredentialStore) Save(ctx context.Context, owner, token string) error {
	if !validCachedToken(token) {
		return errCredentialStoreUnavailable
	}
	command := "add-generic-password -U -a " + credentialAccount(owner) +
		" -s " + credentialService + " -w " + token +
		" -T /usr/bin/security\nquit\n"
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "-i")
	cmd.Stdin = strings.NewReader(command)
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

func (darwinCredentialStore) Delete(ctx context.Context, owner string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "delete-generic-password",
		"-a", credentialAccount(owner), "-s", credentialService)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return errCredentialNotFound
	}
	return nil
}
