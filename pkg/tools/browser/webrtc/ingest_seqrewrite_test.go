// ingest_seqrewrite_test.go — unit tests for seqRewriter (2026-08-13), the
// constant-offset sequence rewrite that replaced read-order renumbering.
// Read-order renumbering hid ingest-leg packet loss from the viewer (no gap
// -> no NACK, no prompt PLI) and spliced late-retransmitted packets into the
// wrong bitstream position with clean numbering — operator-visible as
// persistent macroblock corruption during scroll.

package webrtc

import (
	"sync/atomic"
	"testing"
)

func TestSeqRewrite_PreservesGapsAndOrderWithinConnection(t *testing.T) {
	var hw atomic.Uint32
	hw.Store(1000)
	r := seqRewriter{lastOut: &hw}

	// Inbound stream with a loss gap (17,18 missing) and a late reordered
	// packet (17 arrives after 19).
	in := []uint16{10, 11, 12, 16, 19, 17, 20}
	out := make([]uint16, len(in))
	for i, seq := range in {
		out[i] = r.rewrite(seq)
	}
	base := out[0]
	for i, seq := range in {
		wantDelta := seq - in[0]
		if out[i]-base != wantDelta {
			t.Fatalf("packet %d (in=%d): offset must be constant; got out=%d, want base+%d=%d",
				i, seq, out[i], wantDelta, base+wantDelta)
		}
	}
	if out[0] != 1000+seqReconnectGap {
		t.Fatalf("first outgoing seq must be highwater+gap: got %d, want %d", out[0], 1000+seqReconnectGap)
	}
	// The late packet 17 must map BELOW the already-forwarded 19/20 — the
	// viewer's jitter buffer needs the true position, not read order.
	if !seq16Ahead(out[4], out[5]) {
		t.Fatalf("late packet must keep its true (earlier) position: out[19]=%d must be ahead of out[17]=%d", out[4], out[5])
	}
	// High-water mark = the max forwarded (for 20), not the last written.
	if got := uint16(hw.Load()); got != out[6] {
		t.Fatalf("high-water mark must track the MAX outgoing seq, got %d want %d", got, out[6])
	}
}

func TestSeqRewrite_ReconnectContinuesAheadOfPreviousConnection(t *testing.T) {
	var hw atomic.Uint32
	r1 := seqRewriter{lastOut: &hw}
	var last uint16
	for _, seq := range []uint16{40000, 40001, 40002} {
		last = r1.rewrite(seq)
	}
	// New connection with a completely different randomized starting seq.
	r2 := seqRewriter{lastOut: &hw}
	first := r2.rewrite(3)
	if !seq16Ahead(first, last) {
		t.Fatalf("reconnected stream must continue AHEAD of the previous one: first=%d last=%d", first, last)
	}
	if first-last != seqReconnectGap {
		t.Fatalf("reconnect must open exactly seqReconnectGap: got %d, want %d", first-last, seqReconnectGap)
	}
	// A straggler from the OLD connection after the switch must not drag
	// the high-water mark backwards.
	r1.rewrite(40003)
	if got := uint16(hw.Load()); got != first {
		t.Fatalf("old-connection straggler dragged the high-water mark: got %d, want %d", got, first)
	}
}

func TestSeqRewrite_WrapAround(t *testing.T) {
	var hw atomic.Uint32
	hw.Store(65530)
	r := seqRewriter{lastOut: &hw}
	a := r.rewrite(100) // 65530+gap wraps past 65535
	b := r.rewrite(101)
	if b-a != 1 {
		t.Fatalf("increments must survive 16-bit wraparound: a=%d b=%d", a, b)
	}
	if !seq16Ahead(b, 65530) {
		t.Fatalf("wrapped seq %d must still be AHEAD of 65530 in RFC 1982 space", b)
	}
}

func TestSeq16Ahead(t *testing.T) {
	cases := []struct {
		a, b uint16
		want bool
	}{
		{1, 0, true}, {0, 1, false}, {5, 5, false},
		{0, 65535, true},   // wraparound: 0 is one ahead of 65535
		{32767, 0, true},   // just inside the forward half
		{32768, 0, false},  // exactly half the space is "behind"
		{100, 65530, true}, // reconnect-gap style forward wrap
	}
	for _, c := range cases {
		if got := seq16Ahead(c.a, c.b); got != c.want {
			t.Errorf("seq16Ahead(%d,%d) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
