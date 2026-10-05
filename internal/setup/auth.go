package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
)

// AuthLogin authenticates the quota account for the current context and stores
// the GitHub App token in the operating system's secure credential store.
func AuthLogin(ctx context.Context, input io.Reader, output io.Writer) error {
	return newSetup(input, output).authLogin(ctx)
}

// AuthStatus reports whether the quota account for the current context has a
// usable stored gh-actions-quota authorization. The boolean is false when no
// usable credential exists.
func AuthStatus(ctx context.Context, input io.Reader, output io.Writer) (bool, error) {
	return newSetup(input, output).authStatus(ctx)
}

// AuthLogout removes only the local gh-actions-quota credential for the quota
// account in the current context. It does not remove repository setup or revoke
// the GitHub App authorization server-side.
func AuthLogout(ctx context.Context, input io.Reader, output io.Writer) error {
	return newSetup(input, output).authLogout(ctx)
}

func (s *setup) authAccount(ctx context.Context) (string, string, error) {
	repo, found, err := s.statusRepository(ctx)
	if err != nil {
		return "", "", err
	}
	if found && repo.Private {
		owner, _, _ := strings.Cut(repo.Name, "/")
		kind, err := s.checkOwner(ctx, owner)
		if err != nil {
			return "", "", err
		}
		return owner, kind, nil
	}
	account, err := s.currentPersonalAccount(ctx)
	return account, "user", err
}

func (s *setup) authLogin(ctx context.Context) error {
	account, kind, err := s.authAccount(ctx)
	if err != nil {
		return err
	}
	if s.credentials == nil {
		return errors.New("secure credential storage unavailable")
	}

	if token, loadErr := s.credentials.Load(ctx, account); loadErr == nil {
		if validCachedToken(token) {
			if checkErr := s.client.checkAuthorization(ctx, account, token, kind); checkErr == nil {
				printAuthenticated(s.output, account)
				return nil
			} else if !shouldReplaceCachedCredential(checkErr) {
				return checkErr
			}
		}
		_ = s.credentials.Delete(ctx, account)
	} else if errors.Is(loadErr, errCredentialStoreUnavailable) {
		return errors.New("secure credential storage unavailable; configure the OS credential store before running gh actions-quota auth login")
	} else if !errors.Is(loadErr, errCredentialNotFound) {
		return errors.New("could not read secure credential storage")
	}

	token, err := s.authorize(ctx, true)
	if err != nil {
		return err
	}
	if err := s.client.checkAuthorization(ctx, account, token, kind); err != nil {
		return err
	}
	if err := s.credentials.Save(ctx, account, token); err != nil {
		return errors.New("authorization succeeded but could not be stored securely")
	}
	printAuthenticated(s.output, account)
	return nil
}

func (s *setup) authStatus(ctx context.Context) (bool, error) {
	account, kind, err := s.authAccount(ctx)
	if err != nil {
		return false, err
	}
	if s.credentials == nil {
		return false, errors.New("secure credential storage unavailable")
	}

	token, err := s.credentials.Load(ctx, account)
	if err != nil {
		if errors.Is(err, errCredentialNotFound) {
			printNotAuthenticated(s.output, account)
			return false, nil
		}
		if errors.Is(err, errCredentialStoreUnavailable) {
			return false, errors.New("secure credential storage unavailable")
		}
		return false, errors.New("could not read secure credential storage")
	}
	if !validCachedToken(token) {
		_ = s.credentials.Delete(ctx, account)
		printNotAuthenticated(s.output, account)
		return false, nil
	}
	if err := s.client.checkAuthorization(ctx, account, token, kind); err != nil {
		if shouldReplaceCachedCredential(err) {
			_ = s.credentials.Delete(ctx, account)
			printNotAuthenticated(s.output, account)
			return false, nil
		}
		return false, err
	}
	printAuthenticated(s.output, account)
	return true, nil
}

func (s *setup) authLogout(ctx context.Context) error {
	account, _, err := s.authAccount(ctx)
	if err != nil {
		return err
	}
	if s.credentials == nil {
		return errors.New("secure credential storage unavailable")
	}
	if err := s.credentials.Delete(ctx, account); err != nil {
		if errors.Is(err, errCredentialNotFound) {
			fmt.Fprintf(s.output, "No gh-actions-quota authentication found for %s.\n", account)
			return nil
		}
		if errors.Is(err, errCredentialStoreUnavailable) {
			return errors.New("secure credential storage unavailable")
		}
		return errors.New("could not remove gh-actions-quota authentication")
	}
	fmt.Fprintf(s.output, "Removed gh-actions-quota authentication for %s.\n", account)
	return nil
}

func printAuthenticated(output io.Writer, account string) {
	fmt.Fprintf(output, "Account: %s\nStatus:  authenticated\nStorage: %s\n", account, credentialStorageName())
}

func printNotAuthenticated(output io.Writer, account string) {
	fmt.Fprintf(output, "Account: %s\nStatus:  not authenticated\n\nRun:\n  gh actions-quota auth login\n", account)
}

func credentialStorageName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS Keychain"
	case "windows":
		return "Windows Credential Manager"
	case "linux":
		return "Linux Secret Service"
	default:
		return "OS secure credential store"
	}
}
