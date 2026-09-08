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
// Control admission acquires writer locks before Session.mu. Never wait for
// this lock while holding Session.mu. Receipt publication uses its own short lock.
type mediaForwarder struct {
	mu              mediaWriterLock
	feed            int64
	seq             seqRewriter
	lastSeq         atomic.Uint32
	receivedPackets atomic.Int64
	forwardFailures atomic.Int64
	// receiptIdentity belongs to feed and is protected by mu. receiptMu
	// guards only the latest accepted-packet copy, never external writer IO.
	receiptIdentity    VideoReceipt
	receiptMu          sync.Mutex
	receipt            VideoReceipt
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
	f.beginWithReceipt(feed, clockRate, VideoReceipt{})
}

func (f *mediaForwarder) beginWithReceipt(feed int64, clockRate uint32, identity VideoReceipt) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beginWithReceiptLocked(feed, clockRate, identity)
}

// beginWithReceiptLocked requires writer ownership. Historical receipt/serial
// remain intact; only the identity of subsequent accepted packets changes.
func (f *mediaForwarder) beginWithReceiptLocked(feed int64, clockRate uint32, identity VideoReceipt) {
	f.feed = feed
	f.receiptIdentity = identity
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

// retireIfFeed cannot let late cleanup retire a successor's ownership.
func (f *mediaForwarder) retireIfFeed(feed int64) {
	if feed == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.feed == feed {
		f.feed = 0
	}
}

func (f *mediaForwarder) write(feed int64, pkt *rtp.Packet, now time.Time, write func(*rtp.Packet) error) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if feed == 0 || f.feed != feed {
		return false, nil
	}
	// Source progress is independent of an individual viewer's egress.
	f.receivedPackets.Add(1)
	f.receiptMu.Lock()
	// Exhaustion must not wrap and make an ancient serial appear fresh.
	// Keep the last valid receipt rather than publish a reused serial.
	if f.receipt.Serial != ^uint64(0) {
		next := f.receiptIdentity
		next.Serial = f.receipt.Serial + 1
		f.receipt = next
	}
	f.receiptMu.Unlock()
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

// latestReceipt copies a complete identity without waiting for the packet writer.
func (f *mediaForwarder) latestReceipt() VideoReceipt {
	f.receiptMu.Lock()
	defer f.receiptMu.Unlock()
	return f.receipt
}
