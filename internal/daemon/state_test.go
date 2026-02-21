package daemon

import (
	"path/filepath"
	"testing"
)

func TestStateFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sf := newStateFile(dir)

	// Initially empty
	records, err := sf.load()
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if records != nil {
		t.Fatalf("expected nil, got %v", records)
	}

	// Set a record
	if err := sf.set("svc-a", ServiceRecord{Type: "native", PID: 12345}); err != nil {
		t.Fatalf("set: %v", err)
	}

	// Read it back
	records, err = sf.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if rec, ok := records["svc-a"]; !ok || rec.PID != 12345 {
		t.Errorf("expected PID 12345, got %v", records)
	}

	// Add another
	if err := sf.set("svc-b", ServiceRecord{Type: "container"}); err != nil {
		t.Fatalf("set: %v", err)
	}

	records, err = sf.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 records, got %d", len(records))
	}

	// Verify file path
	expected := filepath.Join(dir, "state.json")
	if sf.path != expected {
		t.Errorf("expected path %s, got %s", expected, sf.path)
	}
}
