// Omnipus — knowledge base drift check and its automatic schedule
// (ADR-067 stage 2, unit B4; FR-038, FR-038a).
//
// THE QUESTION THIS FILE ANSWERS: does the index still match what is on disk?
//
// It has to be answerable with NO AGENT INVOLVED (FR-038). A knowledge base is
// the operator's own folder — Obsidian writes it, Syncthing replicates into it,
// git checks branches out under it, an editor saves into it. None of those
// writers tells Omnipus anything. So an index that was correct an hour ago can
// be quietly wrong now, and "quietly" is the whole problem: a search over a
// stale index answers confidently and looks exactly like a search over a fresh
// one. The drift check is the only thing standing between that and the operator.
//
// FOUR PROPERTIES OF THE SCHEDULE (FR-038a), each one load-bearing:
//
//   - IT RUNS BY ITSELF. Every DefaultDriftInterval — six hours — plus once when
//     a collection is mounted. Operator-configurable.
//
//   - THERE IS NO BUTTON. Anywhere. A health check you have to remember to press
//     is a health check that reports nothing, because the moment you suspect
//     something is wrong is the moment you would press it, and by then you
//     already know. HealthChecker deliberately exposes no method that triggers a
//     run on demand, and a test asserts the exported method set to keep it that
//     way.
//
//   - IT REPORTS ONLY WHEN SOMETHING IS WRONG. A healthy run produces no
//     notification at all — not a "all good" toast, not a log line the operator
//     is meant to read. Four healthy notifications a day is how an operator
//     learns to ignore the fifth, which is the one that mattered.
//
//   - IT RUNS IN-PROCESS. This is not a preference. The gateway holds the
//     scorch index open, and scorch keeps its root metadata in a bbolt file
//     under a PROCESS-EXCLUSIVE lock. A command-line `omnipus doctor` in a second
//     process could not open the index at all — it would block on that lock
//     until its timeout and then report an error about the lock rather than
//     anything about the collection. So CheckDrift takes an already-open *Index
//     and never opens one of its own.
//
// WHAT COUNTS AS DRIFT, AND WHAT DELIBERATELY DOES NOT. A symbolic link inside
// the collection is skipped by the walk BY DESIGN (FR-044) — the index correctly
// covers its defined scope, so a skipped symlink is reported in the report's
// Skipped list and does not make the collection unhealthy. Were it a finding,
// every collection containing one symlink would notify the operator every six
// hours forever, and the fourth property above would be dead within a week.
//
// An attachment is never opened here, for any reason (FR-039a). Only its
// presence and its path matter, because only its name and path were ever
// indexed.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultDriftInterval is FR-038a's stated default: the drift check runs every
// six hours. Operators may configure it; nobody may switch it off by never
// pressing anything, because there is nothing to press.
const DefaultDriftInterval = 6 * time.Hour

// DriftKind is the closed set of ways an index can stop matching its disk.
type DriftKind string

