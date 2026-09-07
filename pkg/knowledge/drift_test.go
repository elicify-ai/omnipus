// Tests for the drift check and its automatic schedule (ADR-067 unit B4).
//
// Oracles from the specification, never from the implementation:
//
//	FR-038   a drift check that runs with NO agent involved: does the index still
//	         match the disk?
//	FR-038a  it runs automatically — every 6 hours by default, operator-
//	         configurable — plus once on mount; no button anywhere; it reports
//	         ONLY when something is wrong; at most one run per collection in
//	         flight; and it runs IN-PROCESS, because the gateway holds the
//	         exclusive index lock
//	FR-039a  an attachment is never opened, for any reason
//	D14      mtime alone is insufficient to detect an external change — the
//	         content hash is the decision
//
// Spec test 72 states the shape this file must have: "Injected clock; COUNT
// RUNS, not elapsed time. Healthy -> zero notifications; one bad file -> exactly
// one report. Catches downgrading to a single boot run, and reporting on healthy
// runs."
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"
)

// b4Check runs a drift check and fails the test on error.
func b4Check(t *testing.T, ix *Index) DriftReport {
	t.Helper()
	rep, err := CheckDrift(context.Background(), ix)
	if err != nil {
		t.Fatalf("CheckDrift: %v", err)
	}
	return rep
}

// b4Findings renders a report's findings as "kind path" strings, so a failure
// prints something a reader can act on.
func b4Findings(rep DriftReport) []string {
	out := make([]string, 0, len(rep.Findings))
	for _, f := range rep.Findings {
		out = append(out, string(f.Kind)+" "+f.Path)
	}
	return out
}

// ---------------------------------------------------------------------------
// FR-038 — the check itself
// ---------------------------------------------------------------------------

// TestDrift_DetectsWhatChangedOnDiskWithNoAgent is FR-038.
//
// A knowledge base is the operator's own folder. Obsidian writes it, Syncthing
// replicates into it, git checks out branches under it. None of those writers
// tells Omnipus anything, so the only way to know the index is still right is to
// go and look — with no agent, no model and no operator action.
//
// The three mutations are chosen to be the three DIFFERENT ways an index goes
// wrong, and the third is the one a naive check misses: the note is rewritten to
// exactly the same length with its modification time restored. ADR-067 D14
// records why that case is real rather than contrived — "Syncthing preserves
// source mtimes on replication, and several filesystems have 1-second
// granularity" — and it is invisible to any check that trusts size and mtime.
func TestDrift_DetectsWhatChangedOnDiskWithNoAgent(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "notes/a.md", "alpha note about drift")
	b2WriteFile(t, root, "notes/b.md", "bravo note about drift")
	b2WriteFile(t, root, "notes/c.md", "charlie note about drift")
	b2WriteFile(t, root, "img/diagram.png", "\x89PNG binary bytes")

	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	if rep := b4Check(t, ix); !rep.Healthy() {
		t.Fatalf("a freshly indexed collection reported drift: %v", b4Findings(rep))
	}

	// (1) A note deleted behind our back.
	if err := os.Remove(filepath.Join(root, "notes", "a.md")); err != nil {
		t.Fatal(err)
	}
	// (2) A note added behind our back.
	b2WriteFile(t, root, "notes/d.md", "delta note about drift")
	// (3) A note rewritten to the SAME LENGTH with its mtime restored — the case
	// only a content hash can see.
	bPath := filepath.Join(root, "notes", "b.md")
	before, err := os.Stat(bPath)
	if err != nil {
		t.Fatal(err)
	}
	replacement := "BRAVO note about drift" // same byte length, different bytes
	if len(replacement) != int(before.Size()) {
		t.Fatalf("fixture error: replacement is %d bytes, original is %d; the same-size case is what makes this test meaningful",
			len(replacement), before.Size())
	}
	if writeErr := os.WriteFile(bPath, []byte(replacement), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if timeErr := os.Chtimes(bPath, before.ModTime(), before.ModTime()); timeErr != nil {
		t.Fatal(timeErr)
	}
	after, err := os.Stat(bPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("fixture error: size/mtime were not preserved (%d/%v vs %d/%v); "+
			"the hash-only case is not being exercised",
			after.Size(), after.ModTime(), before.Size(), before.ModTime())
	}

	rep := b4Check(t, ix)
	if rep.Healthy() {
		t.Fatal("CheckDrift reported healthy after a file was deleted, one added and one rewritten")
	}
	got := b4Findings(rep)
	sort.Strings(got)
	want := []string{
		"missing_from_disk notes/a.md",
		"not_indexed notes/d.md",
		"stale_content notes/b.md",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %v, want exactly %v", got, want)
	}
	if rep.Root != ix.Root() {
		t.Errorf("report.Root = %q, want %q", rep.Root, ix.Root())
	}
	if rep.Summary() == "" {
		t.Error("Summary() is empty for an unhealthy report; the notification needs something to say")
	}

	// An ordinary reconcile repairs the two mutations it can SEE — and leaves the
	// third. FR-033 lets the default freshness check trust size and modification
	// time, and this rewrite preserved both. That is not a defect in the
	// reconcile; it is the reason FR-038 exists as a separate, hash-based check,
	// and asserting it here stops anyone "fixing" the drift check by simply
	// running a reconcile and declaring victory.
	b2Sync(t, ix)
	rep = b4Check(t, ix)
	if got, want := b4Findings(rep), []string{"stale_content notes/b.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after a stat-based reconcile, findings = %v, want %v — "+
			"a same-size, same-mtime rewrite is invisible to size and mtime", got, want)
	}

	// A DEEP reconcile hashes, so it does repair it, and the check then agrees.
	if _, err := ix.SyncWith(context.Background(), SyncOptions{Deep: true}); err != nil {
		t.Fatal(err)
	}
	if rep := b4Check(t, ix); !rep.Healthy() {
		t.Errorf("drift persisted after a deep reconcile: %v", b4Findings(rep))
	}
}

