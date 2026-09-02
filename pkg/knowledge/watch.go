// Filesystem watching for a knowledge collection — the "instant" half of
// docs/internal/design/knowledge-index-freshness.md.
//
// The governing sentence, quoted because everything here follows from it:
//
//	The watcher is an optimisation. It is never the source of truth.
//
// Correctness rests on Index.UpdatePath/RemovePath/SyncWith — the content
// hash and the incremental sweep — all of which already exist and already
// work (index.go). A missed watcher event costs LATENCY, not CORRECTNESS: it
// is caught by whatever periodic or startup sweep the caller already runs.
// That is what makes every shortcut in this file safe to take: an OS watch
// limit, a dropped kernel event, an unsupported platform, a directory removed
// out from under an open watch handle — none of them can make the index
// wrong, only slower to catch up.
//
// # What this file deliberately does not do
//
//   - No self-event suppression. Omnipus's own writes trigger this watcher
//     like any other write; the event arrives, UpdatePath/RemovePath hash the
//     file, find nothing changed, and do nothing. Building ownership
//     tracking to filter those events out would be solving a bug that the
//     hash check already prevents from existing (design §3).
//   - No watch cap, no size-based degradation. The design measured the real
//     vault (3,002 files, 385 directories) and found the limiting resource is
//     event RATE, not file count (design §6) — see the burst escalation
//     below, which is the actual answer to load.
//   - No document body is ever read here. Every content read happens inside
//     Index.UpdatePath, by way of the same indexOneFile path SyncWith uses;
//     this file only ever computes a collection-relative path string.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// fsEvent is one filesystem change, already translated to a collection-
// relative, slash-separated path by the platform backend. It says nothing
// about WHY the change happened (edit vs. create vs. atomic-replace-rename
// all collapse to the same "removed=false" shape) because Index.UpdatePath
// does not need to know why — only what the file now contains, which it
// reads for itself.
type fsEvent struct {
	// relPath is collection-relative and slash-separated, matching
	// ScanEntry.RelPath and the argument UpdatePath/RemovePath expect.
	relPath string
	// removed is true when the platform backend is confident the path no
	// longer exists (a delete, or the "from" side of a rename). It is a
	// hint, not a guarantee — applyOne re-derives the true state from
	// UpdatePath's own fs.ErrNotExist rather than trusting this blindly,
	// because a raced create-then-delete can make even a confident "removed"
	// stale by the time the debounce timer fires.
	removed bool
}

// WatchUnavailableError is returned by Start, and is what a caller should
// errors.As-match to distinguish "the platform/environment cannot watch"
// from an ordinary setup failure — design §8's "that must be observable".
type WatchUnavailableError struct {
	// Reason is a short, human-readable cause: "unsupported platform",
	// "failed to start", etc.
	Reason string
	// Err is the underlying error, if any (a syscall failure, a permission
	// error). May be nil — an unsupported platform has no underlying error,
	// only a reason.
	Err error
}

func (e *WatchUnavailableError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("knowledge: filesystem watching unavailable (%s): %v", e.Reason, e.Err)
	}
	return fmt.Sprintf("knowledge: filesystem watching unavailable (%s)", e.Reason)
}

func (e *WatchUnavailableError) Unwrap() error { return e.Err }

// startPlatformWatch is implemented once per build by each platform backend
// (watch_linux.go, watch_darwin.go, watch_other.go) — a plain package-level
// function rather than an interface, because exactly one implementation is
// ever compiled into a given binary; the build tags themselves are the
// dispatch.
//
// It begins watching root recursively. It blocks only long enough to
// establish the initial watch set (or fail outright) — Start treats a
// non-nil error here as "cannot start", design §8's first case. Once it
// returns successfully, translated events are delivered on out and
// burst/overflow signals (the backend's own event queue overflowed, e.g.
// Linux's IN_Q_OVERFLOW) are delivered on overflow, until stop is closed.
//
// runErr receives at most one value — a non-nil error — if the backend dies
// on its own before stop is closed (a read error, a lost watch); it is never
// sent otherwise, and is always closed when the backend's own goroutine
// exits, so a caller can select on it without leaking. That is design §8's
// second case: watching STOPPING must be observable too, not just watching
// never starting.
//
//	func startPlatformWatch(root string, out chan<- fsEvent, overflow chan<- struct{}, stop <-chan struct{}) (runErr <-chan error, err error)

