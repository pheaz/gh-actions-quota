package setup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

const credentialService = "gh-actions-quota"

var errCredentialNotFound = errors.New("credential not found")
var errCredentialStoreUnavailable = errors.New("secure credential store unavailable")
var errAuthorizedAccountMismatch = errors.New("the authorized personal GitHub account must match the quota account; run the command again and authorize as that account")

var credentialTokenPattern = regexp.MustCompile(`^ghu_[A-Za-z0-9_]+$`)

type credentialStore interface {
	Load(context.Context, string) (string, error)
	Save(context.Context, string, string) error
	Delete(context.Context, string) error
}

func credentialAccount(owner string) string {
	return "github.com:" + strings.ToLower(owner)
}

func credentialTarget(owner string) string {
	return credentialService + ":" + credentialAccount(owner)
}

func validCachedToken(token string) bool {
	return credentialTokenPattern.MatchString(token)
}

func shouldReplaceCachedCredential(err error) bool {
	if errors.Is(err, errAuthorizedAccountMismatch) {
		return true
	}
	var httpError *githubHTTPError
	return errors.As(err, &httpError) && (httpError.status == http.StatusUnauthorized || httpError.status == http.StatusForbidden)
}

func authenticationRequired(owner string) error {
	return fmt.Errorf("not authenticated with gh-actions-quota for %s; run gh actions-quota auth login", owner)
}

func (s *setup) storedAuthorizationForOwner(ctx context.Context, owner string) (string, string, int, error) {
	if s.credentials == nil {
		return "", "", 0, errors.New("secure credential storage unavailable; gh actions-quota status requires a stored authorization")
	}
	token, err := s.credentials.Load(ctx, owner)
	if err != nil {
		if errors.Is(err, errCredentialNotFound) {
			return "", "", 0, authenticationRequired(owner)
		}
		if errors.Is(err, errCredentialStoreUnavailable) {
			return "", "", 0, errors.New("secure credential storage unavailable; gh actions-quota status requires a stored authorization")
		}
		return "", "", 0, errors.New("could not read secure credential storage")
	}
	if !validCachedToken(token) {
		_ = s.credentials.Delete(ctx, owner)
		return "", "", 0, authenticationRequired(owner)
	}
	plan, quota, err := s.client.checkAccount(ctx, owner, token)
	if err == nil {
		return token, plan, quota, nil
	}
	if shouldReplaceCachedCredential(err) {
		_ = s.credentials.Delete(ctx, owner)
		return "", "", 0, authenticationRequired(owner)
	}
	return "", "", 0, err
}

func (s *setup) authorizationForOwner(ctx context.Context, owner string) (string, string, int, error) {
	if s.credentials != nil {
		if token, err := s.credentials.Load(ctx, owner); err == nil {
			if validCachedToken(token) {
				plan, quota, accountErr := s.client.checkAccount(ctx, owner, token)
				if accountErr == nil {
					return token, plan, quota, nil
				}
				if !shouldReplaceCachedCredential(accountErr) {
					return "", "", 0, accountErr
				}
			}
			_ = s.credentials.Delete(ctx, owner)
		}
	}

	token, err := s.authorize(ctx, true)
	if err != nil {
		return "", "", 0, err
	}
	plan, quota, err := s.client.checkAccount(ctx, owner, token)
	if err != nil {
		return "", "", 0, err
	}
	if s.credentials != nil {
		if err := s.credentials.Save(ctx, owner, token); err != nil {
			fmt.Fprintln(s.output, "Secure credential storage unavailable; authorization will be requested again next time.")
		}
	}
	return token, plan, quota, nil
}
