package webrtc

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

// mediaForwarder owns the write boundary for one outgoing media kind.
// Switching ownership and writing packets use the same lock, so a retired
// source cannot write after its replacement or consume its sequence range.
// Never acquire Session.mu while holding this lock.
type mediaForwarder struct {
	mu                 sync.Mutex
	feed               int64
	seq                seqRewriter
	lastSeq            atomic.Uint32
	receivedPackets    atomic.Int64
	forwardFailures    atomic.Int64
	clockRate          uint32
	haveTimestamp      bool
	lastTimestamp      uint32
	lastAt             time.Time
	timestampOffset    uint32
	offsetReady        bool
	boundaryTimestamp  uint32
	forwardedTimestamp uint32
	haveForwarded      bool
}

func (f *mediaForwarder) begin(feed int64, clockRate uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feed = feed
	f.clockRate = clockRate
	f.seq = seqRewriter{lastOut: &f.lastSeq}
	f.offsetReady = false
	f.haveForwarded = false
}

func (f *mediaForwarder) retire() {
	f.mu.Lock()
	f.feed = 0
	f.mu.Unlock()
}

func (f *mediaForwarder) write(feed int64, pkt *rtp.Packet, now time.Time, write func(*rtp.Packet) error) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if feed == 0 || f.feed != feed {
		return false, nil
	}
	// Source progress is independent of an individual viewer's egress.
	f.receivedPackets.Add(1)
	if !f.offsetReady {
		f.timestampOffset = 0
		if f.haveTimestamp {
			elapsed := now.Sub(f.lastAt)
			if elapsed < 0 {
				elapsed = 0
			}
			// A long idle period must not jump by half the RTP serial space
			// (or wrap several times), which makes old frames compare newer.
			if elapsed > time.Second {
				elapsed = time.Second
			}
			ticks := uint32(elapsed.Seconds() * float64(f.clockRate))
			if ticks == 0 {
				ticks = 1
			}
			f.timestampOffset = f.lastTimestamp + ticks - pkt.Timestamp
		}
		f.offsetReady = true
		f.boundaryTimestamp = pkt.Timestamp + f.timestampOffset
	}
	pkt.SequenceNumber = f.seq.rewrite(pkt.SequenceNumber)
	pkt.Timestamp += f.timestampOffset
	if !f.haveTimestamp || (pkt.Timestamp != f.lastTimestamp && pkt.Timestamp-f.lastTimestamp < 0x80000000) {
		f.lastTimestamp = pkt.Timestamp
		f.lastAt = now
		f.haveTimestamp = true
	}
	err := write(pkt)
	if err != nil {
		f.forwardFailures.Add(1)
	}
	if err == nil {
		if !f.haveForwarded {
			// Retain the first mapped timestamp as a floor, even if an
			// earlier write partially failed and this packet is reordered.
			f.forwardedTimestamp = f.boundaryTimestamp
			f.haveForwarded = true
		}
		if pkt.Timestamp != f.forwardedTimestamp && pkt.Timestamp-f.forwardedTimestamp < 0x80000000 {
			f.forwardedTimestamp = pkt.Timestamp
		}
	}
	return true, err
}

func (f *mediaForwarder) senderReport(feed int64, sr *rtcp.SenderReport, write func(*rtcp.SenderReport)) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if feed == 0 || f.feed != feed || !f.offsetReady {
		return false
	}
	out := *sr
	out.RTPTime += f.timestampOffset
	write(&out)
	return true
}

// boundary snapshots only the current feed. Its latest marker stays fresh
// across long-lived captures; the first marker is for the one-time callback.
func (f *mediaForwarder) boundary(feed int64) (first uint32, latest uint32, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if feed == 0 || f.feed != feed || !f.haveForwarded {
		return 0, 0, false
	}
	return f.boundaryTimestamp, f.forwardedTimestamp, true
}
