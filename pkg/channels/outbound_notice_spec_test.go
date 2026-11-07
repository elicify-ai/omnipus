package channels

// T-16b / S-86/S-87 case1: real queue/dispatcher and real notice publisher.
// Only the external adapter and unrelated rate-limit clock are controlled.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func t16Wait(t *testing.T, condition func() bool, failure string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !condition() {
		select {
		case <-deadline.C:
			t.Fatal(failure)
		default:
			runtime.Gosched()
		}
	}
}

func t16LogFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notices.jsonl")
	previous := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	require.NoError(t, logger.EnableFileLogging(path))
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(previous) })
	return path
}

func t16QueueDrops(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.Count(string(raw), "Outbound queue full")
}

func TestT16b_FullQueue_StartPublishesNoNoticeWhileOrdinaryReplyStillDoes(t *testing.T) {
	for _, isStart := range []bool{true, false} {
		t.Run(map[bool]string{true: "start_no_notice", false: "ordinary_reply_control"}[isStart], func(t *testing.T) {
			drop := bus.OutboundMessage{Channel: "discord", ChatID: "222", Content: "QA dropped reply"}
			if isStart {
				drop = t15BookkeepingStart(t)
			}
			seed, _, _ := t15BookkeepingFixture()
			ctx, cancel := context.WithCancel(context.Background())
			m, mb := newDeliveryManager(ctx)
			t.Cleanup(mb.Close)
			t.Cleanup(cancel)
			m.mu.Lock()
			m.config = seed.config
			m.liveCfg.Store(seed.config)
			m.mu.Unlock()
			m.SetPolicySource(func() *config.Config { return seed.config })
			entered, release, fenceDone, finalDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			a := &mockChannel{BaseChannel: *NewBaseChannel("discord", nil, mb, nil)}
			a.SetRunning(true)
			a.sendFn = func(sendCtx context.Context, msg bus.OutboundMessage) error {
				switch msg.Content {
				case "QA first in flight":
					close(entered)
					select {
					case <-release:
					case <-sendCtx.Done():
						return sendCtx.Err()
					}
				case "QA queue fence":
					close(fenceDone)
				case "QA final notice fence":
					close(finalDone)
				}
				return nil
			}
			m.RegisterChannel("discord", a)
			m.mu.RLock()
			worker := m.workers["discord"]
			m.mu.RUnlock()
			// Rate policy is not under test; initialize before the first message.
			worker.limiter = rate.NewLimiter(rate.Inf, 1)
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(release) })
				stopCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				require.NoError(t, m.StopAll(stopCtx))
			})
			path := t16LogFile(t)
			require.NoError(t, mb.PublishOutbound(ctx, bus.OutboundMessage{Channel: "discord", ChatID: "222", Content: "QA first in flight"}))
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("instrument: first real worker Send did not block")
			}
			capacity := cap(worker.queue) // actual production capacity, not a smaller replacement.
			for i := 0; i < capacity; i++ {
				worker.queue <- bus.OutboundMessage{Channel: "discord", ChatID: "222", Content: "QA queued filler"}
			}
			require.Equal(t, capacity, len(worker.queue), "instrument: actual worker queue is full")
			require.NoError(t, mb.PublishOutbound(ctx, drop))
			t16Wait(t, func() bool { return t16QueueDrops(t, path) >= 1 }, "S-86: full-queue drop did not reach real enqueueOutbound")
			releaseOnce.Do(func() { close(release) })
			t16Wait(t, func() bool { return len(worker.queue) == 0 }, "instrument: released queue did not drain")
			require.NoError(t, mb.PublishOutbound(ctx, bus.OutboundMessage{Channel: "discord", ChatID: "222", Content: "QA queue fence"}))
			select {
			case <-fenceDone:
			case <-time.After(5 * time.Second):
				t.Fatal("instrument: queue completion fence did not Send")
			}
			require.NoError(t, mb.PublishOutbound(ctx, bus.OutboundMessage{Channel: "discord", ChatID: "222", Content: "QA final notice fence"}))
			select {
			case <-finalDone:
			case <-time.After(5 * time.Second):
				t.Fatal("instrument: final derived-notice bus fence did not Send")
			}
			a.mu.Lock()
			attempts := append([]bus.OutboundMessage(nil), a.sentMessages...)
			a.mu.Unlock()
			counts := make(map[string]int)
			for _, msg := range attempts {
				require.Equal(t, "222", msg.ChatID)
				counts[msg.Content]++
			}
			notice := "[message dropped: channel is overloaded, please retry]"
			notices := counts[notice]
			delete(counts, notice)
			require.Equal(t, map[string]int{"QA first in flight": 1, "QA queued filler": capacity, "QA queue fence": 1, "QA final notice fence": 1}, counts,
				"exact queue attempts; dropped start/reply never reaches Send")
			drops := t16QueueDrops(t, path)
			if isStart {
				require.Equal(t, 0, notices, "S-86: no start-derived notice reaches chat")
				require.Equal(t, 1, drops, "S-86: a wrongly published notice cannot hide by overflowing the still-full queue")
			} else {
				// Scheduler decides whether the notice itself finds a full queue.
				// Both exact outcomes prove publication; no-notify produces neither.
				require.True(t, (notices == 1 && drops == 1) || (notices == 0 && drops == 2),
					"positive control: exactly one reply notice is delivered or itself overflows; notices=%d drops=%d", notices, drops)
			}
		})
	}
}

type t16PermanentFailure struct{ *t15BookkeepingAdapter }

func (a *t16PermanentFailure) Send(_ context.Context, msg bus.OutboundMessage) error {
	a.events = append(a.events, t15DeliveryEvent{kind: "send", chat: msg.ChatID, content: msg.Content})
	return ErrSendFailed
}

func TestT16b_PermanentFailure_StartPublishesNoFallbackWhileOrdinaryReplyStillDoes(t *testing.T) {
	for _, isStart := range []bool{true, false} {
		t.Run(map[bool]string{true: "start_no_notice", false: "ordinary_reply_control"}[isStart], func(t *testing.T) {
			msg := bus.OutboundMessage{Channel: "discord", ChatID: "222", Content: "QA reply"}
			if isStart {
				msg = t15BookkeepingStart(t)
			}
			m, worker, base := t15BookkeepingFixture()
			mb := bus.NewMessageBus()
			t.Cleanup(mb.Close)
			m.bus = mb
			worker.ch = &t16PermanentFailure{t15BookkeepingAdapter: base}
			path := t16LogFile(t)
			m.sendWithRetry(context.Background(), "discord", worker, msg, false)
			require.Equal(t, []t15DeliveryEvent{{kind: "send", chat: "222", content: msg.Content}}, base.events,
				"ErrSendFailed is permanent: one actual attempt")
			// sendWithRetry and notifyDrop are synchronous; no dispatcher is
			// competing for this real bus, so this is a deterministic emptiness check.
			select {
			case actual := <-mb.OutboundChan():
				require.False(t, isStart, "S-87: terminally failed start published an unrestricted fallback")
				require.Equal(t, bus.OutboundMessage{Channel: "discord", ChatID: "222", Content: dropNoticePrefix + "[message delivery failed]"}, actual,
					"ordinary-reply positive control keeps exact existing notice")
			default:
				require.True(t, isStart, "positive control: ordinary failure must publish its real bus notice")
			}
			require.Equal(t, 0, len(mb.OutboundChan()), "zero extra fallback messages")
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, 1, strings.Count(string(raw), "Send failed"), "existing error log remains the operator's signal")
		})
	}
}