const (
	// DriftNotIndexed — the file is on disk and the index has no record of it.
	// A search cannot find it; the operator has no way to know that.
	DriftNotIndexed DriftKind = "not_indexed"
	// DriftMissingFromDisk — the index has a record of a file that is gone. A
	// search returns a path that cannot be opened.
	DriftMissingFromDisk DriftKind = "missing_from_disk"
	// DriftStaleContent — the file is indexed, but its bytes have changed since.
	// Detected by CONTENT HASH, never by modification time alone: ADR-067 D14
	// records that Syncthing preserves source mtimes on replication and that
	// several filesystems have one-second granularity, so a sub-second external
	// write is invisible to mtime.
	DriftStaleContent DriftKind = "stale_content"
	// DriftDocumentCount — the index holds a different number of documents than
	// the manifest says it should. The manifest and the index disagree about the
	// index itself, which no amount of comparing the manifest to disk would find.
	DriftDocumentCount DriftKind = "document_count"
	// DriftManifestUnusable — the freshness manifest is missing, corrupt, or
	// recorded against a different collection. Everything else this check says
	// is then unreliable, so it is stated first rather than inferred from the
	// pile of not_indexed findings it produces.
	DriftManifestUnusable DriftKind = "manifest_unusable"
	// DriftUnreadable — a file the walk could not read. It should be in the
	// index and cannot be, and FR-112 forbids omitting it silently.
	DriftUnreadable DriftKind = "unreadable"
	// DriftPendingRename — a rename was journalled and never confirmed
	// complete. FR-104's second clause makes an interrupted rewrite
	// "detectable and completable", and §12's scenario places the detection
	// at "when the collection is checked" — which is this check, the only
	// automatic one there is. Without it the journal-writing half is built,
	// the recovery half is built and tested, and nothing ever looks: a
	// process killed mid-rename leaves the note moved, some inbound links
	// pointing at a name that no longer exists, and a record on disk that no
	// code path reads.
	//
	// It is a finding rather than an entry in Skipped because it is not
	// "by design": the collection genuinely may be half-rewritten, and the
	// remedy — RecoverPending — is an action someone has to take.
	DriftPendingRename DriftKind = "pending_rename"
)

// DriftFinding is one way in which the index and the disk disagree.
type DriftFinding struct {
	// Kind is the machine-readable cause.
	Kind DriftKind `json:"kind"`
	// Path is the collection-relative path concerned; empty for a finding about
	// the collection as a whole.
	Path string `json:"path,omitempty"`
	// Detail carries the specifics — an error string, or the two numbers that
	// disagreed.
	Detail string `json:"detail,omitempty"`
}

// DriftReport is the result of one check.
type DriftReport struct {
	// Root is the collection root's resolved real path.
	Root string `json:"root"`
	// CheckedAt is when the check ran.
	CheckedAt time.Time `json:"checked_at"`
	// Duration is how long it took.
	Duration time.Duration `json:"duration"`
	// FilesOnDisk is how many files the walk found.
	FilesOnDisk int `json:"files_on_disk"`
	// FilesIndexed is how many files the manifest claims are indexed.
	FilesIndexed int `json:"files_indexed"`
	// Findings is every disagreement, in a deterministic order (kind, then
	// path), so two runs over the same state produce the same report and a
	// notification can be compared against the previous one.
	Findings []DriftFinding `json:"findings,omitempty"`
	// Skipped is what the walk refused to follow by design — symbolic links
	// (FR-044). Informational: it does NOT make the collection unhealthy.
	Skipped []ScanProblem `json:"skipped,omitempty"`
}

// Healthy reports whether the index matches the disk.
func (r DriftReport) Healthy() bool { return len(r.Findings) == 0 }

// Summary is a one-line description for a notification, counting findings by
// kind. Empty for a healthy report, because a healthy report is never shown.
func (r DriftReport) Summary() string {
	if r.Healthy() {
		return ""
	}
	counts := map[DriftKind]int{}
	order := make([]DriftKind, 0, len(r.Findings))
	for _, f := range r.Findings {
		if _, seen := counts[f.Kind]; !seen {
			order = append(order, f.Kind)
		}
		counts[f.Kind]++
	}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	return fmt.Sprintf("knowledge base %s has drifted from its index: %s",
		r.Root, strings.Join(parts, ", "))
}

