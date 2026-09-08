// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package filegrep is the bounded, index-free file search engine of ADR-081
// (docs/internal/specs/unified-search-and-grep-spec.md, workstream B).
//
// It searches file NAMES/paths always and TEXT-file content under hard bounds,
// across one or more confined roots (a workspace work tree and its mounts),
// and it is the single engine behind both the human Library file search
// (POST /library/{ws}/files/search) and the agent `grep` tool.
//
// # Contract highlights (the spec is the authority)
//
//   - Pure Go, no index, no external binaries (ADR-081 D3/D6).
//   - Confinement is the CALLER's job: every root arrives as an fs.FS that
//     already enforces the boundary (os.Root.FS() in production, fstest/dirFS
//     in tests). This package never touches the host filesystem directly, so
//     it cannot escape what it is handed.
//   - Every bound is explicit and every truncation is honest: Result.Truncated
//   - Result.TruncatedReason name which bound fired (MV-3); the per-file
//     content cap is a counted per-file skip, never a request truncation.
//   - A lost walk root is ReasonRootLost — never a quiet empty result (FR-021).
//   - Binary files (NUL in the first 8 KiB) are name-matchable, never
//     content-scanned (FR-005).
//   - Case modes: smart (default; any uppercase in the pattern => sensitive),
//     sensitive, insensitive — applied to names AND content (MV-13).
//   - .gitignore/.ignore pruning applies to BOTH name and content search,
//     nested files and negations honored, git-ness irrelevant; `.git/`,
//     `.library/` and `.omnipus-vault/` are ALWAYS pruned regardless of
//     IncludeHidden, which governs user dotfiles only (FR-007, R2-MIN-006).
//   - Excerpts are <= ExcerptCapBytes and always valid UTF-8 (MV-6).
//   - A hit is ONE matching line (first match position); a name match is one
//     hit with KindName (MV-14).
//
// # Performance (FR-022)
//
// This is the BEST-IN-CLASS engine layered behind the reference API's exact
// contract (see filegrep_test.go for the equivalence tests pinning it):
//
//   - Sensitive-literal fast path: bytes.Index only, the regex engine is
//     structurally unreachable (matcher.lineMatch's plain-literal branch),
//     zero allocations (matcher.go).
//   - Insensitive literals: an ASCII-case-folded scanner (indexFoldASCII,
//     zero allocation) for ASCII patterns; regex ("(?i)") fallback only when
//     the pattern contains a non-ASCII byte (matcher.go).
//   - Required-literal prefilter: a literal proven mandatory by the parsed
//     regex AST (prefilter.go) is bytes.Contains-checked before a line ever
//     reaches RE2.
//   - Parallel scanning: the directory walk stays single-goroutine and fully
//     deterministic (name matching, gitignore/hidden/glob filtering, and
//     every structural bound — max files, max depth — are unchanged from the
//     reference), and feeds a bounded worker pool that scans file CONTENT
//     concurrently. Every hit is collected under one mutex and the final
//     Result.Hits is sorted path-lexicographic-then-line before return (spec
//     A3), so the result set is deterministic regardless of scheduling.
//     Budget counters (BytesScanned, output bytes, match counts) are
//     mutex-protected so they are exact — never torn — under concurrency;
//     which exact hit trips a global budget can vary run-to-run when several
//     files are being scanned at once (an inherent, documented property of
//     concurrent budget enforcement), but every stat and every reason
//     reported is truthful.
package filegrep

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
)

// CaseMode selects how letter case is treated for BOTH name and content
// matching (spec MV-13, R2-MAJ-006).
type CaseMode string

const (
	CaseSmart       CaseMode = "smart"
	CaseSensitive   CaseMode = "sensitive"
	CaseInsensitive CaseMode = "insensitive"
)

// TruncatedReason is the machine-readable request-level truncation cause
// (MV-3). Exactly the wire enum of FileSearchResponse.truncated_reason.
type TruncatedReason string

const (
	ReasonMaxFiles   TruncatedReason = "max_files"
	ReasonMaxBytes   TruncatedReason = "max_bytes"
	ReasonMaxMatches TruncatedReason = "max_matches"
	ReasonMaxDepth   TruncatedReason = "max_depth"
	ReasonDeadline   TruncatedReason = "deadline"
	ReasonMaxOutput  TruncatedReason = "max_output"
	ReasonRootLost   TruncatedReason = "root_lost"
)