// TestDrift_IsDeterministic pins the ordering property. Two checks over the same
// state must produce the same report, or a notification can never be compared
// against the previous one and every run looks like new news.
func TestDrift_IsDeterministic(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	for _, n := range []string{"m.md", "b.md", "z.md", "a.md", "k.md"} {
		b2WriteFile(t, root, n, "note "+n)
	}
	ix := b2Open(t, home, root)
	b2Sync(t, ix)
	for _, n := range []string{"m.md", "b.md", "z.md"} {
		if err := os.Remove(filepath.Join(root, n)); err != nil {
			t.Fatal(err)
		}
	}
	first := b4Findings(b4Check(t, ix))
	second := b4Findings(b4Check(t, ix))
	if !reflect.DeepEqual(first, second) {
		t.Errorf("two checks of the same state disagreed:\n first = %v\nsecond = %v", first, second)
	}
	want := []string{"missing_from_disk b.md", "missing_from_disk m.md", "missing_from_disk z.md"}
	if !reflect.DeepEqual(first, want) {
		t.Errorf("findings = %v, want %v in path order", first, want)
	}
}

// TestDrift_NeverOpensAnAttachment is FR-039a applied to this path.
//
// The drift check is exactly where the rule is most tempting to break: the
// obvious way to know whether a file changed is to read it and hash it, and
// doing that to a 4 GB video an operator keeps in their vault is both a
// violation and a performance disaster. An attachment's presence is all that can
// drift, because its name and path are all that were ever indexed.
//
// Both halves are asserted. Counting zero attachment reads alone would pass an
// implementation that skipped attachments entirely; the notes must be read, or
// the check is not doing its job.
func TestDrift_NeverOpensAnAttachment(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "note.md", "an ordinary note")
	b2WriteFile(t, root, "img/diagram-v3.png", "\x89PNG\r\n\x1a\n binary")
	b2WriteFile(t, root, "docs/report.pdf", "%PDF-1.7 binary")

	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	opened, restore := b2CountingOpen(t)
	defer restore()

	if rep := b4Check(t, ix); !rep.Healthy() {
		t.Fatalf("unexpected drift: %v", b4Findings(rep))
	}

	var notesRead int
	for _, p := range *opened {
		switch {
		case strings.HasSuffix(p, "diagram-v3.png"), strings.HasSuffix(p, "report.pdf"):
			t.Errorf("the drift check opened attachment %q; FR-039a allows zero content reads from one", p)
		case strings.HasSuffix(p, "note.md"):
			notesRead++
		}
	}
	if notesRead == 0 {
		t.Error("the drift check read no note either; a check that opens nothing cannot detect a same-size rewrite, " +
			"so \"zero attachment reads\" would be passing for the wrong reason")
	}
}