// CheckDrift compares an open index against the collection on disk (FR-038).
//
// It runs with no agent, calls no language model, and MUTATES NOTHING — neither
// the collection nor the index. It takes an already-open *Index because the
// process holding that handle holds scorch's exclusive bbolt lock, so a check in
// any other process could not open the index to begin with (FR-038a).
//
// Notes are compared by size and then by CONTENT HASH. Attachments are compared
// by presence only: FR-039a forbids opening one for any reason, and a hash is a
// reason.
func CheckDrift(ctx context.Context, ix *Index) (DriftReport, error) {
	if ix == nil {
		return DriftReport{}, ErrNoIndex
	}

	started := time.Now()
	report := DriftReport{Root: ix.root, CheckedAt: started}

	ix.mu.Lock()
	defer ix.mu.Unlock()

	scan, err := Scan(ix.root)
	if err != nil {
		return report, err
	}
	report.FilesOnDisk = len(scan.Entries)

	var findings []DriftFinding

	for _, p := range scan.Problems {
		switch p.Reason {
		case ScanProblemSymlink:
			// By design, not drift. See the file header.
			report.Skipped = append(report.Skipped, p)
		default:
			findings = append(findings, DriftFinding{
				Kind: DriftUnreadable, Path: p.RelPath, Detail: p.Detail,
			})
		}
	}

	// Pending renames are asked about FIRST among the disk-state questions,
	// because they explain the findings that follow: a half-applied rename
	// produces stale_content on every note it did not reach.
	findings = append(findings, pendingRenameFindings(ix.root)...)

	manifest, loadErr := LoadManifest(ix.manifestPath, ix.root)
	if loadErr != nil {
		findings = append(findings, DriftFinding{
			Kind: DriftManifestUnusable, Detail: loadErr.Error(),
		})
	}
	report.FilesIndexed = manifest.Len()

	seen := make(map[string]struct{}, len(scan.Entries))
	for _, entry := range scan.Entries {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return report, ctxErr
		}
		seen[entry.RelPath] = struct{}{}

		rec, ok := manifest.Get(entry.RelPath)
		if !ok {
			findings = append(findings, DriftFinding{
				Kind: DriftNotIndexed, Path: entry.RelPath,
				Detail: "on disk, absent from the index",
			})
			continue
		}
		if entry.Kind != ScanKindNote {
			// An attachment is indexed by name and path only, so only its
			// presence can drift. Never opened (FR-039a).
			continue
		}
		if rec.Size != entry.Size {
			findings = append(findings, DriftFinding{
				Kind: DriftStaleContent, Path: entry.RelPath,
				Detail: fmt.Sprintf("indexed at %d bytes, now %d", rec.Size, entry.Size),
			})
			continue
		}
		sum, hashErr := ix.hashFile(entry.RelPath)
		if hashErr != nil {
			findings = append(findings, DriftFinding{
				Kind: DriftUnreadable, Path: entry.RelPath, Detail: hashErr.Error(),
			})
			continue
		}
		if sum != rec.Hash {
			// Same size, same (possibly preserved) mtime, different bytes. This
			// is the case mtime alone cannot see.
			findings = append(findings, DriftFinding{
				Kind: DriftStaleContent, Path: entry.RelPath,
				Detail: "content hash differs from the indexed copy",
			})
		}
	}

	expectedDocs := 0
	for relPath, rec := range manifest.Entries {
		expectedDocs += rec.Segments
		if _, ok := seen[relPath]; ok {
			continue
		}
		findings = append(findings, DriftFinding{
			Kind: DriftMissingFromDisk, Path: relPath,
			Detail: "in the index, absent from disk",
		})
	}

	// The manifest and the index can disagree about the index itself — a
	// half-committed batch, a removed segment file, a crash between the batch
	// commit and the manifest write. Comparing the manifest to disk would never
	// notice, because both of those are on the correct side of that comparison.
	docs, docErr := ix.DocCount()
	if docErr != nil {
		findings = append(findings, DriftFinding{
			Kind: DriftDocumentCount, Detail: docErr.Error(),
		})
	} else if uint64(expectedDocs) != docs {
		findings = append(findings, DriftFinding{
			Kind: DriftDocumentCount,
			Detail: fmt.Sprintf("the manifest accounts for %d index documents, the index holds %d",
				expectedDocs, docs),
		})
	}

	sortDriftFindings(findings)
	report.Findings = findings
	report.Duration = time.Since(started)
	return report, nil
}