// MatchKind distinguishes a name/path hit from a content-line hit.
type MatchKind string

const (
	KindName    MatchKind = "name"
	KindContent MatchKind = "content"
)

// Limits are the request-level bounds (MV-3). The zero value of any field
// means "use the default"; values above the defaults are clamped DOWN by
// Normalize (the caller discloses the clamp via the limits_applied echo).
type Limits struct {
	Files          int           // max files+directories visited combined (default 50_000; review finding F3)
	Bytes          int64         // max content bytes scanned (default 256 MiB)
	Matches        int           // max total hits (default 1_000)
	MatchesPerFile int           // max hits contributed by one file (default 50)
	Depth          int           // max directory depth (default 32)
	Deadline       time.Duration // wall-clock budget (default 10s)
	OutputBytes    int           // accumulated output budget (default 1 MiB)
}

// Defaults per MV-3. Exported so the handler and the tool share one source.
const (
	DefaultMaxFiles             = 50_000
	DefaultMaxBytes       int64 = 256 << 20
	DefaultMaxMatches           = 1_000
	DefaultMatchesPerFile       = 50
	DefaultMaxDepth             = 32
	DefaultDeadline             = 10 * time.Second
	DefaultOutputBytes          = 1 << 20
	// PerFileContentCap is the per-file scan cap (4 MiB). Exceeding it is a
	// per-file remainder skip counted in Stats, NOT a request truncation.
	PerFileContentCap int64 = 4 << 20
	// ExcerptCapBytes bounds one excerpt window (MV-6).
	ExcerptCapBytes = 512
	// binarySniffBytes is how much of a file decides binary-vs-text (NUL rule).
	binarySniffBytes = 8 << 10
)

// Normalize fills defaults and clamps over-asks down. It reports the effective
// limits — the caller's `limits_applied` echo.
func (l Limits) Normalize() Limits {
	def := Limits{
		Files: DefaultMaxFiles, Bytes: DefaultMaxBytes, Matches: DefaultMaxMatches,
		MatchesPerFile: DefaultMatchesPerFile, Depth: DefaultMaxDepth,
		Deadline: DefaultDeadline, OutputBytes: DefaultOutputBytes,
	}
	clampInt := func(v, d int) int {
		if v <= 0 || v > d {
			return d
		}
		return v
	}
	out := Limits{
		Files:          clampInt(l.Files, def.Files),
		Matches:        clampInt(l.Matches, def.Matches),
		MatchesPerFile: clampInt(l.MatchesPerFile, def.MatchesPerFile),
		Depth:          clampInt(l.Depth, def.Depth),
		OutputBytes:    clampInt(l.OutputBytes, def.OutputBytes),
	}
	if l.Bytes <= 0 || l.Bytes > def.Bytes {
		out.Bytes = def.Bytes
	} else {
		out.Bytes = l.Bytes
	}
	if l.Deadline <= 0 || l.Deadline > def.Deadline {
		out.Deadline = def.Deadline
	} else {
		out.Deadline = l.Deadline
	}
	return out
}

// Root is one confined tree to search. Name prefixes every reported path
// ("" for the workspace work tree; the mount name for a mount), joined with
// "/" — matching how the Library addresses entries.
type Root struct {
	Name string
	FS   fs.FS
	// ScopePrefix and AncestorIgnore let a caller that narrowed FS to a
	// subdirectory of a larger tree (os.Root.OpenRoot, fs.Sub) preserve the
	// .gitignore/.ignore layers that live ABOVE the narrowed root (F8):
	// this package never reads outside the fs.FS it's handed, so those
	// ancestor layers are otherwise invisible to it. ScopePrefix is FS's
	// own path relative to the true, unscoped root ("src" after
	// fs.Sub(wfs, "src")); AncestorIgnore is that root's ignore file
	// content for every directory strictly above ScopePrefix, built by
	// LoadAncestorIgnore against the UNNARROWED fs.FS before narrowing.
	// Both zero-valued (the default) means FS already IS the true root.
	ScopePrefix    string
	AncestorIgnore []AncestorIgnoreLayer
}

