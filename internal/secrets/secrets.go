// Package secrets stores machine passwords in the OS keychain (macOS Keychain, Linux Secret Service).
package secrets

import (
	"errors"

	"github.com/zalando/go-keyring"

	"github.com/LianYun/rdp_mcp/internal/config"
)

// legacyService is the keychain service name used before the rename to remoter.
// Entries stored under it are still readable; new entries go to config.KeychainService.
const legacyService = "rdp_mcp"

func Get(key string) (string, error) {
	pw, err := keyring.Get(config.KeychainService, key)
	if errors.Is(err, keyring.ErrNotFound) {
		if old, lerr := keyring.Get(legacyService, key); lerr == nil {
			return old, nil
		}
	}
	return pw, err
}

func Set(key, password string) error { return keyring.Set(config.KeychainService, key, password) }

// Delete removes the entry under the current and the legacy service name.
func Delete(key string) error {
	err := keyring.Delete(config.KeychainService, key)
	if lerr := keyring.Delete(legacyService, key); lerr == nil && errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Keychain implements adminapi.SecretStore on the OS keychain.
type Keychain struct{}

func (Keychain) Get(key string) (string, error) { return Get(key) }
func (Keychain) Set(key, pw string) error       { return Set(key, pw) }
func (Keychain) Delete(key string) error        { return Delete(key) }