// Tunables. Every value here is a deliberate number with a reason, not a
// guess — see each constant's comment. A caller may override any of them via
// WatchOptions; these are only the defaults.
const (
	// DefaultWatchDebounce is the quiet period a single path must go without
	// a new event before its change is applied (design §5's "same mechanism
	// at small scale" as the burst escalation below).
	//
	// 300ms is comfortably longer than the few-millisecond-to-tens-of-
	// millisecond spread between the create/write/rename events a single
	// atomic-replace save produces — the pattern iCloud sync and every
	// "safe save" editor uses (design §7) — so one logical save collapses to
	// one UpdatePath call instead of two or three. It is still far below
	// anything a person perceives as latency. That distinction matters
	// because the requirement that a write be INSTANT (design §4) is met by
	// the direct UpdatePath call a vault tool makes before its own write
	// returns — this debounce only ever applies to the watcher's path (UI
	// uploads, external edits), which design §4 explicitly says does not
	// need to win a race against the same millisecond.
	DefaultWatchDebounce = 300 * time.Millisecond

	// DefaultBurstWindow and DefaultBurstThreshold together define "a lot of
	// events fast" (design §5): DefaultBurstThreshold raw filesystem events
	// inside a rolling DefaultBurstWindow escalate the watcher from
	// per-file updates to one SyncWith, and further events are absorbed by
	// that sweep rather than queued as individual updates.
	//
	// 100 events / 2s = 50 events/sec sustained. A single interactive save
	// produces on the order of 2-6 raw filesystem events per platform
	// backend (temp file create, write, rename over target), so even ten
	// files saved in quick succession — a real but ordinary editing burst —
	// stays under ~60 events, comfortably below the threshold: ordinary
	// editing never escalates. A genuine bulk change (a git checkout
	// switching branches, an import, a vault reorganisation touching
	// dozens of files) crosses 100 events within a couple of seconds
	// almost immediately. At that point one guaranteed-complete SyncWith
	// pass (design §5: "the sweep is both cheaper and more reliable" than
	// the events, because the event stream may already have dropped
	// something) beats paying a manifest load+save round trip per file for
	// a hundred-plus files.
	DefaultBurstWindow    = 2 * time.Second
	DefaultBurstThreshold = 100

	// defaultOpTimeout bounds a single watcher-triggered UpdatePath or
	// RemovePath call, so one pathological file (huge, on a stalled network
	// mount under the collection root) cannot wedge the debounce loop
	// indefinitely and starve every other pending path. It does not bound
	// SyncWith — the escalation sweep uses the Watcher's own lifetime
	// context instead, which Stop cancels.
	defaultOpTimeout = 30 * time.Second

	// rawEventBuffer and dueBuffer size the internal channels generously
	// enough that a short burst never blocks the platform backend's own
	// read loop before the burst-detection logic (which runs on every
	// receive) has a chance to see it and escalate. They are not a cap on
	// how many changes the watcher can see — see the burst/debounce logic
	// in run(), which drains without ever refusing to receive.
	rawEventBuffer = 256
	dueBuffer      = 256
)

// WatchOptions tunes one Watcher. Every field's zero value selects the
// documented default.
type WatchOptions struct {
	// Debounce is the per-path quiet period. See DefaultWatchDebounce.
	Debounce time.Duration
	// BurstWindow and BurstThreshold set the escalation trigger. See
	// DefaultBurstWindow / DefaultBurstThreshold.
	BurstWindow    time.Duration
	BurstThreshold int
	// Logger receives every observable event this file promises (design
	// §8): watch start/stop, escalation to a sweep, a per-file update or
	// remove failure, and unavailability. Defaults to slog.Default().
	Logger *slog.Logger
}

// Watcher watches one collection root for filesystem changes and keeps its
// Index fresh — see the package-level "governing sentence" above for what it
// is, and is not, relied on for.
type Watcher struct {
	ix     *Index
	logger *slog.Logger

	debounce       time.Duration
	burstWindow    time.Duration
	burstThreshold int
	opTimeout      time.Duration

	rawEvents chan fsEvent
	overflow  chan struct{}
	runErrCh  <-chan error

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once

	sweepCancel context.CancelFunc
	sweepCtx    context.Context

	mu      sync.Mutex
	started bool
	lastErr error

	unavailableCh chan struct{}
	unavailOnce   sync.Once

	// testOnApply and testOnSweepStart are test-only instrumentation seams:
	// nil in every production Watcher (NewWatcher never sets them), so they
	// cost one nil check per call and are never user-visible. They exist
	// because the alternative — a test inferring "debounce collapsed N
	// events into one" or "a burst escalated to a sweep" purely from final
	// index state — cannot actually distinguish that from "every event was
	// applied individually and just happened to converge on the same
	// state", which is precisely the failure mode design §5 exists to rule
	// out. See watch_test.go.
	testOnApply      func(relPath string, removed bool)
	testOnSweepStart func()
}

