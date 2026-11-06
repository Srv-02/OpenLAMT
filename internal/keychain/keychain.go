// Package keychain stores favorite passwords and trusted certificate fingerprints in the OS keychain.
package keychain

import (
	"errors"

	keyring "github.com/zalando/go-keyring"
)

const (
	Service            = "openlamt"
	FingerprintService = "openlamt-fingerprint"
)

var ErrNotFound = errors.New("entry not found in keychain")

type Store interface {
	Set(host, username, password string) error
	Get(host, username string) (string, error)
	Delete(host, username string) error
	SetFingerprint(host, username, fp string) error
	GetFingerprint(host, username string) (string, error)
	DeleteFingerprint(host, username string) error
}

type OS struct{}

var _ Store = OS{}

func account(host, username string) string { return username + "@" + host }
func get(service, acct string) (string, error) {
	p, err := keyring.Get(service, acct)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return p, err
}
func del(service, acct string) error {
	err := keyring.Delete(service, acct)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
func (OS) Set(h, u, p string) error        { return keyring.Set(Service, account(h, u), p) }
func (OS) Get(h, u string) (string, error) { return get(Service, account(h, u)) }
func (OS) Delete(h, u string) error        { return del(Service, account(h, u)) }
func (OS) SetFingerprint(h, u, p string) error {
	return keyring.Set(FingerprintService, account(h, u), p)
}
func (OS) GetFingerprint(h, u string) (string, error) { return get(FingerprintService, account(h, u)) }
func (OS) DeleteFingerprint(h, u string) error        { return del(FingerprintService, account(h, u)) }