// TestDrift_DocumentCountMismatchIsFound covers the disagreement that comparing
// the manifest to disk cannot possibly find: the manifest and the INDEX
// disagreeing about the index. A half-committed batch, a crash between the batch
// commit and the manifest write, or a lost segment file all land here — and on
// disk everything looks perfect.
func TestDrift_DocumentCountMismatchIsFound(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "a.md", "alpha")
	b2WriteFile(t, root, "b.md", "bravo")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	if rep := b4Check(t, ix); !rep.Healthy() {
		t.Fatalf("unexpected drift before the mutation: %v", b4Findings(rep))
	}

	// Remove one document from the index only. Disk and manifest are untouched.
	if err := ix.idx.Delete(segmentDocID("a.md", 0)); err != nil {
		t.Fatal(err)
	}

	rep := b4Check(t, ix)
	if rep.Healthy() {
		t.Fatal("CheckDrift reported healthy with a document missing from the index; " +
			"comparing the manifest to disk alone can never see this")
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Kind != DriftDocumentCount {
		t.Fatalf("findings = %v, want exactly one %q finding", b4Findings(rep), DriftDocumentCount)
	}
	if !strings.Contains(rep.Findings[0].Detail, "2") || !strings.Contains(rep.Findings[0].Detail, "1") {
		t.Errorf("detail = %q, want it to state both counts (the manifest's 2 and the index's 1)",
			rep.Findings[0].Detail)
	}
}

// TestDrift_SkippedSymlinkIsReportedButDoesNotMakeTheCollectionUnhealthy is the
// deliberate non-finding, and it protects the property that makes the whole
// schedule work.
//
// The walk skips symbolic links by design (FR-044), so the index correctly
// covers its defined scope and nothing has drifted. Were a skipped symlink a
// finding, a collection containing one would notify the operator every six hours
// forever — and an operator who has learnt to dismiss the six-hourly drift
// notice will dismiss the one that matters too. It is still REPORTED, in the
// report's own Skipped list, because FR-112 forbids omitting it silently.
func TestDrift_SkippedSymlinkIsReportedButDoesNotMakeTheCollectionUnhealthy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}
	home, root, outside := t.TempDir(), t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "note.md", "an ordinary note")
	secret := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secret, []byte("outside the collection"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "link.md")); err != nil {
		t.Fatal(err)
	}

	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	rep := b4Check(t, ix)
	if !rep.Healthy() {
		t.Errorf("a skipped symlink made the collection unhealthy: %v; "+
			"a six-hourly notification about a file we correctly refuse to follow trains the operator to ignore all of them",
			b4Findings(rep))
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0].RelPath != "link.md" {
		t.Errorf("report.Skipped = %+v, want exactly one entry for link.md; "+
			"skipped is not the same as unreported (FR-112)", rep.Skipped)
	}
	if rep.Skipped[0].Reason != ScanProblemSymlink {
		t.Errorf("skipped reason = %q, want %q", rep.Skipped[0].Reason, ScanProblemSymlink)
	}
}

