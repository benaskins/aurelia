package logbuf

import (
	"testing"
)

func TestRingBasicWrite(t *testing.T) {
	r := New(5)
	r.Write([]byte("line 1\nline 2\nline 3\n"))

	lines := r.Lines()
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[0] != "line 1" || lines[1] != "line 2" || lines[2] != "line 3" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestRingOverflow(t *testing.T) {
	r := New(3)
	r.Write([]byte("a\nb\nc\nd\ne\n"))

	lines := r.Lines()
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[0] != "c" || lines[1] != "d" || lines[2] != "e" {
		t.Errorf("expected [c d e], got %v", lines)
	}
}

func TestRingPartialWrites(t *testing.T) {
	r := New(5)
	r.Write([]byte("hel"))
	r.Write([]byte("lo world\n"))
	r.Write([]byte("second line\n"))

	lines := r.Lines()
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0] != "hello world" {
		t.Errorf("expected 'hello world', got %q", lines[0])
	}
}

func TestRingLast(t *testing.T) {
	r := New(10)
	r.Write([]byte("a\nb\nc\nd\ne\n"))

	last := r.Last(3)
	if len(last) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(last))
	}
	if last[0] != "c" || last[1] != "d" || last[2] != "e" {
		t.Errorf("expected [c d e], got %v", last)
	}
}

func TestRingLastMoreThanAvailable(t *testing.T) {
	r := New(10)
	r.Write([]byte("a\nb\n"))

	last := r.Last(5)
	if len(last) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(last))
	}
}

func TestRingEmpty(t *testing.T) {
	r := New(5)
	lines := r.Lines()
	if len(lines) != 0 {
		t.Errorf("expected empty, got %v", lines)
	}
}
