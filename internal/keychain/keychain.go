// Package keychain provides secret storage backed by macOS Keychain.
//
// Secrets are stored as generic passwords with:
//   - Service: "com.aurelia" (all aurelia secrets share this service)
//   - Account: the secret key (e.g. "chat/database-url")
//   - Label: "aurelia: <key>" (for Keychain Access.app visibility)
//
// Secrets are scoped with kSecAttrAccessibleWhenUnlockedThisDeviceOnly:
// never synced to iCloud, never available when the machine is locked.
package keychain

import "errors"

// ErrNotFound is returned when a secret does not exist in the store.
var ErrNotFound = errors.New("secret not found")

// Store is the interface for secret storage operations.
type Store interface {
	Set(key, value string) error
	Get(key string) (string, error)
	List() ([]string, error)
	Delete(key string) error
	GetMultiple(keys []string) (map[string]string, error)
}
