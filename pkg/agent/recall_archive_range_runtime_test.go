//go:build goolm && stdjson

package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/memory"
)

// MIN-001 retained-memory plan:
//   - Generate each archive incrementally on disk from one fixed-size record.
//   - Keep record size, page cap, offset and length fixed; increase record count.
//   - Observe reachable heap DURING Execute. The assertion reads the peak
//     from samples taken after a forced garbage collection (the checkpoint
//     path). The periodic tick only reads heap stats: a full collection on
//     that tick is slower than the tick, and the meter would spend the call
//     measuring itself. Total allocation and final output size are not
//     retained-memory evidence.
//   - Include a positive control retained across a background observation.
//   - Independent CHECK must kill a full-concatenation and an all-records mutant.
//
// This observes aggregate Go heap, not a private decoder's exact scratch layout.
// It cannot certify "at most one record" by itself; that needs CHECK code-reading
// and the materialization mutants. Cancellation read checkpoints are a separate
// explicitly BLOCKED testability finding below, not faked by context-call counts.

type cwRangeHeapMeter struct {
	base    uint64
	peak    atomic.Uint64
	during  atomic.Bool
	samples atomic.Uint64
	inCall  atomic.Uint64
	forced  atomic.Uint64
	request chan chan struct{}
	stop    chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func cwNewRangeHeapMeter(t *testing.T) *cwRangeHeapMeter {
	t.Helper()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	m := &cwRangeHeapMeter{
		base: stats.HeapAlloc, request: make(chan chan struct{}),
		stop: make(chan struct{}), stopped: make(chan struct{}),
	}
	go func() {
		defer close(m.stopped)
		// A scheduling interval, not a performance pass/fail threshold.
		// The tick must not collect. On the 64MiB fixture one forced
		// collection costs longer than 2ms, so the sampler never keeps
		// up and the call spends itself in runtime.GC.
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-m.stop:
				return
			case ack := <-m.request:
				m.sample(true)
				close(ack)
			case <-ticker.C:
				m.sample(false)
			}
		}
	}()
	t.Cleanup(m.close)
	return m
}

