//go:build darwin

package keychain

import (
	"errors"
	"fmt"

	gokeychain "github.com/keybase/go-keychain"
)

const (
	// ServiceName is the Keychain service attribute for all aurelia secrets.
	ServiceName = "com.aurelia"
)

// SystemStore provides CRUD operations for secrets in macOS Keychain.
type SystemStore struct {
	service string
}

// NewSystemStore creates a new Keychain-backed secret store.
func NewSystemStore() *SystemStore {
	return &SystemStore{service: ServiceName}
}

// Set stores a secret in the Keychain. Overwrites if it already exists.
func (s *SystemStore) Set(key, value string) error {
	// Try to delete existing item first (update = delete + add)
	_ = s.Delete(key)

	item := gokeychain.NewGenericPassword(
		s.service,
		key,
		fmt.Sprintf("aurelia: %s", key),
		[]byte(value),
		"",
	)
	item.SetSynchronizable(gokeychain.SynchronizableNo)
	item.SetAccessible(gokeychain.AccessibleWhenUnlockedThisDeviceOnly)

	if err := gokeychain.AddItem(item); err != nil {
		return fmt.Errorf("keychain add %q: %w", key, err)
	}
	return nil
}

// Get retrieves a secret from the Keychain.
func (s *SystemStore) Get(key string) (string, error) {
	data, err := gokeychain.GetGenericPassword(s.service, key, "", "")
	if err != nil {
		if errors.Is(err, gokeychain.ErrorItemNotFound) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, key)
		}
		return "", fmt.Errorf("keychain get %q: %w", key, err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return string(data), nil
}

// List returns all secret keys stored by aurelia.
func (s *SystemStore) List() ([]string, error) {
	accounts, err := gokeychain.GetGenericPasswordAccounts(s.service)
	if err != nil {
		if errors.Is(err, gokeychain.ErrorItemNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("keychain list: %w", err)
	}
	return accounts, nil
}

// Delete removes a secret from the Keychain.
func (s *SystemStore) Delete(key string) error {
	err := gokeychain.DeleteGenericPasswordItem(s.service, key)
	if err != nil && !errors.Is(err, gokeychain.ErrorItemNotFound) {
		return fmt.Errorf("keychain delete %q: %w", key, err)
	}
	return nil
}