// TestCheckDrift_RunsInProcessAgainstTheOpenIndex is FR-038a's "it MUST run
// in-process", asserted against the reason the requirement exists rather than
// restated as a comment.
//
// scorch keeps its root metadata in a bbolt file under a process-exclusive lock.
// While the gateway holds the index open, a separate `omnipus doctor` process
// could not open it at all — it would wait out its lock timeout and report an
// error about a lock, having learnt nothing about the collection. The pair of
// assertions is what makes this falsifiable: the second open must FAIL while the
// index is held and SUCCEED once it is released, so a failure for any other
// reason (a wrong path, a missing directory) fails the test rather than passing
// it.
func TestCheckDrift_RunsInProcessAgainstTheOpenIndex(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "a.md", "alpha")
	ix, err := OpenIndex(home, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, syncErr := ix.Sync(context.Background()); syncErr != nil {
		t.Fatal(syncErr)
	}

	// The in-process check works on the handle the gateway already holds.
	if rep := b4Check(t, ix); !rep.Healthy() {
		t.Fatalf("unexpected drift: %v", b4Findings(rep))
	}

	shortWait := map[string]any{"bolt_timeout": "250ms"}
	outcome := make(chan error, 1)
	go func() {
		idx, openErr := bleve.OpenUsing(ix.blevePath, shortWait)
		if openErr == nil {
			_ = idx.Close()
		}
		outcome <- openErr
	}()
	select {
	case openErr := <-outcome:
		if openErr == nil {
			_ = ix.Close()
			t.Fatal("a second open of the index succeeded while it was held; " +
				"if that were true out of process too, FR-038a's in-process requirement would have no basis — " +
				"and if it is true only in-process, the exclusion this package's registry depends on is gone")
		}
	case <-time.After(15 * time.Second):
		_ = ix.Close()
		t.Fatal("the second open neither failed nor returned within 15s: the bolt_timeout this package passes " +
			"is not being honoured, so a contended index open would HANG rather than error — a bounded error is " +
			"the stated failure mode for this integration boundary")
	}

	// Positive control: the same open succeeds once the handle is released, so
	// the failure above was the lock and not a broken path.
	if closeErr := ix.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	released, err := bleve.OpenUsing(ix.blevePath, shortWait)
	if err != nil {
		t.Fatalf("opening the index after it was closed failed with %v; "+
			"the earlier failure therefore proves nothing about locking", err)
	}
	_ = released.Close()
}

// ---------------------------------------------------------------------------
// FR-038a — the automatic schedule
// ---------------------------------------------------------------------------

// b4Ticker is an injected clock: FR-038a's schedule is proved by COUNTING RUNS
// against ticks the test delivers, never by sleeping and hoping. A test that
// slept six hours would not run; one that slept and asserted "at least one run"
// would pass with the schedule deleted.
type b4Ticker struct {
	ch       chan time.Time
	interval time.Duration
	stops    atomic.Int64
}

func newB4Ticker() *b4Ticker { return &b4Ticker{ch: make(chan time.Time, 16)} }

func (k *b4Ticker) new(d time.Duration) (<-chan time.Time, func()) {
	k.interval = d
	return k.ch, func() { k.stops.Add(1) }
}

func (k *b4Ticker) tick() { k.ch <- time.Now() }

// b4WaitRun blocks until one run signal arrives, or fails the test.
func b4WaitRun(t *testing.T, runs <-chan error) error {
	t.Helper()
	select {
	case err := <-runs:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for a scheduled drift run")
		return nil
	}
}