// watchBackend is the platform backend Start uses to begin watching. It
// defaults to startPlatformWatch — implemented once per build by
// watch_linux.go/watch_darwin.go/watch_other.go — and this indirection is
// NOT an abstraction over multiple real backends; it exists purely so tests
// in this package can substitute a stub to exercise Start's and run's
// unavailable/failure handling deterministically, without needing to
// actually break the underlying OS facility (exhausting the inotify watch
// limit, revoking a kqueue permission, ...). Production code always goes
// through the real platform implementation; nothing outside _test.go files
// ever reassigns this.
var watchBackend = startPlatformWatch

// NewWatcher builds a Watcher over ix. It does nothing on disk and starts no
// goroutine until Start is called.
func NewWatcher(ix *Index, opts WatchOptions) *Watcher {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	debounce := opts.Debounce
	if debounce <= 0 {
		debounce = DefaultWatchDebounce
	}
	burstWindow := opts.BurstWindow
	if burstWindow <= 0 {
		burstWindow = DefaultBurstWindow
	}
	burstThreshold := opts.BurstThreshold
	if burstThreshold <= 0 {
		burstThreshold = DefaultBurstThreshold
	}
	return &Watcher{
		ix:             ix,
		logger:         logger,
		debounce:       debounce,
		burstWindow:    burstWindow,
		burstThreshold: burstThreshold,
		opTimeout:      defaultOpTimeout,
		rawEvents:      make(chan fsEvent, rawEventBuffer),
		overflow:       make(chan struct{}, 4),
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
		unavailableCh:  make(chan struct{}),
	}
}

// Start begins watching ix.Root(). It returns a *WatchUnavailableError,
// unwrappable to the underlying cause, when watching cannot be established
// at all — an unsupported platform (watch_other.go), or a real error from the
// platform backend (permission denied, the OS watch/fd limit exhausted). That
// is design §8's mandate that a failure to start is STATED, never inferred:
// the caller receives it synchronously, from this call, and can fall back to
// relying entirely on its own periodic sweep.
//
// ctx governs Start's caller-visible lifetime, not the watcher's own
// long-running goroutines: if ctx is cancelled, the Watcher stops (as if Stop
// had been called) rather than leaking. Passing context.Background() ties the
// Watcher's lifetime to Stop alone.
//
// Start may be called at most once per Watcher.
func (w *Watcher) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return errors.New("knowledge: watcher already started")
	}
	w.started = true
	w.mu.Unlock()

	w.sweepCtx, w.sweepCancel = context.WithCancel(context.Background())

	runErr, err := watchBackend(w.ix.Root(), w.rawEvents, w.overflow, w.stopCh)
	if err != nil {
		var unavailable *WatchUnavailableError
		if !errors.As(err, &unavailable) {
			err = &WatchUnavailableError{Reason: "failed to start", Err: err}
		}
		w.markUnavailable(err)
		w.sweepCancel()
		close(w.doneCh)
		return err
	}
	w.runErrCh = runErr

	w.logger.Info("knowledge: filesystem watching started", "root", w.ix.Root())
	go w.run()

	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				w.Stop()
			case <-w.doneCh:
			}
		}()
	}
	return nil
}

// Stop shuts the watcher down: the platform backend, any in-flight
// escalation sweep, and the debounce/burst goroutine. It blocks until all of
// that has actually finished — no goroutine leak, no leaked file descriptor —
// so a caller can rely on it as a genuine join point, e.g. in a defer right
// after a successful Start. Safe to call more than once, and safe to call
// after a failed Start.
func (w *Watcher) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
		if w.sweepCancel != nil {
			w.sweepCancel()
		}
	})
	<-w.doneCh
}

// Unavailable returns a channel that is closed exactly once: when watching
// stops working, whether that happens before Start ever succeeds (a failed
// Start also closes it) or later, mid-run (the platform backend's own
// goroutine died). Err reports why. A caller must treat a close on this
// channel as "watching is off starting now" and rely on its own periodic
// sweep to compensate — design §8.
func (w *Watcher) Unavailable() <-chan struct{} { return w.unavailableCh }

// Err reports the reason watching became unavailable, or nil if it still is
// (or never started failing). It is safe to call at any time, including
// before Start.
func (w *Watcher) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastErr
}

func (w *Watcher) markUnavailable(err error) {
	w.mu.Lock()
	w.lastErr = err
	w.mu.Unlock()
	w.unavailOnce.Do(func() {
		close(w.unavailableCh)
	})
	w.logger.Error("knowledge: filesystem watching unavailable", "root", w.ix.Root(), "error", err)
}