// Options is one search request.
type Options struct {
	Query         string
	Regex         bool
	Case          CaseMode
	IncludeHidden bool
	IncludeGlobs  []string
	ExcludeGlobs  []string
	ContextLines  int // 0..5
	Limits        Limits
}

// Hit is one result row (MV-14: one matching line = one hit; a name match is
// one hit with KindName and Line 0).
type Hit struct {
	Path    string
	Kind    MatchKind
	Line    int
	Excerpt string
	// IsDir is true when this hit is a directory rather than a file (review
	// finding F1). It is only ever true on a KindName hit — directories are
	// never content-scanned, so a KindContent hit's IsDir is always false.
	// Wire: FileSearchHit.is_dir (optional; omitted/false for a file hit).
	IsDir         bool
	ContextBefore []string
	ContextAfter  []string
}

// Stats is the walk accounting (observable, never silent).
type Stats struct {
	FilesVisited         int
	BytesScanned         int64
	FilesSkippedProblems int
	FilesPrunedIgnored   int
	FilesSkippedFileCap  int
	HitsCappedPerFile    int
	// DirsVisited is the directory-entry counterpart of FilesVisited (review
	// finding F3): directories the walk reached, name-checked, and glob-
	// filtered. Kept as its own counter rather than folded into
	// FilesVisited — a "files searched" figure that silently included
	// directories would be its own kind of misleading — but the two share
	// one budget: see the Limits.Files enforcement in walkDir.
	DirsVisited int
	// FilesFilteredGlob counts a file OR directory the walk reached but
	// rejected via include_globs/exclude_globs (review finding F4).
	// Deliberately distinct from FilesPrunedIgnored: an include/exclude
	// glob is a request-scoped filter the caller asked for, not a
	// repository-level ignore rule (.gitignore/.ignore/always-pruned/
	// hidden) — conflating the two would hide which one actually explains
	// a given search's shape.
	FilesFilteredGlob int
}

// Result is one search answer.
type Result struct {
	Hits            []Hit
	Truncated       bool
	TruncatedReason TruncatedReason
	LimitsApplied   Limits
	Stats           Stats
}

// alwaysPruned are directory basenames never entered regardless of
// IncludeHidden (R2-MIN-006): Omnipus internals and git object noise.
var alwaysPruned = map[string]struct{}{
	".git": {}, ".library": {}, ".omnipus-vault": {},
}

// excerpt cuts a <=ExcerptCapBytes window around pos, snapping to rune
// boundaries so the result is always valid UTF-8 (MV-6).
func excerpt(line []byte, pos int) string {
	return excerptCapped(line, pos, ExcerptCapBytes)
}

// excerptCapped is excerpt's shared implementation, parameterized on the cap
// so capContextLine's truncation-marker path (review finding F5) can reserve
// room for the marker without duplicating the rune-boundary/UTF-8-validity
// logic. excerpt(line, pos) is exactly excerptCapped(line, pos,
// ExcerptCapBytes) — behavior-identical to the pre-F5 excerpt().
func excerptCapped(line []byte, pos, capBytes int) string {
	if len(line) == 0 {
		return ""
	}
	if pos < 0 {
		pos = 0
	}
	if pos > len(line) {
		pos = len(line)
	}
	half := capBytes / 2
	start := pos - half
	if start < 0 {
		start = 0
	}
	end := start + capBytes
	if end > len(line) {
		end = len(line)
		if end-start > capBytes {
			start = end - capBytes
		}
	}
	// Snapping INWARD (trimming a partial leading/trailing rune) rather than
	// outward can only shrink [start, end), so the pre-snap size — already
	// <=capBytes above — bounds the result unconditionally; no separate
	// reclamp is needed.
	for start < end && !utf8.RuneStart(line[start]) {
		start++
	}
	for end > start && end < len(line) && !utf8.RuneStart(line[end]) {
		end--
	}
	result := line[start:end]
	if !utf8.Valid(result) {
		// Landing both edges on a lead byte (utf8.RuneStart) guarantees the
		// window doesn't SPLIT a rune, but not that every byte inside it
		// forms a COMPLETE valid sequence — content that isn't valid UTF-8
		// to begin with (non-UTF-8 text that has no NUL byte in its first
		// 8 KiB, so it passes the binary sniff) can still produce an
		// invalid window. MV-6 requires excerpts to be valid UTF-8
		// unconditionally, so drop whatever invalid bytes remain rather
		// than ever return one that isn't (dropping only shrinks the
		// result, so it never grows past the cap either).
		return strings.ToValidUTF8(string(result), "")
	}
	return string(result)
}

