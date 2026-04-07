package logbuf

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

// DefaultMaxLineBytes is the default maximum size of a single log line in bytes.
// Lines longer than this are truncated to prevent unbounded memory usage.
const DefaultMaxLineBytes = 8192

// Ring is a thread-safe ring buffer that stores the last N lines of output.
// It implements io.Writer so it can be used as stdout/stderr for a process.
type Ring struct {
	mu           sync.Mutex
	lines        []string
	size         int
	pos          int
	full         bool
	maxLineBytes int
	generation   int
	// partial holds an incomplete line (no trailing newline yet)
	partial bytes.Buffer
}

// New creates a ring buffer that stores the last n lines.
func New(n int) *Ring {
	return &Ring{
		lines:        make([]string, n),
		size:         n,
		maxLineBytes: DefaultMaxLineBytes,
	}
}

// NewWithMaxLineBytes creates a ring buffer with a custom per-line byte limit.
// If maxBytes is <= 0, DefaultMaxLineBytes is used.
func NewWithMaxLineBytes(n int, maxBytes int) *Ring {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxLineBytes
	}
	return &Ring{
		lines:        make([]string, n),
		size:         n,
		maxLineBytes: maxBytes,
	}
}

// Write implements io.Writer. Splits input on newlines and stores each line.
func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.partial.Write(p)

	for {
		line, err := r.partial.ReadString('\n')
		if err != nil {
			// No more complete lines — put the partial back
			r.partial.Reset()
			r.partial.WriteString(line)
			break
		}
		// Store complete line (without trailing newline)
		r.addLine(strings.TrimRight(line, "\n"))
	}

	return len(p), nil
}

func (r *Ring) addLine(line string) {
	if len(line) > r.maxLineBytes {
		line = line[:r.maxLineBytes] + "... (truncated)"
	}
	r.lines[r.pos] = line
	r.pos = (r.pos + 1) % r.size
	if r.pos == 0 {
		r.full = true
	}
	r.generation++
}

// Lines returns all stored lines in order, oldest first.
func (r *Ring) Lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.full {
		result := make([]string, r.pos)
		copy(result, r.lines[:r.pos])
		return result
	}

	result := make([]string, r.size)
	copy(result, r.lines[r.pos:])
	copy(result[r.size-r.pos:], r.lines[:r.pos])
	return result
}

// Last returns the last n lines. If fewer lines exist, returns all of them.
func (r *Ring) Last(n int) []string {
	all := r.Lines()
	if n >= len(all) {
		return all
	}
	return all[len(all)-n:]
}

// Reader returns an io.Reader over the current buffer contents.
func (r *Ring) Reader() io.Reader {
	lines := r.Lines()
	return strings.NewReader(strings.Join(lines, "\n"))
}

// Since returns all lines written after gen, plus the current generation counter.
// Pass gen=0 to get all currently buffered lines.
// The returned generation can be passed to a subsequent Since call to receive only new lines.
func (r *Ring) Since(gen int) ([]string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	newGen := r.generation
	if gen >= newGen {
		return nil, newGen
	}

	// How many new lines since gen?
	newCount := newGen - gen

	// Cap at buffer capacity — older lines have been overwritten.
	total := r.pos
	if r.full {
		total = r.size
	}
	if newCount > total {
		newCount = total
	}

	// Collect the last newCount lines in order.
	result := make([]string, newCount)
	for i := 0; i < newCount; i++ {
		idx := (r.pos - newCount + i + r.size) % r.size
		result[i] = r.lines[idx]
	}
	return result, newGen
}
