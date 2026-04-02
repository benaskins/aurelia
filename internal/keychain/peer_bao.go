package keychain

import (
	"fmt"
	"sync"
	"time"
)

// TokenVendFunc returns a short-lived OpenBao token on demand.
type TokenVendFunc func() (string, error)

// PeerBaoStore implements Store by reading from a remote OpenBao instance
// using short-lived tokens vended by a peer aurelia daemon.
//
// Write operations are not supported (vended tokens are read-only).
type PeerBaoStore struct {
	vendFunc TokenVendFunc
	store    *BaoStore

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// NewPeerBaoStore creates a store that reads from a remote OpenBao at addr,
// vending tokens on demand via vendFunc.
func NewPeerBaoStore(addr, mount string, vendFunc TokenVendFunc) *PeerBaoStore {
	return &PeerBaoStore{
		vendFunc: vendFunc,
		store:    NewBaoStore(addr, "", mount),
	}
}

func (p *PeerBaoStore) ensureToken() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.token != "" && time.Until(p.expiresAt) > 5*time.Second {
		return nil
	}

	token, err := p.vendFunc()
	if err != nil {
		return fmt.Errorf("vending openbao token: %w", err)
	}
	p.token = token
	p.expiresAt = time.Now().Add(55 * time.Second)
	p.store.token = token
	return nil
}

func (p *PeerBaoStore) Get(key string) (string, error) {
	if err := p.ensureToken(); err != nil {
		return "", err
	}
	return p.store.Get(key)
}

func (p *PeerBaoStore) List() ([]string, error) {
	if err := p.ensureToken(); err != nil {
		return nil, err
	}
	return p.store.List()
}

func (p *PeerBaoStore) Set(key, value string) error {
	return fmt.Errorf("peer secret store is read-only")
}

func (p *PeerBaoStore) Delete(key string) error {
	return fmt.Errorf("peer secret store is read-only")
}

// PKIIssuer returns a BaoPKIIssuer for the given mount, using the same
// token vending mechanism as the secret store.
func (p *PeerBaoStore) PKIIssuer(mount string) *BaoPKIIssuer {
	pkiStore := NewBaoStore(p.store.addr, "", mount)
	return &BaoPKIIssuer{
		store: pkiStore,
		mount: mount,
		preRequest: func() error {
			if err := p.ensureToken(); err != nil {
				return err
			}
			pkiStore.token = p.store.token
			return nil
		},
	}
}