// contextTruncationMarker is appended to a ContextBefore/ContextAfter line
// whose tail was actually cut by the ExcerptCapBytes bound (review finding
// F5), so a reader — human or agent — never mistakes a truncated line for a
// short one that simply ends there. An in-string marker rather than a wire
// flag: context_before/context_after are plain string arrays
// (FileSearchHit.yaml), and every existing consumer (SPA row rendering, the
// grep tool's text rendering) already treats each entry as an opaque line —
// a marker embedded in the string reaches all of them for free, with no
// contract or consumer change, versus restructuring a string[] into an
// object[] wire type for one edge case. Unlike excerpt(), which deliberately
// windows AROUND the match (a reader already expects a fragment there), a
// context line carries no match position and would otherwise lose its tail
// with no trace at all.
const contextTruncationMarker = "…"

// capContextLine bounds a ContextBefore/ContextAfter line to <=ExcerptCapBytes
// the same UTF-8-safe way excerpt() bounds a match window (MV-6) — a context
// line carries no match position of its own, so it is capped from its start.
// A line whose tail is actually cut (len(line) > ExcerptCapBytes, so
// excerptCapped MUST drop bytes) gets contextTruncationMarker appended,
// itself still inside the ExcerptCapBytes bound — the untruncated fast path
// is untouched (no marker, no extra allocation beyond excerpt's own).
func capContextLine(line []byte) string {
	if len(line) <= ExcerptCapBytes {
		return excerpt(line, 0)
	}
	return excerptCapped(line, 0, ExcerptCapBytes-len(contextTruncationMarker)) + contextTruncationMarker
}

// budgetError signals a request-level bound; carried through the walk.
type budgetError struct{ reason TruncatedReason }

func (e budgetError) Error() string { return "filegrep: budget " + string(e.reason) }

// errStopped is an internal sentinel: it means "some other goroutine already
// recorded the real stop reason" (see walkAbort). It never reaches a caller
// of Search — walkRoot always substitutes the real reason before returning.
var errStopped = errors.New("filegrep: walk stopped")

// walkAbort is the first-error-wins handoff between the (single-goroutine)
// walker and the content-scan worker pool: whichever goroutine discovers a
// budget breach first records it and cancels the shared scan context so
// every other goroutine stops promptly.
type walkAbort struct {
	mu  sync.Mutex
	err error
}

func (a *walkAbort) report(err error, cancel context.CancelFunc) {
	a.mu.Lock()
	if a.err == nil {
		a.err = err
	}
	a.mu.Unlock()
	cancel()
}

func (a *walkAbort) get() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

// state is one search's mutable accounting. Fields touched only by the
// single walker goroutine (FilesVisited, FilesPrunedIgnored, and the
// directory-walk control flow) need no synchronization; fields reachable
// from the content-scan worker pool (res.Hits, output, and the Stats fields
// scanFile updates) are guarded by mu so concurrent scanning never tears an
// update.
type state struct {
	m       *matcher
	opts    Options
	lim     Limits
	res     *Result
	mu      sync.Mutex
	output  int // accumulated output bytes (MV-3a engine budget)
	include []string
	exclude []string
}

