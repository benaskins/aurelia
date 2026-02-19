package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// stateFile persists service PIDs for crash recovery.
type stateFile struct {
	path string
	mu   sync.Mutex
}

// ServiceRecord is the persisted state of a running service.
type ServiceRecord struct {
	PID         int    `json:"pid,omitempty"`
	Type        string `json:"type"`
	ContainerID string `json:"container_id,omitempty"`
}

func newStateFile(dir string) *stateFile {
	return &stateFile{
		path: filepath.Join(dir, "state.json"),
	}
}

func (sf *stateFile) load() (map[string]ServiceRecord, error) {
	sf.mu.Lock()
	defer sf.mu.Unlock()

	data, err := os.ReadFile(sf.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading state file: %w", err)
	}

	var records map[string]ServiceRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("parsing state file: %w", err)
	}
	return records, nil
}

func (sf *stateFile) save(records map[string]ServiceRecord) error {
	sf.mu.Lock()
	defer sf.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(sf.path), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(sf.path, data, 0644)
}

func (sf *stateFile) set(name string, rec ServiceRecord) error {
	sf.mu.Lock()
	defer sf.mu.Unlock()

	records, err := sf.loadUnsafe()
	if err != nil || records == nil {
		records = make(map[string]ServiceRecord)
	}
	records[name] = rec

	return sf.saveUnsafe(records)
}

func (sf *stateFile) remove(name string) error {
	sf.mu.Lock()
	defer sf.mu.Unlock()

	records, err := sf.loadUnsafe()
	if err != nil || records == nil {
		return nil
	}
	delete(records, name)
	return sf.saveUnsafe(records)
}

// loadUnsafe reads without locking — caller must hold sf.mu.
func (sf *stateFile) loadUnsafe() (map[string]ServiceRecord, error) {
	data, err := os.ReadFile(sf.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var records map[string]ServiceRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (sf *stateFile) saveUnsafe(records map[string]ServiceRecord) error {
	if err := os.MkdirAll(filepath.Dir(sf.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sf.path, data, 0644)
}