// TestHealthCheck_RunsOnScheduleAndOnlyReportsFailures is spec test 72.
//
// Two failures are being caught, and they pull in opposite directions:
//
//   - "Catches downgrading to a single boot run." A check that runs once at
//     startup and never again looks identical to a scheduled one for the first
//     six hours, and is useless afterwards. The oracle is therefore the RUN
//     COUNT against delivered ticks — one on mount plus one per tick — not
//     "a run happened".
//
//   - "Catches reporting on healthy runs." Four healthy notifications a day is
//     how an operator learns to dismiss the fifth. Zero notifications while
//     healthy is the requirement; exactly one when a file goes missing is the
//     proof that silence meant health rather than a dead notifier.
func TestHealthCheck_RunsOnScheduleAndOnlyReportsFailures(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "a.md", "alpha")
	b2WriteFile(t, root, "b.md", "bravo")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	ticker := newB4Ticker()
	runs := make(chan error, 32)
	var mu sync.Mutex
	var notices []DriftReport

	h, err := NewHealthChecker(HealthCheckerOptions{
		Notify: func(rep DriftReport) {
			mu.Lock()
			notices = append(notices, rep)
			mu.Unlock()
		},
		OnRun:     func(_ DriftReport, err error) { runs <- err },
		NewTicker: ticker.new,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Watch(context.Background(), ix); err != nil {
		t.Fatal(err)
	}
	defer h.Stop()

	// FR-038a's "plus once on mount".
	if err := b4WaitRun(t, runs); err != nil {
		t.Fatalf("mount run failed: %v", err)
	}
	if h.Runs() != 1 {
		t.Fatalf("Runs() = %d after mounting, want 1 (the on-mount run)", h.Runs())
	}

	// Three scheduled ticks, three more runs. A single-boot-run implementation
	// stops at 1.
	const ticks = 3
	for i := 0; i < ticks; i++ {
		ticker.tick()
		if err := b4WaitRun(t, runs); err != nil {
			t.Fatalf("scheduled run %d failed: %v", i+1, err)
		}
	}
	if got, want := h.Runs(), 1+ticks; got != want {
		t.Errorf("Runs() = %d after %d ticks, want %d; the check must run on EVERY interval, not once at boot",
			got, ticks, want)
	}

	// Healthy throughout: nothing was reported.
	mu.Lock()
	healthyNotices := len(notices)
	mu.Unlock()
	if healthyNotices != 0 || h.Notified() != 0 {
		t.Errorf("%d notifications from %d healthy runs (Notified() = %d), want 0; "+
			"a healthy run produces no notification at all (FR-038a)", healthyNotices, h.Runs(), h.Notified())
	}

	// Now break it: exactly one bad file.
	if err := os.Remove(filepath.Join(root, "a.md")); err != nil {
		t.Fatal(err)
	}
	ticker.tick()
	if err := b4WaitRun(t, runs); err != nil {
		t.Fatalf("run after the mutation failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(notices) != 1 {
		t.Fatalf("got %d notifications after one file went missing, want exactly 1", len(notices))
	}
	if h.Notified() != 1 {
		t.Errorf("Notified() = %d, want 1", h.Notified())
	}
	rep := notices[0]
	if rep.Healthy() {
		t.Fatal("the reported drift report claims to be healthy")
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Kind != DriftMissingFromDisk || rep.Findings[0].Path != "a.md" {
		t.Errorf("reported findings = %v, want exactly [missing_from_disk a.md]", b4Findings(rep))
	}
	if rep.Root != ix.Root() {
		t.Errorf("reported root = %q, want %q — a notification that does not name the collection is not actionable",
			rep.Root, ix.Root())
	}
}

// TestHealthCheck_AtMostOneRunPerCollectionInFlight is FR-038a's concurrency
// bound, in both the ways it can be broken.
//
//   - Overlapping ticks. If the run were spawned rather than performed inline,
//     a collection whose check outlasts its interval would start a second check
//     of the same files on top of the first, and a slow collection would end up
//     hashing its whole corpus several times over.
//
//   - Overlapping MOUNTS. FR-031 makes one host folder mounted into three
//     workspaces one index with one root. Three Watch calls must be one
//     schedule, not three concurrent checks of the same files.
func TestHealthCheck_AtMostOneRunPerCollectionInFlight(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "a.md", "alpha")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	var inFlight, maxInFlight atomic.Int64
	ticker := newB4Ticker()
	runs := make(chan error, 32)

	h, err := NewHealthChecker(HealthCheckerOptions{
		Notify: func(DriftReport) {},
		OnRun:  func(_ DriftReport, err error) { runs <- err },
		Check: func(ctx context.Context, ix *Index) (DriftReport, error) {
			n := inFlight.Add(1)
			for {
				old := maxInFlight.Load()
				if n <= old || maxInFlight.CompareAndSwap(old, n) {
					break
				}
			}
			// Long enough that a spawned second run would overlap this one.
			time.Sleep(20 * time.Millisecond)
			inFlight.Add(-1)
			return DriftReport{Root: ix.Root()}, nil
		},
		NewTicker: ticker.new,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Three Watch calls for the SAME collection.
	for i := 0; i < 3; i++ {
		if err := h.Watch(context.Background(), ix); err != nil {
			t.Fatalf("Watch %d: %v", i, err)
		}
	}
	if n := len(h.watching); n != 1 {
		t.Fatalf("%d watchers for one collection root, want 1; FR-031 makes several mounts of one folder one collection", n)
	}

	if err := b4WaitRun(t, runs); err != nil {
		t.Fatal(err)
	}
	// Queue several ticks at once, faster than a run completes.
	const ticks = 4
	for i := 0; i < ticks; i++ {
		ticker.tick()
	}
	for i := 0; i < ticks; i++ {
		if err := b4WaitRun(t, runs); err != nil {
			t.Fatal(err)
		}
	}
	h.Stop()

	if got := maxInFlight.Load(); got != 1 {
		t.Errorf("%d checks of one collection ran concurrently, want at most 1 (FR-038a)", got)
	}
	if got, want := h.Runs(), 1+ticks; got != want {
		t.Errorf("Runs() = %d, want %d — three Watch calls for one root must be one schedule, not three", got, want)
	}
	if ticker.stops.Load() != 1 {
		t.Errorf("the ticker was stopped %d times, want 1; a schedule that never stops its ticker leaks it per mount",
			ticker.stops.Load())
	}
}

// TestHealthCheck_IntervalDefaultsToSixHoursAndIsOperatorConfigurable is
// FR-038a's stated cadence. The default is a number in the requirement, so it is
// asserted as one; and "operator-configurable" means a supplied interval must
// actually reach the clock rather than being accepted and ignored.
func TestHealthCheck_IntervalDefaultsToSixHoursAndIsOperatorConfigurable(t *testing.T) {
	if DefaultDriftInterval != 6*time.Hour {
		t.Errorf("DefaultDriftInterval = %v, want 6h (FR-038a)", DefaultDriftInterval)
	}

	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "a.md", "alpha")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	for _, tc := range []struct {
		name string
		set  time.Duration
		want time.Duration
	}{
		{"unset uses the six-hour default", 0, DefaultDriftInterval},
		{"an operator setting is honoured", 90 * time.Minute, 90 * time.Minute},
		{"a nonsensical setting falls back to the default", -1, DefaultDriftInterval},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ticker := newB4Ticker()
			runs := make(chan error, 4)
			h, err := NewHealthChecker(HealthCheckerOptions{
				Interval:  tc.set,
				Notify:    func(DriftReport) {},
				OnRun:     func(_ DriftReport, err error) { runs <- err },
				Check:     func(context.Context, *Index) (DriftReport, error) { return DriftReport{}, nil },
				NewTicker: ticker.new,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := h.Watch(context.Background(), ix); err != nil {
				t.Fatal(err)
			}
			b4WaitRun(t, runs)
			h.Stop()
			if ticker.interval != tc.want {
				t.Errorf("the schedule ticked at %v, want %v", ticker.interval, tc.want)
			}
		})
	}
}

// TestHealthCheck_HasNoManualTrigger is FR-038a's "with no button anywhere",
// asserted where it can be asserted: nothing in this package's scheduler offers
// a run-on-demand entry point for a button to be wired to.
//
// The requirement's reasoning is worth restating, because the button is always
// the thing someone adds back. A health check you have to remember to press
// reports nothing, since the moment you think to press it is the moment you
// already suspect the answer. The on-mount run and the interval are the whole
// trigger surface.
//
// This test fails the day someone adds RunNow, CheckNow or Refresh, which is
// precisely the day the guarantee would otherwise end quietly. (CheckDrift
// itself stays exported — FR-038 requires a check that can run with no agent,
// and `doctor` needs it. The absence of a UI affordance is enforced above this
// package; see the unit's report.)
func TestHealthCheck_HasNoManualTrigger(t *testing.T) {
	allowed := map[string]bool{
		"Watch":    true, // starts the schedule; its first run IS the on-mount run
		"Unwatch":  true,
		"Stop":     true,
		"Runs":     true, // observability
		"Notified": true, // observability
	}
	typ := reflect.TypeOf(&HealthChecker{})
	var got []string
	for i := 0; i < typ.NumMethod(); i++ {
		got = append(got, typ.Method(i).Name)
	}
	sort.Strings(got)
	for _, name := range got {
		if !allowed[name] {
			t.Errorf("HealthChecker has exported method %q, which is not in the allow-list; "+
				"FR-038a forbids a manual trigger, and an exported run-now method is the button", name)
		}
	}
}

// TestHealthCheck_RefusesToBeBuiltWithoutANotifier pins the other half of
// "reports only when something is wrong": it must be able to report at all. A
// checker with a nil notifier would burn a full corpus hash every six hours and
// discard the answer, which looks exactly like a healthy collection.
func TestHealthCheck_RefusesToBeBuiltWithoutANotifier(t *testing.T) {
	if _, err := NewHealthChecker(HealthCheckerOptions{}); err == nil {
		t.Error("NewHealthChecker succeeded with no notifier; the results would go nowhere and look like health")
	} else if err != ErrNoDriftNotifier {
		t.Errorf("err = %v, want ErrNoDriftNotifier", err)
	}
}

// TestHealthCheck_StopsWithItsContext ensures a revoked mount does not leave a
// goroutine hashing the operator's disk every six hours forever.
func TestHealthCheck_StopsWithItsContext(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "a.md", "alpha")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	ticker := newB4Ticker()
	runs := make(chan error, 4)
	h, err := NewHealthChecker(HealthCheckerOptions{
		Notify:    func(DriftReport) {},
		OnRun:     func(_ DriftReport, err error) { runs <- err },
		NewTicker: ticker.new,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := h.Watch(ctx, ix); err != nil {
		t.Fatal(err)
	}
	b4WaitRun(t, runs)

	cancel()
	// Unwatch waits for the goroutine to finish; a leaked one would hang here.
	done := make(chan struct{})
	go func() { h.Unwatch(ix.Root()); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Unwatch did not return; the schedule goroutine outlived its context")
	}
	if n := len(h.watching); n != 0 {
		t.Errorf("%d watchers remain after Unwatch, want 0", n)
	}
	before := h.Runs()
	ticker.tick()
	time.Sleep(50 * time.Millisecond)
	if h.Runs() != before {
		t.Errorf("Runs() went from %d to %d after the schedule was stopped", before, h.Runs())
	}
}

// TestDrift_ReportsAnInterruptedRename is FR-104's "detectable" clause, at the
// surface §12's scenario names: "Given a rename interrupted part-way, When the
// collection IS CHECKED, Then the interruption is reported".
//
// The automatic check is this one. Before this test the check did not look for
// journals at all — grep for "journal" in drift.go returned nothing — so an
// interrupted rename was reported by nothing, anywhere: not the drift check,
// not the gateway's doctor, not a subsequent rename. The recovery machinery was
// complete and unreachable.
func TestDrift_ReportsAnInterruptedRename(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "Old Note.md", "# Old\n")
	b2WriteFile(t, root, "inbound.md", "Refers to [[Old Note]].\n")

	ix := b2Open(t, home, root)
	b2Sync(t, ix)
	if rep := b4Check(t, ix); !rep.Healthy() {
		t.Fatalf("a freshly indexed collection reported drift: %v", b4Findings(rep))
	}

	// Plan and journal a rename, then die before applying a single step —
	// exactly the state FR-104's first clause guarantees is on disk.
	cr, err := NewCollectionRoot(OSLinkFS(), root)
	if err != nil {
		t.Fatal(err)
	}
	store := NewJournalStore(DefaultJournalDir(root))
	r := &Renamer{Root: cr, Store: store}
	plan, err := r.Plan(RenameRequest{From: "Old Note.md", To: "Renamed.md"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(plan.Journal); err != nil {
		t.Fatal(err)
	}

	rep := b4Check(t, ix)
	var pending []DriftFinding
	for _, f := range rep.Findings {
		if f.Kind == DriftPendingRename {
			pending = append(pending, f)
		}
	}
	if len(pending) != 1 {
		t.Fatalf("findings = %v, want exactly one pending_rename — an interrupted rename must be reported when the collection is checked (FR-104)",
			b4Findings(rep))
	}
	if pending[0].Path != "Old Note.md" {
		t.Errorf("pending_rename path = %q, want the note being renamed", pending[0].Path)
	}
	if !strings.Contains(pending[0].Detail, "Renamed.md") {
		t.Errorf("pending_rename detail = %q, want it to name the destination so an operator can act", pending[0].Detail)
	}
	if rep.Healthy() {
		t.Error("a collection with an unfinished rename reported itself healthy")
	}

	// Once the operation completes, the report goes quiet again. Without this
	// the finding could be a permanent false alarm rather than a state report.
	if _, err := r.RecoverPending(); err != nil {
		t.Fatalf("RecoverPending: %v", err)
	}
	after := b4Check(t, ix)
	for _, f := range after.Findings {
		if f.Kind == DriftPendingRename {
			t.Errorf("pending_rename survives a completed recovery: %v", f)
		}
	}
}
