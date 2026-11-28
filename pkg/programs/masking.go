// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

import (
	"errors"
	"io"
	"sync"
)

// gramLen is the protection threshold: every substring of gramLen or more
// bytes of a token is a protected fragment (I-1). A fragment of n ≥ 8 bytes
// is a union of overlapping 8-byte windows, so the matcher only needs the set
// of 8-byte windows of every token.
const gramLen = 8

// holdBack is the most bytes the writer ever keeps undecided (gramLen − 1).
const holdBack = gramLen - 1

func tokenGrams(tokens []string) map[string]struct{} {
	grams := map[string]struct{}{}
	for _, t := range tokens {
		for i := 0; i+gramLen <= len(t); i++ {
			grams[t[i:i+gramLen]] = struct{}{}
		}
	}
	return grams
}

// maskingWriter is the program masking writer of invariant I-1 (B0).
type maskingWriter struct {
	mu    sync.Mutex
	dst   io.Writer
	grams map[string]struct{}
	// buf holds up to holdBack already-decided context bytes (buf[:decided])
	// followed by the undecided bytes (buf[decided:]).
	buf     []byte
	decided int
	// lastCovered reports whether the last decided byte was covered, so a
	// covered run continuing across writes stays ONE [FILTERED].
	lastCovered bool
	closed      bool
	err         error
}

// NewMaskingWriter wraps w in the program masking writer (I-1, B0): every
// maximal run of bytes covered by protected fragments — 8-or-more-byte
// substrings of any token in tokens — is replaced by one "[FILTERED]"; at most
// 7 trailing bytes are held back, and Close (or EOF in ReadFrom) releases only
// those ≤ 7 bytes, which are below the protection threshold. Matching is a
// hash lookup per 8-byte window, linear in the output length.
func NewMaskingWriter(w io.Writer, tokens []string) io.WriteCloser {
	return &maskingWriter{dst: w, grams: tokenGrams(tokens)}
}

// Write masks p and forwards every byte that can no longer be part of a
// protected fragment.
func (m *maskingWriter) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return 0, m.err
	}
	if m.closed {
		return 0, io.ErrClosedPipe
	}
	m.buf = append(m.buf, p...)
	if err := m.flushLocked(false); err != nil {
		return 0, err
	}
	return len(p), nil
}

// ReadFrom copies r through the writer and applies the close rule at EOF or
// on a read error. os/exec copies a child's output with io.Copy, which uses
// ReadFrom, so the held bytes are released before Cmd.Wait returns.
func (m *maskingWriter) ReadFrom(r io.Reader) (int64, error) {
	var n int64
	chunk := make([]byte, 32*1024)
	for {
		k, rerr := r.Read(chunk)
		if k > 0 {
			if _, werr := m.Write(chunk[:k]); werr != nil {
				return n, werr
			}
			n += int64(k)
		}
		if rerr != nil {
			cerr := m.Close()
			if errors.Is(rerr, io.EOF) {
				return n, cerr
			}
			return n, errors.Join(rerr, cerr)
		}
	}
}

// Close applies the rule to everything held and releases the remaining
// ≤ 7 bytes as they are. It is idempotent.
func (m *maskingWriter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return m.err
	}
	m.closed = true
	return m.flushLocked(true)
}

// flushLocked decides every byte that can be decided and writes the result.
// At final, nothing more can arrive, so every byte is decided.
func (m *maskingWriter) flushLocked(final bool) error {
	n := len(m.buf)
	covered := make([]bool, n)
	for i := 0; i+gramLen <= n; i++ {
		if _, ok := m.grams[string(m.buf[i:i+gramLen])]; ok {
			for j := i; j < i+gramLen; j++ {
				covered[j] = true
			}
		}
	}
	// Capacity is the undecided bytes; append grows it for a marker.
	out := make([]byte, 0, n-m.decided)
	j := m.decided
	for ; j < n; j++ {
		// A byte more than holdBack before the end can only be covered by a
		// window lying wholly inside buf, so it is decided; coverage never
		// shrinks, so a covered byte is decided too.
		if !final && !covered[j] && j >= n-holdBack {
			break
		}
		if covered[j] {
			if !m.lastCovered {
				out = append(out, FilteredMarker...)
			}
			m.lastCovered = true
			continue
		}
		out = append(out, m.buf[j])
		m.lastCovered = false
	}
	if len(out) > 0 {
		if _, err := m.dst.Write(out); err != nil {
			m.err = err
			return err
		}
	}
	keep := j - holdBack
	if keep < 0 {
		keep = 0
	}
	m.buf = append(m.buf[:0], m.buf[keep:]...)
	m.decided = j - keep
	return nil
}
