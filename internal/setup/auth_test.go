package setup

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestAuthLoginStoresAuthorization(t *testing.T) {
	s, output, requests := setupFixture(t, &fakeGH{}, validAccount, 0)
	if err := s.authLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	store := s.credentials.(*fakeCredentialStore)
	if *requests != 3 || store.loads != 1 || store.saves != 1 || store.token != fakeToken {
		t.Fatalf("wrong auth login operations: requests=%d store=%+v", *requests, store)
	}
	for _, want := range []string{
		"Code copied to clipboard: ABCD-EFGH",
		"Account: owner\nStatus:  authenticated\nStorage: " + credentialStorageName() + "\n",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing auth login output %q: %s", want, output)
		}
	}
}

func TestAuthLoginReusesValidCredential(t *testing.T) {
	s, output, requests := setupFixture(t, &fakeGH{}, validAccount, 0)
	store := s.credentials.(*fakeCredentialStore)
	store.token = fakeToken
	s.browser = func(context.Context, string) error { t.Fatal("auth login opened browser"); return nil }
	s.clipboard = func(context.Context, string) error { t.Fatal("auth login used clipboard"); return nil }
	if err := s.authLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *requests != 1 || store.saves != 0 || store.deletes != 0 {
		t.Fatalf("valid credential was not reused: requests=%d store=%+v", *requests, store)
	}
	if output.String() != "Account: owner\nStatus:  authenticated\nStorage: "+credentialStorageName()+"\n" {
		t.Fatalf("wrong cached login output: %s", output)
	}
}

func TestAuthStatus(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "present"}[cached], func(t *testing.T) {
			s, output, requests := setupFixture(t, &fakeGH{}, validAccount, 0)
			store := s.credentials.(*fakeCredentialStore)
			if cached {
				store.token = fakeToken
			}
			authenticated, err := s.authStatus(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if authenticated != cached {
				t.Fatalf("authenticated=%t, want %t", authenticated, cached)
			}
			if cached {
				if *requests != 1 || !strings.Contains(output.String(), "Status:  authenticated") {
					t.Fatalf("wrong authenticated status: %s", output)
				}
			} else {
				if *requests != 0 || output.String() != "Account: owner\nStatus:  not authenticated\n\nRun:\n  gh actions-quota auth login\n" {
					t.Fatalf("wrong unauthenticated status: %s", output)
				}
			}
		})
	}
}

func TestAuthStatusDiscardsRejectedCredentialWithoutDeviceFlow(t *testing.T) {
	s, output, _ := setupFixture(t, &fakeGH{}, validAccount, 0)
	store := s.credentials.(*fakeCredentialStore)
	store.token = fakeToken
	s.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Fatalf("unexpected endpoint: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	s.browser = func(context.Context, string) error { t.Fatal("auth status opened browser"); return nil }
	authenticated, err := s.authStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if authenticated || store.deletes != 1 || store.token != "" || !strings.Contains(output.String(), "not authenticated") {
		t.Fatalf("rejected credential was not discarded: %+v %s", store, output)
	}
}

func TestAuthLogout(t *testing.T) {
	s, output, _ := setupFixture(t, &fakeGH{}, validAccount, 0)
	store := s.credentials.(*fakeCredentialStore)
	store.token = fakeToken
	if err := s.authLogout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.deletes != 1 || store.token != "" || output.String() != "Removed gh-actions-quota authentication for owner.\n" {
		t.Fatalf("wrong logout result: %+v %s", store, output)
	}

	output.Reset()
	store.deleteErr = errCredentialNotFound
	if err := s.authLogout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if output.String() != "No gh-actions-quota authentication found for owner.\n" {
		t.Fatalf("logout should be idempotent: %s", output)
	}
}

func TestAuthLoginRequiresSecureStorage(t *testing.T) {
	s, _, requests := setupFixture(t, &fakeGH{}, validAccount, 0)
	store := s.credentials.(*fakeCredentialStore)
	store.loadErr = errCredentialStoreUnavailable
	err := s.authLogin(context.Background())
	if err == nil || !strings.Contains(err.Error(), "secure credential storage unavailable") || *requests != 0 {
		t.Fatalf("secure storage failure was not handled before device flow: %v", err)
	}
}

func TestAuthStatusPropagatesNonAuthenticationErrors(t *testing.T) {
	s, _, _ := setupFixture(t, &fakeGH{}, validAccount, 0)
	store := s.credentials.(*fakeCredentialStore)
	store.loadErr = errors.New("boom")
	if _, err := s.authStatus(context.Background()); err == nil || strings.Contains(err.Error(), "boom") {
		t.Fatalf("credential store error was not sanitized: %v", err)
	}
}
