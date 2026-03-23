package keychain

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
)

// fakeBao simulates OpenBao KV v1 for testing.
type fakeBao struct {
	mu      sync.Mutex
	secrets map[string]string
	sealed  bool
}

func newFakeBao() *fakeBao {
	return &fakeBao{secrets: make(map[string]string)}
}

func (f *fakeBao) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Health endpoint
	if r.URL.Path == "/v1/sys/health" {
		if f.sealed {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]any{"sealed": true})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"sealed": false})
		return
	}

	// Unseal endpoint
	if r.URL.Path == "/v1/sys/unseal" && r.Method == "PUT" {
		var body struct {
			Key string `json:"key"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Key == "test-unseal-key" {
			f.sealed = false
			json.NewEncoder(w).Encode(map[string]any{"sealed": false})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"sealed": true})
		return
	}

	// LIST
	if r.Method == "LIST" {
		var keys []string
		for k := range f.secrets {
			keys = append(keys, k)
		}
		if len(keys) == 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"keys": keys},
		})
		return
	}

	// Strip mount prefix: /v1/secret/some/key -> some/key
	key := r.URL.Path[len("/v1/secret/"):]

	switch r.Method {
	case "GET":
		val, ok := f.secrets[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"value": val},
		})

	case "PUT":
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Value string `json:"value"`
		}
		json.Unmarshal(body, &payload)
		f.secrets[key] = payload.Value
		w.WriteHeader(http.StatusNoContent)

	case "DELETE":
		delete(f.secrets, key)
		w.WriteHeader(http.StatusNoContent)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func TestBaoStore_SetAndGet(t *testing.T) {
	srv := httptest.NewServer(newFakeBao())
	defer srv.Close()

	store := NewBaoStore(srv.URL, "test-token", "secret")

	if err := store.Set("db/url", "postgres://localhost"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	val, err := store.Get("db/url")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val != "postgres://localhost" {
		t.Errorf("expected postgres://localhost, got %s", val)
	}
}

func TestBaoStore_GetNotFound(t *testing.T) {
	srv := httptest.NewServer(newFakeBao())
	defer srv.Close()

	store := NewBaoStore(srv.URL, "test-token", "secret")

	_, err := store.Get("nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestBaoStore_Delete(t *testing.T) {
	srv := httptest.NewServer(newFakeBao())
	defer srv.Close()

	store := NewBaoStore(srv.URL, "test-token", "secret")
	store.Set("temp/key", "value")

	if err := store.Delete("temp/key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := store.Get("temp/key")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestBaoStore_List(t *testing.T) {
	srv := httptest.NewServer(newFakeBao())
	defer srv.Close()

	store := NewBaoStore(srv.URL, "test-token", "secret")
	store.Set("a", "1")
	store.Set("b", "2")

	keys, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}
}

func TestBaoStore_ListEmpty(t *testing.T) {
	srv := httptest.NewServer(newFakeBao())
	defer srv.Close()

	store := NewBaoStore(srv.URL, "test-token", "secret")

	keys, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}
}

func TestBaoStore_Ping(t *testing.T) {
	srv := httptest.NewServer(newFakeBao())
	defer srv.Close()

	store := NewBaoStore(srv.URL, "test-token", "secret")
	if err := store.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestBaoStore_PingSealed(t *testing.T) {
	fb := newFakeBao()
	fb.sealed = true
	srv := httptest.NewServer(fb)
	defer srv.Close()

	store := NewBaoStore(srv.URL, "test-token", "secret")
	err := store.Ping()
	if err == nil {
		t.Error("expected error for sealed server without unseal key")
	}
}

func TestBaoStore_AutoUnseal(t *testing.T) {
	fb := newFakeBao()
	fb.sealed = true
	srv := httptest.NewServer(fb)
	defer srv.Close()

	keyFile := t.TempDir() + "/unseal-key"
	if err := writeFile(keyFile, "test-unseal-key\n"); err != nil {
		t.Fatal(err)
	}

	store := NewBaoStore(srv.URL, "test-token", "secret", WithUnsealFile(keyFile))
	if err := store.Ping(); err != nil {
		t.Fatalf("Ping with auto-unseal: %v", err)
	}

	// Should be usable after unseal
	if err := store.Set("test", "value"); err != nil {
		t.Fatalf("Set after unseal: %v", err)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0600)
}
