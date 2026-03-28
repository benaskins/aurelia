package keychain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// BaoTokenVendor creates short-lived, scoped OpenBao tokens for authenticated peers.
// It uses the root (or privileged) token to mint tokens with specific policies.
type BaoTokenVendor struct {
	store *BaoStore
}

// NewBaoTokenVendor creates a vendor backed by the given BaoStore.
func NewBaoTokenVendor(store *BaoStore) *BaoTokenVendor {
	return &BaoTokenVendor{store: store}
}

// BaoTokenResponse is the response from a token vend request.
type BaoTokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Policies  []string  `json:"policies"`
}

// VendToken creates a short-lived OpenBao token with the given policies and TTL.
func (v *BaoTokenVendor) VendToken(policies []string, ttl time.Duration) (*BaoTokenResponse, error) {
	body := fmt.Sprintf(`{"policies":%s,"ttl":%q,"renewable":false,"no_parent":true}`,
		mustJSON(policies), ttl.String())

	resp, err := v.store.do("POST", "/v1/auth/token/create", strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openbao token create: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("openbao token create: status %d", resp.StatusCode)
	}

	var result struct {
		Auth struct {
			ClientToken   string   `json:"client_token"`
			Policies      []string `json:"policies"`
			LeaseDuration int      `json:"lease_duration"`
		} `json:"auth"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("openbao token create: decode: %w", err)
	}

	return &BaoTokenResponse{
		Token:     result.Auth.ClientToken,
		ExpiresAt: time.Now().Add(time.Duration(result.Auth.LeaseDuration) * time.Second),
		Policies:  result.Auth.Policies,
	}, nil
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
