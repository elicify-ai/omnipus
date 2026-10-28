package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// healProofDeadlineContext is the timeout-clock edge, not a builder/read stub.
// Its real parent has the requested 30s timeout. Advancing the test clock uses
// WithCancelCause with a real expired WithDeadline's error; Err exposes that
// deadline cause rather than WithCancelCause's generic Canceled. Deadline and
// Done remain stable, and ordinary cancellation still reports Canceled.
type healProofDeadlineContext struct{ context.Context }

func (c *healProofDeadlineContext) Err() error {
	if c.Context.Err() == nil {
		return nil
	}
	return context.Cause(c.Context)
}

// FIX-ROUND-2 / PTA N1; design §§1,2,4: late deadline expiry must not undo a
// published index or permanently disable a later large-gap heal. The control
// uses the same publication/proof path without advancing the timeout clock.
func TestChatHistoryHeal_PostPublicationDeadlineIsTransientAndKeepsPublishedIndex(t *testing.T) {
	for _, tc := range []struct {
		name     string
		expiries int
	}{
		{"late_deadline_attempts_1_and_2", 2},
		{"normal_completion", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := selfHealCaptureLogs(t)
			f := newSelfHealFixture(t)
			base := time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)
			now := base
			oldClock, oldTimeout := f.store.historyHealNow, f.store.historyHealWithTimeout
			oldRead, oldPublication := chatHistoryReadHook, chatHistoryPublicationHook
			var cleanups []context.CancelFunc
			t.Cleanup(func() {
				chatHistoryReadHook, chatHistoryPublicationHook = oldRead, oldPublication
				f.store.historyHealNow, f.store.historyHealWithTimeout = oldClock, oldTimeout
				for _, cancel := range cleanups {
					cancel()
				}
			})
			f.store.historyHealNow = func() time.Time { return now }
			attempts, publications, proofs, expirations := 0, 0, 0, 0
			proofActive := false
			var internal *healProofDeadlineContext
			var expire context.CancelCauseFunc
			var initialDone <-chan struct{}
			var published, previous historyCheckpoint
			var publishedFile map[string]historyFileFact
			type requestKey struct{}
			f.store.historyHealWithTimeout = func(parent context.Context, duration time.Duration) (context.Context, context.CancelFunc) {
				attempts++
				proofActive = true
				require.Equal(t, 30*time.Second, duration, "design §2.4: the real attempt requests exactly 30s")
				require.Equal(t, f.sid, parent.Value(requestKey{}), "detachment preserves request values")
				require.Nil(t, parent.Done(), "the internal parent must not inherit caller cancellation")
				_, inherited := parent.Deadline()
				require.False(t, inherited, "the internal timeout is independent of the caller deadline")
				live, stopTimer := context.WithTimeout(parent, duration)
				controlled, cancel := context.WithCancelCause(live)
				expire = cancel
				internal = &healProofDeadlineContext{Context: controlled}
				initialDone = internal.Done()
				require.NotNil(t, initialDone)
				require.NoError(t, internal.Err(), "positive control: the full build starts BEFORE expiry")
				published = historyCheckpoint{}
				cleanup := func() { cancel(context.Canceled); stopTimer() }
				cleanups = append(cleanups, cleanup)
				return internal, cleanup
			}
			chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
				if e.SessionID == f.sid && e.Operation == "repair" && e.Point == "checkpoint_published" {
					require.NotNil(t, internal)
					require.NoError(t, internal.Err(), "deadline fault must not fire in Prepare or Publish")
					publications++
					require.Equal(t, attempts, publications, "one REAL full publication for each attempted heal")
					published = historyCheckpointOf(t, f.store, f.sid) // read only; no demand reconciliation
					require.Equal(t, e.IndexGeneration, published.Generation, "positive control: checkpoint is already durable")
					require.NotEqual(t, previous.Generation, published.Generation)
					require.NotEqual(t, previous.File, published.File, "a full rebuild, not an incremental update, published")
					path := filepath.Join(historyIndexDir(f.store, f.sid), "generations", published.File)
					require.FileExists(t, path)
					publishedFile = historyTree(t, path)
				}
				if oldPublication != nil {
					return oldPublication(e)
				}
				return nil
			}
			chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
				if proofActive && e.SessionID == f.sid && e.Scope == "recovery" && e.Purpose == "anchor" && e.Phase == "record_opened" && published.Generation != "" && e.Cut.generation == published.Generation {
					require.Same(t, internal, ctx, "the post-publication proof must use the bounded internal context")
					proofs++
					require.Equal(t, attempts, proofs, "positive control: proof reads the actually published generation once")
					require.NotNil(t, e.Address)
					require.Equal(t, f.addresses[len(f.addresses)-1], *e.Address, "the actual proof opened the latest rebuilt canonical anchor")
					require.NoError(t, internal.Err(), "expiry is controlled only at this late proof boundary")
					if attempts <= tc.expiries {
						// No sleep or fake builder error: advance only the timeout
						// dependency, after actual publication and at real proof I/O.
						expired, cancel := context.WithDeadline(context.WithoutCancel(ctx), time.Time{})
						defer cancel()
						require.ErrorIs(t, expired.Err(), context.DeadlineExceeded)
						expire(expired.Err())
						expirations++
						require.Equal(t, initialDone, internal.Done(), "a controlled timeout must keep the same Done channel")
						select {
						case <-internal.Done():
						default:
							t.Fatal("timeout instrument failed to signal expiry")
						}
						require.ErrorIs(t, internal.Err(), context.DeadlineExceeded)
					}
				}
				if oldRead != nil {
					return oldRead(ctx, e)
				}
				return nil // the REAL reader observes ctx.Err(), not a fabricated hook error
			}

			for attempt := 1; attempt <= tc.expiries+1; attempt++ {
				if attempt > 1 {
					healProofLeaveUniqueGap(t, f, attempt)
					now = base.Add(time.Duration(attempt-1) * 60 * time.Second) // design §1: last-attempt backoff
				}
				_, original := f.store.reconcileChatHistoryIndex(context.Background(), f.sid)
				selfHealRequireUnavailable(t, original, "catchup_limit")
				previous = historyCheckpointOf(t, f.store, f.sid)
				canonical := partitionBytes(t, f.store, f.sid)
				request, cancel := context.WithTimeout(context.WithValue(context.Background(), requestKey{}, f.sid), time.Hour)
				err := f.store.CheckChatHistoryReadiness(request, f.sid)
				proofActive = false
				cancel()
				require.Equal(t, attempt, attempts, "a later eligible >64 gap must heal without restarting this store")
				require.Equal(t, attempt, publications)
				require.Equal(t, attempt, proofs, "success or deadline must follow the actual post-publication proof")
				require.Equal(t, published, historyCheckpointOf(t, f.store, f.sid), "a late timeout must keep, not roll back, the published index")
				require.Equal(t, publishedFile, historyTree(t, filepath.Join(historyIndexDir(f.store, f.sid), "generations", published.File)), "the actual published generation's bytes and metadata must survive the late proof outcome")
				healFixRequireAddresses(t, f.store, f.sid, f.addresses) // before any second readiness/page call
				require.Equal(t, canonical, partitionBytes(t, f.store, f.sid), "deadline handling cannot modify accepted canonical bytes")
				owner := f.store.historyOwner(f.sid)
				owner.mu.Lock()
				state := owner.heal
				owner.mu.Unlock()
				require.False(t, state.latched, "late deadline is transient; even a fresh successful page must not hide a permanent latch")
				warnings := selfHealEvents(t, logs, f.sid, selfHealBadLog)
				if attempt <= tc.expiries {
					selfHealRequireUnavailable(t, err, "catchup_limit")
					require.Equal(t, original.Error(), err.Error(), "late deadline returns the original bounded-reconcile failure")
					require.Nil(t, errors.Unwrap(err), "the internal proof's deadline must not replace the original public error")
					require.Equal(t, attempt, state.attempts)
					require.Equal(t, now, state.last)
					require.Len(t, warnings, attempt)
					require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealOKLog), "publication alone is not a completed heal")
				} else {
					require.NoError(t, err, "removing late expiry permits the later large-gap heal without restart")
					require.Equal(t, chatHistoryHealState{}, state, "a successful heal clears its bounded retry state")
					require.Len(t, warnings, tc.expiries)
					ok := selfHealEvents(t, logs, f.sid, selfHealOKLog)
					require.Len(t, ok, 1)
					selfHealRequireLogBase(t, ok[0], f.sid, int64(attempt))
				}
				for i, warning := range warnings {
					selfHealRequireLogBase(t, warning, f.sid, int64(i+1))
					require.Equal(t, "deadline", warning["class"], "post-publication expiry is NOT permanent readiness failure")
					require.Equal(t, "after_backoff", warning["retry"], "design §§1,4: deadline attempts 1 and 2 remain retryable")
				}
				select {
				case chatHistoryHealSlot <- struct{}{}:
					<-chatHistoryHealSlot
				default:
					t.Fatal("late deadline/success leaked the capacity-one rebuild slot")
				}
				index := historyTree(t, historyIndexDir(f.store, f.sid))
				selfHealRequireIndexed(t, f)
				selfHealRequirePages(t, f) // genuinely fresh requests use the kept complete index
				require.Equal(t, attempt, attempts, "fresh pages must not need another full rebuild")
				require.Equal(t, index, historyTree(t, historyIndexDir(f.store, f.sid)), "fresh reads must keep the published no-write fixed point")
				require.Equal(t, canonical, partitionBytes(t, f.store, f.sid))
				owner.mu.Lock()
				afterRead := owner.heal
				owner.mu.Unlock()
				require.Equal(t, state, afterRead, "a normal read must not conceal the previous attempt's hidden latch or reset its retry count")
			}
			require.Equal(t, tc.expiries, expirations, "positive control: both late-expiry clocks advanced, or neither in the normal control")
			selfHealRequirePrivateLogs(t, logs, f)
		})
	}
}

// Design §2: a real busy owner permits canonical acceptance without publishing
// derived updates. Exactly 65 unique new records exceed the online 64-fact bound.
func healProofLeaveUniqueGap(t *testing.T, f *selfHealFixture, attempt int) {
	t.Helper()
	before := historyCheckpointOf(t, f.store, f.sid)
	owner := f.store.historyOwner(f.sid)
	func() {
		owner.mu.Lock()
		defer owner.mu.Unlock()
		for i := 0; i < 65; i++ {
			id := fmt.Sprintf("late-proof-attempt-%d-row-%02d", attempt, i)
			address, err := f.store.AppendTranscriptAddressed(f.sid, historyUserEntry(id, selfHealBody, len(f.addresses)))
			require.NoError(t, err, "the real held owner must not prevent canonical append+fsync")
			f.addresses = append(f.addresses, address)
		}
	}()
	require.Equal(t, before, historyCheckpointOf(t, f.store, f.sid), "all 65 real accepted records must still be unindexed")
}
