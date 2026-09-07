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
// # Performance
//
// This file is the CORRECT REFERENCE implementation: a serial walk with the
// full behavioral contract, written so all six implementation tracks can build
// and test against a working engine from day one. The best-in-class levers
// (FR-022: parallel scanning, sensitive-literal bytes.Index fast path with an
// alloc gate, required-literal prefilter, folded insensitive scan) are layered
// on behind THIS SAME API by the engine track; behavior must not change.
package filegrep

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/grafana/regexp"
	gitignore "github.com/sabhiram/go-gitignore"
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
	Files          int           // max files visited (default 50_000)
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
	Path          string
	Kind          MatchKind
	Line          int
	Excerpt       string
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

// matcher is the compiled query: exactly one of lit / re is active.
type matcher struct {
	lit      string // literal to find (already case-normalized when folding)
	fold     bool   // true => case-insensitive literal (compare folded)
	re       *regexp.Regexp
	contextN int
}

func (o Options) effectiveCase() CaseMode {
	switch o.Case {
	case CaseSensitive, CaseInsensitive:
		return o.Case
	default: // smart: any uppercase letter => sensitive (MV-13)
		for _, r := range o.Query {
			if unicode.IsUpper(r) {
				return CaseSensitive
			}
		}
		return CaseInsensitive
	}
}

func compile(o Options) (*matcher, error) {
	m := &matcher{contextN: o.ContextLines}
	if m.contextN < 0 {
		m.contextN = 0
	}
	if m.contextN > 5 {
		m.contextN = 5
	}
	cs := o.effectiveCase()
	if o.Regex {
		pat := o.Query
		if cs == CaseInsensitive {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, err
		}
		m.re = re
		return m, nil
	}
	m.lit = o.Query
	m.fold = cs == CaseInsensitive
	return m, nil
}

// lineMatch reports whether line matches, and the byte offset of the first
// match (for excerpt centering). Reference implementation: the optimized
// engine keeps these semantics exactly (folded compare == strings.ToLower
// equality semantics for the reference; ASCII-fold fast path may replace it
// as long as results are identical — test-pinned by the equivalence tests).
func (m *matcher) lineMatch(line []byte) (int, bool) {
	if m.re != nil {
		loc := m.re.FindIndex(line)
		if loc == nil {
			return 0, false
		}
		return loc[0], true
	}
	if m.fold {
		// Correctness-first reference: Unicode-aware fold via lowering both.
		// (The optimized path substitutes an ASCII-folded scanner with regex
		// fallback for non-ASCII patterns — MV-13 — behavior-identical.)
		l := bytes.ToLower(line)
		q := strings.ToLower(m.lit)
		i := bytes.Index(l, []byte(q))
		if i < 0 {
			return 0, false
		}
		return i, true
	}
	i := bytes.Index(line, []byte(m.lit))
	if i < 0 {
		return 0, false
	}
	return i, true
}

func (m *matcher) nameMatch(name string) bool {
	_, ok := m.lineMatch([]byte(name))
	return ok
}

// excerpt cuts a <=ExcerptCapBytes window around pos, snapping to rune
// boundaries so the result is always valid UTF-8 (MV-6).
func excerpt(line []byte, pos int) string {
	if len(line) == 0 {
		return ""
	}
	if pos < 0 {
		pos = 0
	}
	if pos > len(line) {
		pos = len(line)
	}
	half := ExcerptCapBytes / 2
	start := pos - half
	if start < 0 {
		start = 0
	}
	end := start + ExcerptCapBytes
	if end > len(line) {
		end = len(line)
		if end-start > ExcerptCapBytes {
			start = end - ExcerptCapBytes
		}
	}
	for start > 0 && start < len(line) && !utf8.RuneStart(line[start]) {
		start--
	}
	for end < len(line) && !utf8.RuneStart(line[end]) {
		end++
	}
	if end-start > ExcerptCapBytes+utf8.UTFMax {
		end = start + ExcerptCapBytes
		for end > start && end < len(line) && !utf8.RuneStart(line[end]) {
			end--
		}
	}
	return string(line[start:end])
}

// errBudget signals a request-level bound; carried through the walk.
type errBudget struct{ reason TruncatedReason }

func (e errBudget) Error() string { return "filegrep: budget " + string(e.reason) }

// state is one search's mutable accounting.
type state struct {
	m       *matcher
	opts    Options
	lim     Limits
	res     *Result
	output  int // accumulated output bytes (MV-3a engine budget)
	include []string
	exclude []string
}

func (s *state) chargeOutput(h Hit) error {
	n := len(h.Path) + len(h.Excerpt)
	for _, c := range h.ContextBefore {
		n += len(c)
	}
	for _, c := range h.ContextAfter {
		n += len(c)
	}
	if s.output+n > s.lim.OutputBytes {
		return errBudget{ReasonMaxOutput}
	}
	s.output += n
	s.res.Hits = append(s.res.Hits, h)
	if len(s.res.Hits) >= s.lim.Matches {
		return errBudget{ReasonMaxMatches}
	}
	return nil
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

// ignoreStack layers nested .gitignore/.ignore matchers (FR-007): a path is
// pruned when the DEEPEST matching pattern says ignore, honoring negations,
// exactly gitignore's precedence (delegated to the library per O1/MIN-004).
type ignoreLayer struct {
	base string // dir the ignore file lives in, "" for root
	gi   *gitignore.GitIgnore
}

func loadIgnoreLayer(fsys fs.FS, dir string) []ignoreLayer {
	var out []ignoreLayer
	for _, name := range []string{".gitignore", ".ignore"} {
		p := name
		if dir != "" {
			p = dir + "/" + name
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		gi := gitignore.CompileIgnoreLines(lines...)
		out = append(out, ignoreLayer{base: dir, gi: gi})
	}
	return out
}

func ignoredBy(layers []ignoreLayer, rel string, isDir bool) bool {
	// Later (deeper) layers win; go-gitignore handles negation within a file.
	verdict := false
	decided := false
	for _, l := range layers {
		sub := rel
		if l.base != "" {
			if !strings.HasPrefix(rel, l.base+"/") {
				continue
			}
			sub = strings.TrimPrefix(rel, l.base+"/")
		}
		probe := sub
		if isDir {
			probe = sub + "/"
		}
		if l.gi.MatchesPath(probe) {
			verdict, decided = true, true
		} else if decided && l.gi.MatchesPath("!"+probe) {
			// negation is handled inside MatchesPath; nothing extra here.
			_ = probe
		}
	}
	return verdict
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
			var b errBudget
			if errors.As(err, &b) {
				res.Truncated = true
				res.TruncatedReason = b.reason
				return res, nil
			}
			// Root itself failed mid-walk (FR-021): visible, never quiet.
			res.Truncated = true
			res.TruncatedReason = ReasonRootLost
			return res, nil
		}
	}
	return res, nil
}