// sample records one heap observation. forceGC is true only on the explicit
// checkpoint path. A periodic tick passes false and must not raise peak:
// HeapAlloc without a collection includes uncollected garbage, so treating
// it as retained memory would false-fail a bounded live set (churn on the
// longer range can exceed one EncodedLineBound even when the reachable set
// does not scale). It would not false-pass a range that stays reachable —
// that set survives the checkpoint collection and still raises peak. A tick
// that only bumps a counter is not an observation, so every tick still reads
// the heap.
func (m *cwRangeHeapMeter) sample(forceGC bool) {
	inCall := m.during.Load()
	if forceGC {
		runtime.GC()
		m.forced.Add(1)
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	if forceGC {
		var retained uint64
		if stats.HeapAlloc > m.base {
			retained = stats.HeapAlloc - m.base
		}
		for old := m.peak.Load(); retained > old; old = m.peak.Load() {
			if m.peak.CompareAndSwap(old, retained) {
				break
			}
		}
	}
	m.samples.Add(1)
	if inCall && m.during.Load() {
		m.inCall.Add(1)
	}
}

func (m *cwRangeHeapMeter) observe() {
	ack := make(chan struct{})
	m.request <- ack
	<-ack
}

func (m *cwRangeHeapMeter) close() {
	m.once.Do(func() {
		close(m.stop)
		<-m.stopped
	})
}

// Supplemental deterministic samples at ordinary context API boundaries. The
// background sampler still observes readers that cache Done() once. This context
// never drives cancellation and its method counts are NOT used as read counts.
type cwRangeHeapContext struct {
	context.Context
	meter *cwRangeHeapMeter
	polls atomic.Uint64
}

func (c *cwRangeHeapContext) checkpoint() {
	// A 64-check sampling stride reduces forced-GC overhead. It is not a
	// requirement on how frequently production must call a context method.
	if c.polls.Add(1)%64 == 0 {
		c.meter.observe()
	}
}

func (c *cwRangeHeapContext) Err() error {
	c.checkpoint()
	return c.Context.Err()
}

func (c *cwRangeHeapContext) Done() <-chan struct{} {
	c.checkpoint()
	return c.Context.Done()
}

func cwRangeMemoryObserverControl(t *testing.T) {
	m := cwNewRangeHeapMeter(t)
	// Four D10 bounds. The larger-archive test allows only ONE bound of
	// between-run measurement slack, so this control must clearly see more.
	block := make([]byte, 4*memory.EncodedLineBound)
	for i := range block {
		block[i] = byte(i)
	}
	m.during.Store(true)
	m.observe() // acknowledged observation on the background sampler
	m.during.Store(false)
	observed := m.peak.Load()
	if m.inCall.Load() == 0 || observed < 2*memory.EncodedLineBound {
		t.Fatalf("instrument cannot detect retained materialization: live=%d, during samples=%d, control allocation=%d", observed, m.inCall.Load(), len(block))
	}
	runtime.KeepAlive(block)
	m.close()
	t.Logf("positive control: retained=%d bytes, control=%d bytes, during samples=%d", observed, len(block), m.inCall.Load())
}

func cwRangeRepeatedStore(t *testing.T, key, record string, count int) *memory.JSONLStore {
	t.Helper()
	store, path := cwRangeStore(t, key)
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		if _, err := f.WriteString(record); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return store
}

func cwRangeBoundedMemory(t *testing.T) {
	const key = "cw-retained"
	const pageCap, offset, length = 1024, 3, 17
	// Fixed 8-KiB payload. Both maximum actual record size and the admitted
	// D10 bound are unchanged when the selected record count increases.
	record := "{\"role\":\"assistant\",\"content\":\"" + strings.Repeat("a", 8*1024) + "\"}\n"
	var baselinePeak uint64
	for _, count := range []int{256, 2048, 8192} {
		t.Run(fmt.Sprintf("records_%d", count), func(t *testing.T) {
			store := cwRangeRepeatedStore(t, key, record, count)
			tool := NewRecallConversationTool(store, cwNewRangeSink(pageCap))
			args := cwRangeArgs(0, count-1)
			args["offset"], args["length"] = offset, length
			meter := cwNewRangeHeapMeter(t)
			ctx := &cwRangeHeapContext{Context: makeCtx(key), meter: meter}
			meter.during.Store(true)
			res := tool.Execute(ctx, args)
			meter.during.Store(false)
			meter.close()
			if res == nil || res.IsError {
				t.Fatalf("MIN-001: large valid range/small page must stream successfully, got %+v", res)
			}
			page := cwReadRangePage(t, res)
			// No full-range oracle allocation: fixture arithmetic and one
			// small first-record slice independently determine every value.
			want := string([]rune(record)[offset : offset+length])
			if page.payload != want || page.total != count*utf8.RuneCountInString(record) || page.next != offset+length {
				t.Fatalf("literal page/total/next: want %q/%d/%d, got %+v", want, count*utf8.RuneCountInString(record), offset+length, page)
			}
			if utf8.RuneCountInString(res.ForLLM) > pageCap {
				t.Fatalf("framed page exceeds cap %d", pageCap)
			}
			if meter.inCall.Load() == 0 {
				t.Fatal("BLOCKED: no retained-heap sample occurred during Execute; cannot claim bounded memory from final output")
			}
			peak := meter.peak.Load()
			if count == 256 {
				baselinePeak = peak
			} else if peak > baselinePeak+memory.EncodedLineBound {
				// One existing D10 bound of conservative process/GC/buffer
				// measurement slack, NOT an arbitrary product range limit.
				// The 64-MiB selected range is much larger than this slack.
				t.Fatalf("retained working memory scales with range: records=%d peak=%d baseline=%d allowed growth=%d", count, peak, baselinePeak, memory.EncodedLineBound)
			}
			t.Logf("MIN-001: records=%d selected bytes=%d fixed record bytes=%d page cap=%d peak retained=%d during samples=%d forced collections=%d context polls=%d", count, count*len(record), len(record), pageCap, peak, meter.inCall.Load(), meter.forced.Load(), ctx.polls.Load())
		})
	}
}

func cwRangeCancellation(t *testing.T) {
	const key = "cw-cancel"
	const record = "{\"role\":\"assistant\",\"content\":\"page then counting\"}\n"
	store := cwRangeRepeatedStore(t, key, record, 1024)
	tool := NewRecallConversationTool(store, cwNewRangeSink(1024))
	for _, tc := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		want error
	}{
		{"already_canceled", func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(makeCtx(key))
			cancel()
			return ctx, cancel
		}, context.Canceled},
		{"already_expired_deadline", func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(makeCtx(key), time.Now().Add(-time.Second))
		}, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := tc.ctx()
			defer cancel()
			if !errors.Is(ctx.Err(), tc.want) {
				t.Fatalf("instrument error: caller context must actually be %v, got %v", tc.want, ctx.Err())
			}
			args := cwRangeArgs(0, 1023)
			args["length"] = 1
			res := tool.Execute(ctx, args)
			cwAssertRangeError(t, res, tc.want.Error())
			if res.Err != nil && !errors.Is(res.Err, tc.want) {
				t.Errorf("reported underlying error is not the genuine context cause: want %v got %v", tc.want, res.Err)
			}
		})
	}
	for _, tc := range []struct {
		name string
		want error
	}{
		{"cancel_after_page_stops_remaining_reads", context.Canceled},
		{"deadline_after_page_stops_remaining_reads", context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// ADR-066 MAJ-CW-003/MIN-001, B-58: the page is collected
				// before the cutoff; fixture sizes leave five unread records.
				const cutoff, recordCount = 3, 8
				records := make([]string, recordCount)
				readers := make([]*strings.Reader, recordCount)
				inputs := make([]io.Reader, recordCount)
				for idx := range records {
					records[idx] = fmt.Sprintf("{\"role\":\"assistant\",\"content\":\"record-%d ☃\"}\n", idx)
					readers[idx] = strings.NewReader(records[idx])
					inputs[idx] = readers[idx]
				}
				var ctx context.Context
				var cancel context.CancelFunc
				if errors.Is(tc.want, context.DeadlineExceeded) {
					ctx, cancel = context.WithTimeout(context.Background(), time.Second)
				} else {
					ctx, cancel = context.WithCancel(context.Background())
				}
				defer cancel()
				page := archiveRangePage{offset: 0, limit: 1}
				var committed []string
				callbacks := 0
				err := memory.ScanJSONLRange(ctx, io.MultiReader(inputs...), 0, recordCount-1,
					func(idx int, raw []byte, msg memory.ArchivedMessage) error {
						callbacks++
						if idx != callbacks-1 || idx < 0 || idx >= recordCount {
							return fmt.Errorf("record index: want %d, got %d", callbacks-1, idx)
						}
						if string(raw) != records[idx] || msg.Role != "assistant" || msg.Content != fmt.Sprintf("record-%d ☃", idx) {
							return fmt.Errorf("record %d literal/decoded data changed: raw=%q message=%+v", idx, raw, msg)
						}
						if err := page.collect(ctx, raw); err != nil {
							return err
						}
						committed = append(committed, string(raw))
						if callbacks == 1 && (page.payload.String() != "{" || page.returned != 1) {
							return fmt.Errorf("post-page instrument: first record must collect the one-rune page")
						}
						if callbacks == cutoff {
							if errors.Is(tc.want, context.Canceled) {
								cancel()
							} else {
								// Durable blocking advances fake time until the real timer expires.
								<-ctx.Done()
							}
						}
						return nil
					})
				if callbacks != cutoff {
					t.Fatalf("cutoff: want exactly %d callbacks, got %d", cutoff, callbacks)
				}
				wantPrefix := strings.Join(records[:cutoff], "")
				if got := strings.Join(committed, ""); len(committed) != cutoff || got != wantPrefix {
					t.Fatalf("committed prefix: want %d unchanged records %q, got %d records %q", cutoff, wantPrefix, len(committed), got)
				}
				if !errors.Is(err, ctx.Err()) || !errors.Is(err, tc.want) {
					t.Fatalf("genuine context cause: want exact %T %v = ctx.Err(), got %T %v; ctx.Err()=%v", tc.want, tc.want, err, err, ctx.Err())
				}
				if page.payload.String() != "{" || page.returned != 1 || page.total != utf8.RuneCountInString(wantPrefix) {
					t.Fatalf("processed page/count changed: payload=%q returned=%d total=%d, want %q/1/%d", page.payload.String(), page.returned, page.total, "{", utf8.RuneCountInString(wantPrefix))
				}
				for idx, reader := range readers {
					wantUnread := 0
					if idx >= cutoff {
						wantUnread = len(records[idx])
					}
					if got := reader.Len(); got != wantUnread {
						t.Errorf("unconsumed remainder: record %d want %d unread bytes, got %d", idx, wantUnread, got)
					}
				}
			})
		})
	}
}
