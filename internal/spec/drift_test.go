package spec

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectDriftIdentical(t *testing.T) {
	t.Parallel()
	deployed := t.TempDir()
	source := t.TempDir()

	writeFile(t, deployed, "svc-a.yaml", "service:\n  name: svc-a\n  type: native\n  command: echo hello\n")
	writeFile(t, source, "svc-a.yaml", "service:\n  name: svc-a\n  type: native\n  command: echo hello\n")

	results, err := DetectDrift(deployed, source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected no drift, got %d results", len(results))
	}
}

func TestDetectDriftContentChanged(t *testing.T) {
	t.Parallel()
	deployed := t.TempDir()
	source := t.TempDir()

	writeFile(t, deployed, "svc-a.yaml", "service:\n  name: svc-a\n  type: native\n  command: /old/path/bin\n")
	writeFile(t, source, "svc-a.yaml", "service:\n  name: svc-a\n  type: native\n  command: /new/path/bin\n")

	results, err := DetectDrift(deployed, source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 drift result, got %d", len(results))
	}
	if !results[0].Changed {
		t.Error("expected Changed=true")
	}
	if results[0].Name != "svc-a.yaml" {
		t.Errorf("Name = %q, want %q", results[0].Name, "svc-a.yaml")
	}
}

func TestDetectDriftNewInSource(t *testing.T) {
	t.Parallel()
	deployed := t.TempDir()
	source := t.TempDir()

	writeFile(t, deployed, "svc-a.yaml", "service:\n  name: svc-a\n  type: native\n  command: echo a\n")
	writeFile(t, source, "svc-a.yaml", "service:\n  name: svc-a\n  type: native\n  command: echo a\n")
	writeFile(t, source, "svc-b.yaml", "service:\n  name: svc-b\n  type: native\n  command: echo b\n")

	results, err := DetectDrift(deployed, source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 drift result, got %d", len(results))
	}
	if results[0].Name != "svc-b.yaml" {
		t.Errorf("Name = %q, want %q", results[0].Name, "svc-b.yaml")
	}
	if results[0].DeployedIn {
		t.Error("expected DeployedIn=false")
	}
	if !results[0].SourceIn {
		t.Error("expected SourceIn=true")
	}
}

func TestDetectDriftExtraDeployed(t *testing.T) {
	t.Parallel()
	deployed := t.TempDir()
	source := t.TempDir()

	// Deployed has a spec not in source — this is intentional (e.g. local-only service)
	writeFile(t, deployed, "svc-a.yaml", "service:\n  name: svc-a\n  type: native\n  command: echo a\n")
	writeFile(t, deployed, "local-only.yaml", "service:\n  name: local-only\n  type: native\n  command: echo local\n")
	writeFile(t, source, "svc-a.yaml", "service:\n  name: svc-a\n  type: native\n  command: echo a\n")

	results, err := DetectDrift(deployed, source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("deployed-only specs should not be flagged as drift, got %d results", len(results))
	}
}

func TestDetectDriftMixedChanges(t *testing.T) {
	t.Parallel()
	deployed := t.TempDir()
	source := t.TempDir()

	writeFile(t, deployed, "svc-a.yaml", "service:\n  name: svc-a\n  type: native\n  command: /old/path\n")
	writeFile(t, deployed, "svc-b.yaml", "service:\n  name: svc-b\n  type: native\n  command: echo b\n")
	writeFile(t, source, "svc-a.yaml", "service:\n  name: svc-a\n  type: native\n  command: /new/path\n")
	writeFile(t, source, "svc-b.yaml", "service:\n  name: svc-b\n  type: native\n  command: echo b\n")
	writeFile(t, source, "svc-c.yaml", "service:\n  name: svc-c\n  type: native\n  command: echo c\n")

	results, err := DetectDrift(deployed, source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 drift results, got %d", len(results))
	}

	changedCount := 0
	missingCount := 0
	for _, r := range results {
		if r.Changed {
			changedCount++
		}
		if !r.DeployedIn && r.SourceIn {
			missingCount++
		}
	}
	if changedCount != 1 {
		t.Errorf("expected 1 changed, got %d", changedCount)
	}
	if missingCount != 1 {
		t.Errorf("expected 1 missing, got %d", missingCount)
	}
}

func TestDetectDriftSourceDirMissing(t *testing.T) {
	t.Parallel()
	deployed := t.TempDir()

	_, err := DetectDrift(deployed, "/nonexistent/source/dir")
	if err == nil {
		t.Error("expected error for missing source directory")
	}
}

func TestDetectDriftYmlExtension(t *testing.T) {
	t.Parallel()
	deployed := t.TempDir()
	source := t.TempDir()

	writeFile(t, deployed, "svc-a.yml", "service:\n  name: svc-a\n  type: native\n  command: /old/path\n")
	writeFile(t, source, "svc-a.yml", "service:\n  name: svc-a\n  type: native\n  command: /new/path\n")

	results, err := DetectDrift(deployed, source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 drift result for .yml files, got %d", len(results))
	}
	if !results[0].Changed {
		t.Error("expected Changed=true")
	}
}

func TestDetectDriftEmptyDirs(t *testing.T) {
	t.Parallel()
	deployed := t.TempDir()
	source := t.TempDir()

	results, err := DetectDrift(deployed, source)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected no drift for empty dirs, got %d", len(results))
	}
}
