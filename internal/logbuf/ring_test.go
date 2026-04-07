package logbuf

import (
	"sync"
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

func TestRingSinceZeroReturnsAllLines(t *testing.T) {
	t.Parallel()
	r := New(5)
	r.Write([]byte("a\nb\nc\n"))

	lines, gen := r.Since(0)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[0] != "a" || lines[1] != "b" || lines[2] != "c" {
		t.Errorf("expected [a b c], got %v", lines)
	}
	if gen != 3 {
		t.Errorf("expected gen=3, got %d", gen)
	}
}

func TestRingSinceReturnsOnlyNewLines(t *testing.T) {
	t.Parallel()
	r := New(10)
	r.Write([]byte("a\nb\nc\n"))

	// Capture gen after first 3 lines
	_, gen := r.Since(0)

	r.Write([]byte("d\ne\n"))

	lines, newGen := r.Since(gen)
	if len(lines) != 2 {
		t.Fatalf("expected 2 new lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "d" || lines[1] != "e" {
		t.Errorf("expected [d e], got %v", lines)
	}
	if newGen != 5 {
		t.Errorf("expected newGen=5, got %d", newGen)
	}
}

func TestRingSinceNoNewLines(t *testing.T) {
	t.Parallel()
	r := New(10)
	r.Write([]byte("a\nb\n"))

	_, gen := r.Since(0)
	lines, newGen := r.Since(gen)

	if len(lines) != 0 {
		t.Errorf("expected no new lines, got %v", lines)
	}
	if newGen != gen {
		t.Errorf("expected gen unchanged %d, got %d", gen, newGen)
	}
}

func TestRingSinceEmptyBuffer(t *testing.T) {
	t.Parallel()
	r := New(5)

	lines, gen := r.Since(0)
	if len(lines) != 0 {
		t.Errorf("expected no lines, got %v", lines)
	}
	if gen != 0 {
		t.Errorf("expected gen=0, got %d", gen)
	}
}

func TestRingSinceAcrossWrapAround(t *testing.T) {
	t.Parallel()
	// Buffer size 3 — will wrap after 3 lines
	r := New(3)
	r.Write([]byte("a\nb\nc\n"))

	_, gen := r.Since(0)

	// Write 2 more — causes wrap-around
	r.Write([]byte("d\ne\n"))

	lines, _ := r.Since(gen)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines after wrap, got %d: %v", len(lines), lines)
	}
	if lines[0] != "d" || lines[1] != "e" {
		t.Errorf("expected [d e], got %v", lines)
	}
}

func TestRingSinceConcurrent(t *testing.T) {
	t.Parallel()
	r := New(1000)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				r.Write([]byte("line\n"))
			}
		}()
	}

	// Concurrent readers must not panic or deadlock
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				r.Since(0)
			}
		}
	}()

	wg.Wait()
	close(done)
}
