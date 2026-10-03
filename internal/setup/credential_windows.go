//go:build windows

package setup

import (
	"context"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	windowsCredentialTypeGeneric = 1
	windowsCredentialPersistLocalMachine = 2
	windowsErrorNotFound = syscall.Errno(1168)
)

var (
	advapi32        = syscall.NewLazyDLL("advapi32.dll")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
	procCredFree    = advapi32.NewProc("CredFree")
)

type windowsFiletime struct {
	LowDateTime  uint32
	HighDateTime uint32
}

type windowsCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windowsFiletime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

type windowsCredentialStore struct{}

func newCredentialStore() credentialStore {
	return windowsCredentialStore{}
}

func windowsTarget(owner string) (*uint16, error) {
	target, err := syscall.UTF16PtrFromString(credentialTarget(owner))
	if err != nil {
		return nil, errCredentialStoreUnavailable
	}
	return target, nil
}

func (windowsCredentialStore) Load(_ context.Context, owner string) (string, error) {
	target, err := windowsTarget(owner)
	if err != nil {
		return "", err
	}
	var credential *windowsCredential
	ok, _, callErr := procCredReadW.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(windowsCredentialTypeGeneric),
		0,
		uintptr(unsafe.Pointer(&credential)),
	)
	runtime.KeepAlive(target)
	if ok == 0 {
		if errno, matches := callErr.(syscall.Errno); matches && errno == windowsErrorNotFound {
			return "", errCredentialNotFound
		}
		return "", errCredentialStoreUnavailable
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(credential)))
	if credential == nil || credential.CredentialBlob == nil || credential.CredentialBlobSize == 0 || credential.CredentialBlobSize > 4096 {
		return "", errCredentialNotFound
	}
	data := unsafe.Slice(credential.CredentialBlob, int(credential.CredentialBlobSize))
	token := string(append([]byte(nil), data...))
	if !validCachedToken(token) {
		return "", errCredentialNotFound
	}
	return token, nil
}

func (windowsCredentialStore) Save(_ context.Context, owner, token string) error {
	if !validCachedToken(token) {
		return errCredentialStoreUnavailable
	}
	target, err := windowsTarget(owner)
	if err != nil {
		return err
	}
	username, utfErr := syscall.UTF16PtrFromString(credentialAccount(owner))
	if utfErr != nil {
		return errCredentialStoreUnavailable
	}
	blob := []byte(token)
	credential := windowsCredential{
		Type:               windowsCredentialTypeGeneric,
		TargetName:         target,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            windowsCredentialPersistLocalMachine,
		UserName:           username,
	}
	ok, _, _ := procCredWriteW.Call(uintptr(unsafe.Pointer(&credential)), 0)
	runtime.KeepAlive(target)
	runtime.KeepAlive(username)
	runtime.KeepAlive(blob)
	if ok == 0 {
		return errCredentialStoreUnavailable
	}
	return nil
}

func (windowsCredentialStore) Delete(_ context.Context, owner string) error {
	target, err := windowsTarget(owner)
	if err != nil {
		return err
	}
	ok, _, callErr := procCredDeleteW.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(windowsCredentialTypeGeneric),
		0,
	)
	runtime.KeepAlive(target)
	if ok == 0 {
		if errno, matches := callErr.(syscall.Errno); matches && errno == windowsErrorNotFound {
			return errCredentialNotFound
		}
		return errCredentialStoreUnavailable
	}
	return nil
}
