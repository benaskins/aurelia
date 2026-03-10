package logbuf

import (
	"testing"
)

func TestRingBasicWrite(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	r := New(10)
	r.Write([]byte("a\nb\n"))

	last := r.Last(5)
	if len(last) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(last))
	}
}

func TestRingEmpty(t *testing.T) {
	t.Parallel()
	r := New(5)
	lines := r.Lines()
	if len(lines) != 0 {
		t.Errorf("expected empty, got %v", lines)
	}
}

func TestRingTruncatesLongLines(t *testing.T) {
	t.Parallel()
	r := NewWithMaxLineBytes(5, 10)

	// Write a line longer than the 10-byte limit
	r.Write([]byte("abcdefghijklmnop\n"))

	lines := r.Lines()
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	expected := "abcdefghij... (truncated)"
	if lines[0] != expected {
		t.Errorf("expected %q, got %q", expected, lines[0])
	}
}

func TestRingDoesNotTruncateShortLines(t *testing.T) {
	t.Parallel()
	r := NewWithMaxLineBytes(5, 100)
	r.Write([]byte("short line\n"))

	lines := r.Lines()
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0] != "short line" {
		t.Errorf("expected 'short line', got %q", lines[0])
	}
}

func TestRingDefaultMaxLineBytes(t *testing.T) {
	t.Parallel()
	r := New(5)
	if r.maxLineBytes != DefaultMaxLineBytes {
		t.Errorf("expected default max line bytes %d, got %d", DefaultMaxLineBytes, r.maxLineBytes)
	}
}

func TestRingTruncatesAtExactLimit(t *testing.T) {
	t.Parallel()
	r := NewWithMaxLineBytes(5, 5)

	// Exactly at limit — should not truncate
	r.Write([]byte("abcde\n"))
	lines := r.Lines()
	if lines[0] != "abcde" {
		t.Errorf("expected 'abcde', got %q", lines[0])
	}

	// One byte over limit — should truncate
	r2 := NewWithMaxLineBytes(5, 5)
	r2.Write([]byte("abcdef\n"))
	lines2 := r2.Lines()
	expected := "abcde... (truncated)"
	if lines2[0] != expected {
		t.Errorf("expected %q, got %q", expected, lines2[0])
	}
}
