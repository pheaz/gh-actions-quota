package setup

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAuthorizationForOwnerReplacesRevokedCredential(t *testing.T) {
	const staleToken = "ghu_stale_token"
	requests := []string{}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/user":
			if r.Header.Get("Authorization") == "Bearer "+staleToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			io.WriteString(w, `{"login":"owner","type":"User","plan":{"name":"free"}}`)
		case "/login/device/code":
			io.WriteString(w, deviceJSON)
		case "/login/oauth/access_token":
			io.WriteString(w, `{"access_token":"`+fakeToken+`","token_type":"bearer"}`)
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
	})
	store := &fakeCredentialStore{token: staleToken}
	var output strings.Builder
	clipboardCalls := 0
	s := &setup{
		client:      c,
		output:      &output,
		browser:     func(context.Context, string) error { return nil },
		clipboard:   func(_ context.Context, code string) error { clipboardCalls++; return nil },
		credentials: store,
	}
	token, plan, quota, err := s.authorizationForOwner(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if token != fakeToken || plan != "free" || quota != 2000 {
		t.Fatalf("unexpected authorization result: token=%q plan=%q quota=%d", token, plan, quota)
	}
	if store.loads != 1 || store.deletes != 1 || store.saves != 1 || store.token != fakeToken {
		t.Fatalf("stale credential was not replaced: %+v", store)
	}
	if clipboardCalls != 1 {
		t.Fatalf("fresh device code was copied %d times", clipboardCalls)
	}
	want := []string{"/user", "/login/device/code", "/login/oauth/access_token", "/user"}
	if strings.Join(requests, ",") != strings.Join(want, ",") {
		t.Fatalf("wrong request sequence: %v", requests)
	}
}

func TestAuthorizationForOwnerContinuesWhenSecureStoreUnavailable(t *testing.T) {
	s, output, _ := setupFixture(t, &fakeGH{}, validAccount, 0)
	store := s.credentials.(*fakeCredentialStore)
	store.saveErr = errCredentialStoreUnavailable
	token, plan, quota, err := s.authorizationForOwner(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if token != fakeToken || plan != "pro" || quota != 3000 {
		t.Fatal("authorization failed when secure storage was unavailable")
	}
	if !strings.Contains(output.String(), "Secure credential storage unavailable") {
		t.Fatal("missing secure-storage fallback notice")
	}
}
