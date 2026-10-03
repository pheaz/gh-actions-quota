//go:build !darwin && !linux && !windows

package setup

import "context"

type unavailableCredentialStore struct{}

func newCredentialStore() credentialStore {
	return unavailableCredentialStore{}
}

func (unavailableCredentialStore) Load(context.Context, string) (string, error) {
	return "", errCredentialStoreUnavailable
}

func (unavailableCredentialStore) Save(context.Context, string, string) error {
	return errCredentialStoreUnavailable
}

func (unavailableCredentialStore) Delete(context.Context, string) error {
	return errCredentialStoreUnavailable
}