// chargeOutput enforces the accumulated output-byte budget and the total
// match cap (MV-3/MV-3a). Safe for concurrent callers (the walker for name
// hits, worker-pool goroutines for content hits).
func (s *state) chargeOutput(h Hit) error {
	n := len(h.Path) + len(h.Excerpt)
	for _, c := range h.ContextBefore {
		n += len(c)
	}
	for _, c := range h.ContextAfter {
		n += len(c)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Both caps are checked against the hit being admitted NOW, before it is
	// appended: truncation is what happened to a REJECTED hit, never a
	// property of the last one that fit (a request with exactly Matches
	// hits and nothing more is not truncated).
	if len(s.res.Hits) >= s.lim.Matches {
		return budgetError{ReasonMaxMatches}
	}
	if s.output+n > s.lim.OutputBytes {
		return budgetError{ReasonMaxOutput}
	}
	s.output += n
	s.res.Hits = append(s.res.Hits, h)
	return nil
}

// addBytesScanned adds n to Stats.BytesScanned under the shared lock and
// reports whether the running total is now over the byte budget.
func (s *state) addBytesScanned(n int64) (over bool) {
	s.mu.Lock()
	s.res.Stats.BytesScanned += n
	over = s.res.Stats.BytesScanned > s.lim.Bytes
	s.mu.Unlock()
	return over
}

func (s *state) countSkippedProblem() {
	s.mu.Lock()
	s.res.Stats.FilesSkippedProblems++
	s.mu.Unlock()
}

func (s *state) countFileCapSkip() {
	s.mu.Lock()
	s.res.Stats.FilesSkippedFileCap++
	s.mu.Unlock()
}

func (s *state) countHitsCappedPerFile() {
	s.mu.Lock()
	s.res.Stats.HitsCappedPerFile++
	s.mu.Unlock()
}

// checkStop reports whether the walk should stop now. ctx is the original
// request context (its own deadline is the only source of a genuine
// "deadline" reason); scanCtx additionally trips when a worker in the
// content-scan pool has already recorded a different budget breach — in
// that case checkStop returns errStopped so the caller defers to
// walkAbort.get() for the real reason, rather than misreporting it as a
// deadline.
func (s *state) checkStop(ctx, scanCtx context.Context) error {
	select {
	case <-scanCtx.Done():
		if ctx.Err() != nil {
			return budgetError{ReasonDeadline}
		}
		return errStopped
	default:
		return nil
	}
}

func (s *state) globAllowed(rel string) bool {
	for _, g := range s.exclude {
		if ok, _ := doublestar.Match(g, rel); ok {
			return false
		}
	}
	if len(s.include) == 0 {
		return true
	}
	for _, g := range s.include {
		if ok, _ := doublestar.Match(g, rel); ok {
			return true
		}
	}
	return false
}

// workerCount bounds the content-scan pool size: enough to overlap I/O and
// RE2 work across files, never so many that a small search pays needless
// goroutine/scheduling overhead.
func workerCount() int {
	n := runtime.GOMAXPROCS(0)
	if n < 1 {
		n = 1
	}
	if n > 8 {
		n = 8
	}
	return n
}

// Search runs one bounded search over the given roots, in order. It never
// returns a Go error for per-file problems (those are counted); the error
// return is reserved for a bad pattern (regex compile) — every other outcome,
// including every budget stop and a lost root, is expressed in Result.
func Search(ctx context.Context, roots []Root, opts Options) (Result, error) {
	lim := opts.Limits.Normalize()
	res := Result{Hits: []Hit{}, LimitsApplied: lim}
	m, err := compile(opts)
	if err != nil {
		return res, err
	}
	ctx, cancel := context.WithTimeout(ctx, lim.Deadline)
	defer cancel()

	s := &state{m: m, opts: opts, lim: lim, res: &res,
		include: opts.IncludeGlobs, exclude: opts.ExcludeGlobs}

	for _, root := range roots {
		if err := s.walkRoot(ctx, root); err != nil {
			var b budgetError
			if errors.As(err, &b) {
				res.Truncated = true
				res.TruncatedReason = b.reason
				sortHits(res.Hits)
				return res, nil
			}
			// Root itself failed mid-walk (FR-021): visible, never quiet.
			res.Truncated = true
			res.TruncatedReason = ReasonRootLost
			sortHits(res.Hits)
			return res, nil
		}
	}
	sortHits(res.Hits)
	return res, nil
}

// sortHits enforces the deterministic result-set ordering the parallel
// scanner needs (spec A3: files kind => path-lexicographic, then line —
// name hits carry Line 0 so they naturally sort before any content hit on
// the same path). The serial reference produced this order for free by
// walking directories alphabetically and scanning one file at a time; the
// parallel engine restores it explicitly since worker completion order is
// not otherwise deterministic.
func sortHits(hits []Hit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		return hits[i].Line < hits[j].Line
	})
}

// scanJob is one file handed from the (serial) walker to the content-scan
// worker pool.
type scanJob struct {
	root     Root
	rel      string
	reported string
}

