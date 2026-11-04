// Package secrets stores machine passwords in the OS keychain (macOS Keychain, Linux Secret Service).
package secrets

import (
	"github.com/zalando/go-keyring"

	"github.com/LianYun/rdp_mcp/internal/config"
)

func Get(key string) (string, error) { return keyring.Get(config.KeychainService, key) }

func Set(key, password string) error { return keyring.Set(config.KeychainService, key, password) }

func Delete(key string) error { return keyring.Delete(config.KeychainService, key) }
