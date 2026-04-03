package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/benaskins/aurelia/internal/keychain"
)

// SetSecretCache configures the cached secret store for serving
// secret lookups over the local unix socket.
func (s *Server) SetSecretCache(cache *keychain.CachedStore) {
	s.secretCache = cache
}

// secretsList returns all secrets as a key→value map.
// Tessera-only (requires mTLS peer identity).
func (s *Server) secretsList(w http.ResponseWriter, r *http.Request) {
	peer := PeerIdentity(r.Context())
	if peer == "" || peer == "cli" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "bulk secret list requires mTLS authentication",
		})
		return
	}

	if s.secretCache == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "secret cache not configured",
		})
		return
	}

	keys, err := s.secretCache.List()
	if err != nil {
		s.logger.Error("secret list failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}

	secrets := make(map[string]string, len(keys))
	for _, key := range keys {
		val, err := s.secretCache.Get(key)
		if err != nil {
			continue
		}
		secrets[key] = val
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(secrets)
}

// cacheInvalidate evicts a key (or all keys) from the secret cache.
// Tessera-only (requires mTLS peer identity).
// Body: {"key": "secret-name"} to invalidate one key, or {} to invalidate all.
func (s *Server) cacheInvalidate(w http.ResponseWriter, r *http.Request) {
	peer := PeerIdentity(r.Context())
	if peer == "" || peer == "cli" {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "cache invalidation requires mTLS authentication",
		})
		return
	}

	if s.secretCache == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no cache"})
		return
	}

	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	if body.Key != "" {
		s.secretCache.Invalidate(body.Key)
		s.logger.Info("cache invalidated", "key", body.Key, "peer", peer)
	} else {
		s.secretCache.InvalidateAll()
		s.logger.Info("cache invalidated (all)", "peer", peer)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) secretGet(w http.ResponseWriter, r *http.Request) {
	if s.secretCache == nil {
		http.Error(w, `{"error":"secret cache not configured"}`, http.StatusServiceUnavailable)
		return
	}

	key := r.PathValue("key")
	val, err := s.secretCache.Get(key)
	if err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		s.logger.Error("secret get failed", "key", key, "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"value": val})
}