func (s *state) walkRoot(ctx context.Context, root Root) error {
	// Verify the root opens at all — a dead mount is root_lost, not 0 matches.
	if _, err := fs.Stat(root.FS, "."); err != nil {
		return err
	}

	scanCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan scanJob)
	abort := &walkAbort{}
	var wg sync.WaitGroup
	n := workerCount()
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			for job := range jobs {
				if err := s.scanFile(ctx, scanCtx, job); err != nil {
					abort.report(err, cancel)
				}
			}
		}()
	}

	var ancestorLayers []ignoreLayer
	for _, a := range root.AncestorIgnore {
		ancestorLayers = append(ancestorLayers, newIgnoreLayer(a.Dir, a.Lines))
	}
	ancestor := ancestorContext{prefix: root.ScopePrefix, layers: ancestorLayers}

	layers := loadIgnoreLayer(root.FS, "")
	walkErr := s.walkDir(ctx, scanCtx, root, "", 1, layers, ancestor, jobs)
	walkerFoundGenuineStop := walkErr != nil && !errors.Is(walkErr, errStopped)

	// The walker finishing its traversal is the NORMAL, successful case —
	// every eligible file has been handed to a worker, and those workers
	// must be allowed to keep scanning their already-dispatched job to its
	// own natural completion (EOF, its own budget check, …). Only cancel
	// scanCtx here when the walker itself found a genuine stop reason
	// (a budget breach or root loss); a worker that independently finds one
	// already calls cancel itself via abort.report. Cancelling unconditionally
	// the moment the walk finishes would spuriously cut off in-flight scans
	// that hadn't done anything wrong yet.
	if walkerFoundGenuineStop {
		cancel()
	}
	close(jobs)
	wg.Wait()

	if walkerFoundGenuineStop {
		return walkErr
	}
	if workerErr := abort.get(); workerErr != nil {
		return workerErr
	}
	return nil
}

// ancestorContext is the F8 caller-supplied ignore layers from strictly
// above a scoped Root.FS — constant for the whole walk of one root, so it's
// computed once in walkRoot and threaded unchanged through every recursive
// walkDir call rather than recompiled per directory.
type ancestorContext struct {
	prefix string // Root.ScopePrefix; "" when Root.FS is already the true root
	layers []ignoreLayer
}

// seed resolves the ancestor layers' own verdict for rel (a path relative
// to the scoped Root.FS), to be used as ignoredByFrom's starting point
// before the walk root's own layers get their turn — see ignoredByFrom.
func (a ancestorContext) seed(rel string, isDir bool) bool {
	if len(a.layers) == 0 {
		return false
	}
	trueRel := rel
	if a.prefix != "" {
		trueRel = a.prefix + "/" + rel
	}
	return ignoredByFrom(false, a.layers, trueRel, isDir)
}