// run is the single goroutine that owns every mutable field below its local
// variables: the pending-path map, the per-path debounce timers, and the
// burst window counter. Keeping all of that state local to one goroutine,
// touched only from this select loop, is what lets the rest of this file
// avoid a mutex for it entirely — the only cross-goroutine handoffs are the
// channels themselves (rawEvents, overflow, runErrCh, stopCh, dueCh, and the
// escalation sweep's own doneCh), which is exactly the set this loop selects
// on.
func (w *Watcher) run() {
	defer close(w.doneCh)

	pending := make(map[string]bool) // relPath -> removed
	timers := make(map[string]*time.Timer)
	dueCh := make(chan string, dueBuffer)

	var burstCount int
	var burstWindowStart time.Time
	sweeping := false
	sweepAgain := false
	var sweepDoneCh chan struct{} // nil until a sweep is running; a nil channel's case never fires

	stopAllTimers := func() {
		for p, t := range timers {
			t.Stop()
			delete(timers, p)
		}
		for p := range pending {
			delete(pending, p)
		}
	}

	startSweep := func() {
		sweeping = true
		if w.testOnSweepStart != nil {
			w.testOnSweepStart()
		}
		done := make(chan struct{})
		sweepDoneCh = done
		go func() {
			defer close(done)
			w.runSweep()
		}()
	}

	// countBurst records one raw event and reports whether the rolling
	// window has now crossed the escalation threshold. The window resets
	// whenever it has been quiet longer than w.burstWindow, so a threshold
	// crossing always reflects genuinely CONCURRENT activity, not an
	// accumulation from an hour of ordinary, well-spaced edits.
	countBurst := func() bool {
		now := time.Now()
		if burstWindowStart.IsZero() || now.Sub(burstWindowStart) > w.burstWindow {
			burstWindowStart = now
			burstCount = 0
		}
		burstCount++
		return burstCount >= w.burstThreshold
	}

	for {
		select {
		case <-w.stopCh:
			stopAllTimers()
			return

		case err, ok := <-w.runErrCh:
			if ok && err != nil {
				w.markUnavailable(err)
			}
			stopAllTimers()
			return

		case <-w.overflow:
			// The backend's own event queue overflowed: by construction we
			// have already lost information about what changed, so there is
			// nothing to debounce — only a sweep can be trusted (design §5).
			if sweeping {
				sweepAgain = true
			} else {
				stopAllTimers()
				startSweep()
			}

		case ev, ok := <-w.rawEvents:
			if !ok {
				stopAllTimers()
				return
			}
			crossed := countBurst()
			if sweeping {
				if crossed {
					sweepAgain = true
				}
				continue
			}
			if crossed {
				stopAllTimers()
				startSweep()
				continue
			}
			relPath := ev.relPath
			pending[relPath] = ev.removed
			if t, exists := timers[relPath]; exists {
				// Same path changed again inside its own quiet period: reset
				// the clock rather than adding a second timer. This is the
				// debounce half of design §5's "one rule at two scales" —
				// N saves to one file collapse to one update.
				t.Reset(w.debounce)
			} else {
				timers[relPath] = time.AfterFunc(w.debounce, func() {
					select {
					case dueCh <- relPath:
					case <-w.stopCh:
					}
				})
			}

		case relPath := <-dueCh:
			delete(timers, relPath)
			removed, ok := pending[relPath]
			if !ok {
				// Superseded: a sweep already cleared pending for this path
				// (stopAllTimers), or it was already applied via some other
				// path. Either way there is nothing left to do — and doing
				// nothing here is safe precisely because the hash check
				// inside UpdatePath makes re-applying it a no-op anyway, so
				// this is a latency optimisation, not a correctness one.
				continue
			}
			delete(pending, relPath)
			if sweeping {
				// A sweep is already in flight (or about to run again) and
				// will cover this path completely; applying it separately
				// here would just race the sweep's own manifest read-modify-
				// write for no benefit.
				continue
			}
			w.applyOne(relPath, removed)

		case <-sweepDoneCh:
			sweepDoneCh = nil
			sweeping = false
			if sweepAgain {
				sweepAgain = false
				startSweep()
			}
		}
	}
}

