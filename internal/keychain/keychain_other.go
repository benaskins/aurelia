//go:build !darwin

package keychain

// NewSystemStore returns a MemoryStore on non-darwin platforms.
// The macOS Keychain is not available outside of macOS; secrets are
// stored in memory only and will not persist across restarts.
func NewSystemStore() *MemoryStore {
	return NewMemoryStore()
}