// walkDir is the single-goroutine directory walker: it enumerates entries in
// the same alphabetical, depth-first order as the reference, applies every
// structural rule (always-pruned, hidden, gitignore, glob, FilesVisited /
// MaxFiles / MaxDepth), performs NAME matching inline, and hands each
// eligible regular file to the content-scan worker pool via jobs.
func (s *state) walkDir(ctx, scanCtx context.Context, root Root, dir string, depth int, layers []ignoreLayer, ancestor ancestorContext, jobs chan<- scanJob) error {
	if err := s.checkStop(ctx, scanCtx); err != nil {
		return err
	}
	if depth > s.lim.Depth {
		return budgetError{ReasonMaxDepth}
	}
	entries, err := fs.ReadDir(root.FS, pathOrDot(dir))
	if err != nil {
		if dir == "" {
			return err // the walk root's own listing is gone: root_lost
		}
		// FR-021: distinguish an isolated per-directory hiccup from the
		// whole mount having died mid-walk. If the ROOT itself no longer
		// opens either, promote to root_lost instead of silently piling up
		// per-item skips while the mount is actually gone; if the root is
		// still healthy, this is exactly the reference's isolated skip.
		if _, rootErr := fs.Stat(root.FS, "."); rootErr != nil {
			return rootErr
		}
		s.countSkippedProblem()
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if err := s.checkStop(ctx, scanCtx); err != nil {
			return err
		}
		name := e.Name()
		rel := name
		if dir != "" {
			rel = dir + "/" + name
		}
		if e.IsDir() {
			if _, bad := alwaysPruned[name]; bad {
				s.res.Stats.FilesPrunedIgnored++
				continue
			}
			if !s.opts.IncludeHidden && strings.HasPrefix(name, ".") {
				s.res.Stats.FilesPrunedIgnored++
				continue
			}
			if ignoredByFrom(ancestor.seed(rel, true), layers, rel, true) {
				s.res.Stats.FilesPrunedIgnored++
				continue
			}
			reported := rel
			if root.Name != "" {
				reported = root.Name + "/" + rel
			}
			// Review finding F2: a directory's own hit is glob-filtered
			// exactly like a file's — FileSearchRequest.yaml describes
			// include_globs/exclude_globs in terms of "paths" ("only
			// matching paths are considered" / "removed from
			// consideration"), never singling out files, so a directory
			// path is filtered the same way. This gates the HIT only, never
			// TRAVERSAL: pruning the subtree on an include_globs miss here
			// would be an outright bug, not a stricter reading of the
			// contract — an include glob like "**/*.md" legitimately
			// matches a descendant file (docs/report.md) whose parent
			// directory name ("docs") does not itself match "**/*.md";
			// pruning at the directory would silently lose that file's hit
			// too. Subtree pruning already has a purpose-built, directory-
			// aware mechanism — the .gitignore/.ignore layers just above
			// (ignoredByFrom) — and this per-path filter does not take on a
			// second, conflicting role.
			if s.globAllowed(reported) {
				// Review finding F3: a directory the walk reaches counts
				// toward the SAME Files budget files do (checked against
				// the COMBINED FilesVisited+DirsVisited total, not either
				// counter alone — checking them separately would let a
				// tree split across both categories evade the budget
				// entirely, e.g. 999 files + 999 dirs, neither counter
				// over a limit of 1000, yet 1998 entries actually
				// visited), closing the gap where an unbounded-directory
				// tree had no budget protection at all. DirsVisited keeps
				// its own tally (rather than folding into FilesVisited) so
				// a truncation's accounting stays honest either way: a
				// directory-heavy search that hits ReasonMaxMatches no
				// longer reports "0 files searched" while DirsVisited
				// silently explains where the work actually went.
				s.res.Stats.DirsVisited++
				if s.res.Stats.FilesVisited+s.res.Stats.DirsVisited > s.lim.Files {
					return budgetError{ReasonMaxFiles}
				}
				// NAME match on the directory itself (one hit, KindName —
				// F7), IsDir true so the wire can tell it apart from a file
				// hit (review finding F1 — FileSearchHit.is_dir).
				if s.m.nameMatch(name) {
					if err := s.chargeOutput(Hit{Path: reported, Kind: KindName, IsDir: true}); err != nil {
						return err
					}
				}
			} else {
				// Review finding F4: a glob rejection is a walk skip like
				// any other and must be counted, not silently dropped.
				// Distinct from FilesPrunedIgnored — see Stats.
				// FilesFilteredGlob's doc comment for why the two reasons
				// are not conflated.
				s.res.Stats.FilesFilteredGlob++
			}
			sub := append(layers, loadIgnoreLayer(root.FS, rel)...)
			if err := s.walkDir(ctx, scanCtx, root, rel, depth+1, sub, ancestor, jobs); err != nil {
				return err
			}
			continue
		}
		if !s.opts.IncludeHidden && strings.HasPrefix(name, ".") {
			s.res.Stats.FilesPrunedIgnored++
			continue
		}
		if ignoredByFrom(ancestor.seed(rel, false), layers, rel, false) {
			s.res.Stats.FilesPrunedIgnored++
			continue
		}
		reported := rel
		if root.Name != "" {
			reported = root.Name + "/" + rel
		}
		if !s.globAllowed(reported) {
			// Review finding F4: previously an uncounted, silent skip —
			// every other rejection branch in this walk (always-pruned,
			// hidden, .gitignore/.ignore, per-file byte cap, per-file
			// match cap) increments a Stats counter; this one alone left
			// the response's own numbers unable to account for the tree.
			s.res.Stats.FilesFilteredGlob++
			continue
		}
		s.res.Stats.FilesVisited++
		// Review finding F3: checked against the combined files+directories
		// total — see the directory branch's own comment above for why a
		// per-counter check alone isn't enough.
		if s.res.Stats.FilesVisited+s.res.Stats.DirsVisited > s.lim.Files {
			return budgetError{ReasonMaxFiles}
		}
		// NAME match (one hit, KindName — MV-14).
		if s.m.nameMatch(name) {
			if err := s.chargeOutput(Hit{Path: reported, Kind: KindName}); err != nil {
				return err
			}
		}
		// CONTENT scan (regular files only; symlinks are entries the
		// confined FS refuses to traverse — their name may match above).
		if !e.Type().IsRegular() {
			continue
		}
		job := scanJob{root: root, rel: rel, reported: reported}
		select {
		case jobs <- job:
		case <-scanCtx.Done():
			if ctx.Err() != nil {
				return budgetError{ReasonDeadline}
			}
			return errStopped
		}
	}
	return nil
}