// applyOne is design §3's "small change -> per-file update", the ONLY place
// this package calls UpdatePath/RemovePath on the watcher's behalf. It never
// re-implements what UpdatePath/RemovePath already do (segmentation,
// FR-039a's zero-content-read rule for attachments, manifest consistency) —
// see the "do not write a second indexing routine" rule this file is built
// against.
func (w *Watcher) applyOne(relPath string, removed bool) {
	if w.testOnApply != nil {
		w.testOnApply(relPath, removed)
	}

	ctx, cancel := context.WithTimeout(context.Background(), w.opTimeout)
	defer cancel()

	if removed {
		if err := w.ix.RemovePath(ctx, relPath); err != nil {
			w.logger.Warn("knowledge: watcher remove path failed",
				"root", w.ix.Root(), "path", relPath, "error", err)
		}
		return
	}

	if err := w.ix.UpdatePath(ctx, relPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Raced: the backend told us this path changed, but by the time
			// the debounce timer fired and we got here it was gone again
			// (a create immediately followed by a delete, both inside one
			// debounce window's tail). Self-heal by removing it instead of
			// treating this as a failure — the governing principle again:
			// this is exactly the kind of gap the periodic/startup sweep
			// would also close, we are just closing it sooner.
			if rmErr := w.ix.RemovePath(ctx, relPath); rmErr != nil {
				w.logger.Warn("knowledge: watcher self-heal remove after missing update target failed",
					"root", w.ix.Root(), "path", relPath, "error", rmErr)
			}
			return
		}
		w.logger.Warn("knowledge: watcher update path failed",
			"root", w.ix.Root(), "path", relPath, "error", err)
	}
}

// runSweep is design §5's escalation target: ONE incremental SyncWith,
// guaranteed complete, standing in for however many individual events
// triggered it (or were lost before triggering it). It runs on the Watcher's
// own sweepCtx, which Stop cancels, so a sweep in flight when the caller
// shuts down does not keep the process busy walking a large collection after
// the caller has already stopped caring.
func (w *Watcher) runSweep() {
	start := time.Now()
	stats, err := w.ix.SyncWith(w.sweepCtx, SyncOptions{})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		w.logger.Error("knowledge: watcher escalation sweep failed",
			"root", w.ix.Root(), "error", err)
		return
	}
	w.logger.Info("knowledge: watcher escalated to a full sweep",
		"root", w.ix.Root(),
		"scanned", stats.Scanned, "indexed", stats.Indexed, "unchanged", stats.Unchanged,
		"removed", stats.Removed, "duration_ms", time.Since(start).Milliseconds())
}

// joinRelPath builds a collection-relative, slash-separated path the way
// ScanEntry.RelPath is always represented (scan.go), from a platform
// backend's already-relative parent directory and a bare file/dir name. It is
// intentionally NOT filepath.Join: RelPath must be forward-slash on every
// platform (index_manifest and the properties index compare it as an opaque
// string across platforms), so this never routes through an OS-specific
// separator.
func joinRelPath(parentRel, name string) string {
	if parentRel == "" {
		return name
	}
	return parentRel + "/" + name
}

// isWatchSyncArtifact reports whether base names a filesystem artifact the
// sync mechanism itself produces, rather than real vault content — design
// §7's ".icloud placeholders ... skipped by name".
//
// This is deliberately narrow. It does NOT filter an Obsidian "conflicted
// copy" file (e.g. "note (conflicted copy 2026-09-01).md") — that is a real
// markdown file with real content that Scan (scan.go) indexes like any other
// note, and the watcher withholding it would create exactly the kind of
// watcher/sweep inconsistency this design exists to avoid: instant via one
// path, indexed-eventually via the other, for a file that is equally real
// content either way. An iCloud download placeholder is different in kind —
// it is not the note, it is a zero-byte stand-in for a note not yet
// downloaded, named by prefixing "." and appending ".icloud" to the real
// name — so skipping it avoids indexing a phantom path that vanishes the
// moment the real download completes and the placeholder is replaced.
func isWatchSyncArtifact(base string) bool {
	if len(base) < 2 || base[0] != '.' {
		return false
	}
	return strings.HasSuffix(strings.ToLower(base), ".icloud")
}

// reportRunErr delivers a fatal backend error to the Watcher's run loop
// without blocking: runErr is capacity-1 and only ever needs to carry the
// FIRST failure, so a full channel means someone already saw one and this
// send would just be redundant.
func reportRunErr(runErr chan<- error, err error) {
	select {
	case runErr <- err:
	default:
	}
}

// sendEvent delivers one translated filesystem change to the Watcher's run
// loop, or gives up if stop is closed first — used by every platform backend
// so an event never blocks a backend's own read loop past shutdown.
func sendEvent(out chan<- fsEvent, ev fsEvent, stop <-chan struct{}) {
	select {
	case out <- ev:
	case <-stop:
	}
}