// pendingRenameFindings reports every journalled rename that was never
// confirmed complete (FR-104).
//
// A journal that will not parse is reported too, and louder: it names an
// operation whose planned rewrites can no longer be read, which is strictly
// worse than one that can.
func pendingRenameFindings(collectionRoot string) []DriftFinding {
	store := NewJournalStore(DefaultJournalDir(collectionRoot))
	journals, err := store.List()
	var out []DriftFinding
	if err != nil {
		out = append(out, DriftFinding{
			Kind:   DriftPendingRename,
			Detail: "a rename journal could not be read: " + err.Error(),
		})
	}
	for _, j := range journals {
		out = append(out, DriftFinding{
			Kind: DriftPendingRename,
			Path: j.From,
			Detail: fmt.Sprintf(
				"a rename of %q to %q (journal %s) was started and never confirmed complete; %d planned link rewrite(s) may be unapplied",
				j.From, j.To, j.ID, len(j.Steps)),
		})
	}
	return out
}

// sortDriftFindings puts findings in a deterministic order so the same state
// always produces the same report (US-11's determinism, applied to the check
// that reports on it).
func sortDriftFindings(f []DriftFinding) {
	sort.SliceStable(f, func(i, j int) bool {
		if f[i].Kind != f[j].Kind {
			return f[i].Kind < f[j].Kind
		}
		if f[i].Path != f[j].Path {
			return f[i].Path < f[j].Path
		}
		return f[i].Detail < f[j].Detail
	})
}

// DriftNotifier receives a report for an UNHEALTHY collection. It is never
// called for a healthy one (FR-038a).
type DriftNotifier func(DriftReport)

// HealthCheckerOptions configures the automatic schedule.
type HealthCheckerOptions struct {
	// Interval between runs. Zero means DefaultDriftInterval (six hours).
	// This is FR-038a's "operator-configurable": the gateway passes the
	// operator's setting through here.
	Interval time.Duration
	// Notify is called for every UNHEALTHY report and never for a healthy one.
	// Required — a checker with nowhere to report is a checker that does
	// nothing, expensively.
	Notify DriftNotifier
	// OnRun, if set, is called after EVERY run, healthy or not, with whatever
	// the run produced. It is observability — metrics, a debug log — and is
	// explicitly NOT the notification channel: wiring an operator-visible
	// message here would reinstate the healthy-run noise FR-038a forbids.
	OnRun func(DriftReport, error)
	// OnError, if set, is called when a run fails outright.
	OnError func(root string, err error)
	// Check overrides the drift check itself. Test seam; defaults to CheckDrift.
	Check func(context.Context, *Index) (DriftReport, error)
	// NewTicker overrides the clock. It returns a tick channel and a stop
	// function. Test seam: FR-038a's schedule is asserted by COUNTING RUNS
	// against injected ticks, never by sleeping and hoping.
	NewTicker func(time.Duration) (<-chan time.Time, func())
}

// ErrNoDriftNotifier is returned by NewHealthChecker when Notify is nil.
var ErrNoDriftNotifier = errors.New("knowledge: a health checker requires a notifier (FR-038a)")

// HealthChecker runs the drift check automatically for every watched collection.
//
// There is no exported method here that runs a check on demand, and that is a
// requirement rather than an oversight (FR-038a: "with no button anywhere").
// Watch's own first run is the "once on mount" the requirement asks for; every
// later run comes from the clock.
type HealthChecker struct {
	interval  time.Duration
	notify    DriftNotifier
	onRun     func(DriftReport, error)
	onError   func(string, error)
	check     func(context.Context, *Index) (DriftReport, error)
	newTicker func(time.Duration) (<-chan time.Time, func())

	mu       sync.Mutex
	watching map[string]*driftWatcher

	runs     atomic.Int64
	notified atomic.Int64
}

type driftWatcher struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// NewHealthChecker builds the scheduler.
func NewHealthChecker(opts HealthCheckerOptions) (*HealthChecker, error) {
	if opts.Notify == nil {
		return nil, ErrNoDriftNotifier
	}
	h := &HealthChecker{
		interval:  opts.Interval,
		notify:    opts.Notify,
		onRun:     opts.OnRun,
		onError:   opts.OnError,
		check:     opts.Check,
		newTicker: opts.NewTicker,
		watching:  make(map[string]*driftWatcher),
	}
	if h.interval <= 0 {
		h.interval = DefaultDriftInterval
	}
	if h.check == nil {
		h.check = CheckDrift
	}
	if h.newTicker == nil {
		h.newTicker = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		}
	}
	return h, nil
}