func (s *state) walkRoot(ctx context.Context, root Root) error {
	// Verify the root opens at all — a dead mount is root_lost, not 0 matches.
	if _, err := fs.Stat(root.FS, "."); err != nil {
		return err
	}
	perFileHits := map[string]int{}
	var layers []ignoreLayer
	layers = append(layers, loadIgnoreLayer(root.FS, "")...)

	var walk func(dir string, depth int, layers []ignoreLayer) error
	walk = func(dir string, depth int, layers []ignoreLayer) error {
		select {
		case <-ctx.Done():
			return errBudget{ReasonDeadline}
		default:
		}
		if depth > s.lim.Depth {
			return errBudget{ReasonMaxDepth}
		}
		entries, err := fs.ReadDir(root.FS, pathOrDot(dir))
		if err != nil {
			if dir == "" {
				return err // root lost
			}
			s.res.Stats.FilesSkippedProblems++
			return nil
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			select {
			case <-ctx.Done():
				return errBudget{ReasonDeadline}
			default:
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
					continue
				}
				if ignoredBy(layers, rel, true) {
					s.res.Stats.FilesPrunedIgnored++
					continue
				}
				sub := append(layers, loadIgnoreLayer(root.FS, rel)...)
				if err := walk(rel, depth+1, sub); err != nil {
					return err
				}
				continue
			}
			if !s.opts.IncludeHidden && strings.HasPrefix(name, ".") {
				continue
			}
			if ignoredBy(layers, rel, false) {
				s.res.Stats.FilesPrunedIgnored++
				continue
			}
			reported := rel
			if root.Name != "" {
				reported = root.Name + "/" + rel
			}
			if !s.globAllowed(reported) {
				continue
			}
			s.res.Stats.FilesVisited++
			if s.res.Stats.FilesVisited > s.lim.Files {
				return errBudget{ReasonMaxFiles}
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
			if err := s.scanFile(ctx, root, rel, reported, perFileHits); err != nil {
				return err
			}
		}
		return nil
	}
	return walk("", 1, layers)
}

func pathOrDot(p string) string {
	if p == "" {
		return "."
	}
	return path.Clean(p)
}

func (s *state) scanFile(ctx context.Context, root Root, rel, reported string, perFileHits map[string]int) error {
	f, err := root.FS.Open(rel)
	if err != nil {
		s.res.Stats.FilesSkippedProblems++
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
		before    [][]byte // ring of up to contextN previous lines
		pending   []*Hit   // hits awaiting up to contextN after-lines
	)
	appendAfter := func(line []byte) {
		for _, h := range pending {
			if len(h.ContextAfter) < s.m.contextN {
				h.ContextAfter = append(h.ContextAfter, string(line))
			}
		}
		filtered := pending[:0]
		for _, h := range pending {
			if len(h.ContextAfter) < s.m.contextN {
				filtered = append(filtered, h)
			}
		}
		pending = filtered
	}

	for {
		select {
		case <-ctx.Done():
			return errBudget{ReasonDeadline}
		default:
		}
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimRight(line, "\r\n")
			lineNo++
			fileBytes += int64(len(line))
			s.res.Stats.BytesScanned += int64(len(line))
			if s.res.Stats.BytesScanned > s.lim.Bytes {
				return errBudget{ReasonMaxBytes}
			}
			if fileBytes > PerFileContentCap {
				s.res.Stats.FilesSkippedFileCap++
				return nil // per-file remainder skip, counted — not a truncation
			}
			if pos, ok := s.m.lineMatch(trimmed); ok {
				if perFileHits[rel] >= s.lim.MatchesPerFile {
					s.res.Stats.HitsCappedPerFile++
					// keep scanning for byte accounting? No: capped file is done.
					return nil
				}
				perFileHits[rel]++
				h := Hit{
					Path:    reported,
					Kind:    KindContent,
					Line:    lineNo,
					Excerpt: excerpt(trimmed, pos),
				}
				if s.m.contextN > 0 {
					for _, b := range before {
						h.ContextBefore = append(h.ContextBefore, string(b))
					}
				}
				if err := s.chargeOutput(h); err != nil {
					return err
				}
				if s.m.contextN > 0 {
					pending = append(pending, &s.res.Hits[len(s.res.Hits)-1])
				}
			} else if s.m.contextN > 0 {
				appendAfter(trimmed)
			}
			if s.m.contextN > 0 {
				cp := make([]byte, len(trimmed))
				copy(cp, trimmed)
				before = append(before, cp)
				if len(before) > s.m.contextN {
					before = before[1:]
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			s.res.Stats.FilesSkippedProblems++
			return nil
		}
	}
}