func pathOrDot(p string) string {
	if p == "" {
		return "."
	}
	return path.Clean(p)
}

// scanFile scans one file's content on behalf of the worker pool. It
// buffers this file's own hits locally (fileHits) — including resolving
// ContextBefore/ContextAfter, which never cross a file boundary — and only
// hands them to the shared Result via chargeOutput once they're final,
// keeping every cross-goroutine interaction through the single mutex-guarded
// path.
func (s *state) scanFile(ctx, scanCtx context.Context, job scanJob) error {
	f, err := job.root.FS.Open(job.rel)
	if err != nil {
		s.countSkippedProblem()
		return nil
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 64<<10)
	head, _ := br.Peek(binarySniffBytes)
	if bytes.IndexByte(head, 0) >= 0 {
		return nil // binary: name-matchable only (FR-005)
	}

	var (
		fileBytes int64
		lineNo    int
		before    []string // ring of up to contextN previous lines, each pre-capped
		fileHits  []Hit
		pending   []int // indices into fileHits awaiting up to contextN after-lines
		perFile   int   // this file's own match count (MatchesPerFile cap)
	)

	flush := func() error {
		for _, h := range fileHits {
			if err := s.chargeOutput(h); err != nil {
				return err
			}
		}
		return nil
	}

	// appendAfter records line as the next ContextAfter entry for every hit
	// still awaiting one — including when line is itself a match, since a
	// matching line is still a line "following" an earlier match in file
	// order (MV-14/contract: context_after lists file-order lines, matching
	// or not).
	appendAfter := func(line []byte) {
		if len(pending) == 0 {
			return
		}
		capped := capContextLine(line)
		keep := pending[:0]
		for _, idx := range pending {
			fileHits[idx].ContextAfter = append(fileHits[idx].ContextAfter, capped)
			if len(fileHits[idx].ContextAfter) < s.m.contextN {
				keep = append(keep, idx)
			}
		}
		pending = keep
	}

	for {
		if err := s.checkStop(ctx, scanCtx); err != nil {
			if ferr := flush(); ferr != nil {
				return ferr
			}
			return err
		}
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimRight(line, "\r\n")
			lineNo++
			fileBytes += int64(len(line))
			if s.addBytesScanned(int64(len(line))) {
				if ferr := flush(); ferr != nil {
					return ferr
				}
				return budgetError{ReasonMaxBytes}
			}
			if fileBytes > PerFileContentCap {
				s.countFileCapSkip()
				return flush() // per-file remainder skip, counted — not a truncation
			}
			if s.m.contextN > 0 {
				appendAfter(trimmed)
			}
			if pos, ok := s.m.lineMatch(trimmed); ok {
				if perFile >= s.lim.MatchesPerFile {
					s.countHitsCappedPerFile()
					// keep scanning for byte accounting? No: capped file is
					// done, matching the reference's per-file cap behavior.
					return flush()
				}
				perFile++
				h := Hit{
					Path:    job.reported,
					Kind:    KindContent,
					Line:    lineNo,
					Excerpt: excerpt(trimmed, pos),
				}
				if s.m.contextN > 0 {
					h.ContextBefore = append(h.ContextBefore, before...)
				}
				fileHits = append(fileHits, h)
				if s.m.contextN > 0 {
					pending = append(pending, len(fileHits)-1)
				}
			}
			if s.m.contextN > 0 {
				before = append(before, capContextLine(trimmed))
				if len(before) > s.m.contextN {
					before = before[1:]
				}
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return flush()
			}
			s.countSkippedProblem()
			return flush()
		}
	}
}