// Watch starts the automatic schedule for one collection: one run immediately —
// FR-038a's "once on mount" — and one every Interval thereafter, until ctx is
// cancelled or Stop is called.
//
// Watching a collection that is already watched is a no-op. That is what makes
// "at most one run per collection in flight" hold across MOUNTS as well as
// across ticks: FR-031 means one host folder mounted into three workspaces is
// one *Index and one root, and three Watch calls must not become three
// concurrent checks of the same files.
func (h *HealthChecker) Watch(ctx context.Context, ix *Index) error {
	if ix == nil {
		return ErrNoIndex
	}
	root := ix.Root()

	h.mu.Lock()
	if _, already := h.watching[root]; already {
		h.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	w := &driftWatcher{cancel: cancel, done: make(chan struct{})}
	h.watching[root] = w
	h.mu.Unlock()

	go h.loop(runCtx, ix, w)
	return nil
}

// Unwatch stops the schedule for one collection and waits for any run in flight
// to finish.
func (h *HealthChecker) Unwatch(root string) {
	h.mu.Lock()
	w, ok := h.watching[root]
	delete(h.watching, root)
	h.mu.Unlock()
	if !ok {
		return
	}
	w.cancel()
	<-w.done
}

// Stop stops every schedule and waits for runs in flight.
func (h *HealthChecker) Stop() {
	h.mu.Lock()
	roots := make([]string, 0, len(h.watching))
	for root := range h.watching {
		roots = append(roots, root)
	}
	h.mu.Unlock()
	for _, root := range roots {
		h.Unwatch(root)
	}
}

// Runs is how many checks have actually executed. Observability, and the oracle
// spec test 72 uses: FR-038a's schedule is proved by COUNTING RUNS against
// injected ticks, which is what catches an implementation quietly downgraded to
// a single run at boot.
func (h *HealthChecker) Runs() int { return int(h.runs.Load()) }

// Notified is how many unhealthy reports were sent. It must stay at zero for a
// healthy collection no matter how many times the check runs.
func (h *HealthChecker) Notified() int { return int(h.notified.Load()) }

// loop is the whole schedule: one run on mount, then one per tick.
//
// It is a SINGLE goroutine per collection, and the run is performed inline
// rather than spawned. That is what makes FR-038a's "at most one run per
// collection in flight" a structural property rather than a promise: there is no
// path by which a second check of the same collection can start while the first
// is running. Watch's one-watcher-per-root guard is the other half — FR-031
// means one host folder mounted into three workspaces is one root, and three
// mounts must not become three concurrent checks of the same files.
//
// A real time.Ticker drops ticks when the receiver is busy (its channel holds
// one), so a check that outlasts its interval falls behind rather than
// accumulating a backlog it can never work off.
func (h *HealthChecker) loop(ctx context.Context, ix *Index, w *driftWatcher) {
	defer close(w.done)

	tick, stopTicker := h.newTicker(h.interval)
	defer stopTicker()

	h.runOnce(ctx, ix)

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-tick:
			if !ok {
				return
			}
			h.runOnce(ctx, ix)
		}
	}
}

// runOnce performs one check and reports it ONLY if something is wrong.
func (h *HealthChecker) runOnce(ctx context.Context, ix *Index) {
	if ctx.Err() != nil {
		return
	}
	report, err := h.check(ctx, ix)
	h.runs.Add(1)

	if h.onRun != nil {
		h.onRun(report, err)
	}
	if err != nil {
		if h.onError != nil {
			h.onError(ix.Root(), err)
		}
		return
	}
	if report.Healthy() {
		// FR-038a: a healthy run produces NO notification. Not a quiet one.
		return
	}
	h.notified.Add(1)
	h.notify(report)
}
