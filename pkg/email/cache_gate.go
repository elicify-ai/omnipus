package email

// W2 product-owned staging-exclusion evaluation (spec §3.8 rules E-1/E-3;
// founder ruling Q-A 2026-10-02; landing-order register row 18's unified
// condition).
//
// The product guarantees, by its own means and on every install, that its
// Mail cache directory is never captured by data-directory version-control
// staging. This file implements the check side of check-and-refuse: an
// in-process evaluation of the data directory's ACTUAL version-control state
// with git check-ignore-EQUIVALENT semantics. The product never shells out
// to git on this security-critical path (Hard Constraint #2) and never
// writes into, repairs or "establishes" the operator's ignore state — where
// exclusion cannot be proven, the caller refuses and the mailbox runs
// live-only with the visible cache_unavailable notice.
//
// Semantics implemented (check-ignore equivalence for the constructs a data
// directory can contain):
//   - repository discovery (including worktree/submodule ".git" FILE pointers);
//   - no repository ⇒ the staging half holds trivially (nothing to capture);
//   - tracked-state precedence (E-3): a path already in the index is NOT
//     excluded even where a deny rule matches — ignore rules do not untrack
//     (the index is parsed in-process, versions 2/3; anything newer fails
//     closed);
//   - the ".gitignore" stack from the evaluated directory up to the repo
//     root, with git's precedence (deeper file overrides shallower; within a
//     file the last matching rule decides);
//   - "$GIT_DIR/info/exclude" at its git precedence position (below any
//     .gitignore level);
//   - negation ("!") rules, directory-only patterns (trailing "/"), anchored
//     (containing "/") vs basename patterns, and the "*", "?", "[...]" and
//     "**" wildcards with git's ancestor stickiness (a file below an
//     excluded directory is ignored; a negation cannot re-include below an
//     excluded ancestor);
//   - unparseable or uninterpretable rule state fails CLOSED: the evaluation
//     reports "not provable", the write refuses, the mailbox stays live-only
//     (§3.8 divergence rule — an unparseable ignore state never widens the
//     gate).
//
// Deliberate divergence, fail-closed and spec-anchored: the operator's
// GLOBAL git layers (core.excludesFile, the XDG/global ignore files) are NOT
// consulted. §3.8 E-1 requires the product to evaluate "the data directory's
// actual version-control ignore state" and forbids trusting "an external
// file's contents or an operator's setup"; a global rule can only make git
// itself ignore the path, so skipping it can never allow a write git would
// have prevented — it can only refuse where the machine's safety lives
// solely in operator setup, which is exactly the honest cache_unavailable
// state the spec prescribes.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cacheDirName is the cache directory under the resolved data root (§3.8).
const cacheDirName = "mail-cache"

// ErrCachePathTracked marks the E-3 refusal flavour: the cache path is
// already tracked by version control. The caller distinguishes it because a
// tracked file is ALREADY captured — a refused write must leave tracked
// state exactly as found (no stale-target removal), while the other refusal
// flavours mean an existing ciphertext sits unexcluded and must go.
var ErrCachePathTracked = errors.New("a mail cache path is already tracked by version control")

// ensureStagingExclusion returns nil only where the cache directory is
// provably excluded from data-directory version-control staging. The cache
// write path calls this BEFORE any filesystem effect, so a refusal leaves
// nothing behind (not even the directory). The data root is resolved once
// and used consistently: git evaluates RESOLVED paths, so the repository
// walk and the cache-directory comparison must both run on the same
// resolved shape (a symlinked data-root segment otherwise compares a
// resolved repo root against a logical cache path and misreads an enclosing
// repository as "outside").
func ensureStagingExclusion(dataRoot string) error {
	resolved, rerr := filepath.EvalSymlinks(dataRoot)
	if rerr != nil {
		return fmt.Errorf("staging exclusion not provable: %w", rerr)
	}
	repo, err := findGitRepo(resolved)
	if err != nil {
		return fmt.Errorf("staging exclusion not provable: %w", err)
	}
	if repo == nil {
		// No version-control staging in the data directory: nothing to be
		// captured by; the staging half holds trivially (§3.8 E-1).
		return nil
	}
	cacheDir := filepath.Join(resolved, cacheDirName)
	rel, err := filepath.Rel(repo.root, cacheDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("staging exclusion not provable: the cache directory is outside its repository root")
	}
	// Git's index and ignore patterns always use "/" separators; normalize the
	// repo-relative path once so both checks below are platform-stable.
	rel = filepath.ToSlash(rel)
	// E-3 first: a tracked cache path is not excluded, whatever the rules say.
	tracked, err := repo.tracksUnder(rel)
	if err != nil {
		return fmt.Errorf("staging exclusion not provable: %w", err)
	}
	if tracked {
		return fmt.Errorf("%w — ignore rules do not untrack tracked files (E-3)", ErrCachePathTracked)
	}
	ignored, err := repo.pathIgnored(rel)
	if err != nil {
		return fmt.Errorf("staging exclusion not provable: %w", err)
	}
	if !ignored {
		return errors.New("the mail cache directory is not excluded by the data directory's version-control ignore state")
	}
	return nil
}

// gitRepo is a discovered repository: the working-tree root and its resolved
// git directory.
type gitRepo struct {
	root   string
	gitDir string
}

// findGitRepo walks up from start looking for a ".git" directory or a ".git"
// file (worktree/submodule pointer, "gitdir: <path>"). nil with no error
// means no repository encloses start. The walk runs over the RESOLVED path:
// git itself works on resolved paths, and a symlinked data-root segment
// whose target sits inside a repository must find that repository — per-
// component stats over the logical path saw only logical ancestors and
// missed an enclosing repo behind a symlink (the F11 finding). A resolution
// failure is "not provable": the gate refuses rather than guess.
// findGitRepo walks up from start looking for a ".git" directory or a ".git"
// file (worktree/submodule pointer, "gitdir: <path>"). nil with no error
// means no repository encloses start. The walk runs over the RESOLVED path:
// git itself works on resolved paths, and a symlinked data-root segment
// whose target sits inside a repository must find that repository — per-
// component stats over the logical path saw only logical ancestors and
// missed an enclosing repo behind a symlink (the F11 finding). A resolution
// failure is "not provable": the gate refuses rather than guess.
func findGitRepo(start string) (*gitRepo, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return nil, err
	}
	resolved, rerr := filepath.EvalSymlinks(dir)
	if rerr != nil {
		return nil, rerr
	}
	dir = resolved
	for {
		candidate := filepath.Join(dir, ".git")
		info, statErr := os.Stat(candidate)
		if statErr == nil {
			if info.IsDir() {
				return &gitRepo{root: dir, gitDir: candidate}, nil
			}
			raw, readErr := os.ReadFile(candidate)
			if readErr != nil {
				return nil, readErr
			}
			text := strings.TrimSpace(string(raw))
			target, ok := strings.CutPrefix(text, "gitdir:")
			if !ok {
				return nil, fmt.Errorf("uninterpretable .git pointer in %s", dir)
			}
			target = strings.TrimSpace(target)
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			return &gitRepo{root: dir, gitDir: target}, nil
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return nil, statErr
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}
		dir = parent
	}
}

// tracksUnder reports whether the repository's index tracks the cache
// directory itself or anything below it — the E-3 pre-tracked state. Index
// versions other than 2/3 fail closed.
func (r *gitRepo) tracksUnder(relDir string) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(r.gitDir, "index"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(raw) < 12 || string(raw[:4]) != "DIRC" {
		return false, errors.New("uninterpretable git index (bad magic)")
	}
	version := int(beUint32(raw[4:8]))
	if version < 2 || version > 3 {
		return false, fmt.Errorf("uninterpretable git index version %d", version)
	}
	entries := int(beUint32(raw[8:12]))
	pos := 12
	prefix := relDir + "/"
	for i := 0; i < entries; i++ {
		fixed := 62
		if pos+fixed > len(raw) {
			return false, errors.New("uninterpretable git index (truncated entry)")
		}
		flags := beUint16(raw[pos+60 : pos+62])
		if version == 3 && flags&0x4000 != 0 {
			fixed += 2 // v3 extended flags word precedes the path
		}
		if pos+fixed > len(raw) {
			return false, errors.New("uninterpretable git index (truncated extended entry)")
		}
		nameLen := int(flags & 0xfff)
		nameStart := pos + fixed
		var name string
		if nameLen < 0xfff {
			if nameStart+nameLen > len(raw) {
				return false, errors.New("uninterpretable git index (path overruns the entry)")
			}
			name = string(raw[nameStart : nameStart+nameLen])
		} else {
			end := bytes.IndexByte(raw[nameStart:], 0)
			if end < 0 {
				return false, errors.New("uninterpretable git index (unterminated path)")
			}
			name = string(raw[nameStart : nameStart+end])
			nameLen = end
		}
		if name == relDir || strings.HasPrefix(name, prefix) {
			return true, nil
		}
		entrySize := fixed + nameLen
		if pad := entrySize % 8; pad == 0 {
			entrySize += 8 // git pads to an 8-byte multiple with ≥1 NUL
		} else {
			entrySize += 8 - pad
		}
		pos += entrySize
	}
	return false, nil
}

// pathIgnored evaluates the repo's ignore state for relDir (a directory
// path relative to the repo root) with check-ignore-equivalent semantics.
func (r *gitRepo) pathIgnored(relDir string) (bool, error) {
	// Rule sources in git's precedence order, highest first: the .gitignore
	// stack from the evaluated directory's PARENT up to the root (deeper
	// overrides shallower), then $GIT_DIR/info/exclude. Missing sources
	// contribute no rules; a PRESENT source that cannot be parsed fails
	// closed.
	segs := strings.Split(relDir, string(filepath.Separator))
	var sources []ruleSource
	// i counts segments of relDir BELOW the repo root: i == len(segs)-2 is
	// the evaluated directory's parent; i == -1 is the repo root's own
	// .gitignore. Git decides a directory's ignore state from its parent
	// chain only — a .gitignore INSIDE the evaluated directory never decides
	// the directory itself (check-ignore reads it for paths below, never for
	// the directory), so the directory's own file is not on the stack:
	// consulting it let a planted bare line ("mail-cache" inside
	// mail-cache/.gitignore) manufacture the exclusion git would never
	// apply, while git staged the ciphertext on the next add (the F10
	// finding).
	for i := len(segs) - 2; i >= -1; i-- {
		dirElems := append([]string{r.root}, segs[:i+1]...)
		rules, err := parseIgnoreFile(filepath.Join(append(dirElems, ".gitignore")...))
		if err != nil {
			return false, err
		}
		if len(rules) > 0 {
			sources = append(sources, ruleSource{depth: i + 1, rules: rules})
		}
	}
	infoRules, err := parseIgnoreFile(filepath.Join(r.gitDir, "info", "exclude"))
	if err != nil {
		return false, err
	}
	if len(infoRules) > 0 {
		sources = append(sources, ruleSource{depth: 0, rules: infoRules})
	}

	// Ancestor walk, shallowest first: the first source with a matching rule
	// decides (git's cross-level precedence); a deny at any ancestor sticks —
	// nothing below an excluded directory can be re-included; a negation
	// re-includes that prefix and the walk continues below it.
	for i := 1; i <= len(segs); i++ {
		prefix := strings.Join(segs[:i], "/")
		isDir := true // every prefix is a directory, including relDir itself
		for _, src := range sources {
			rule := lastMatchingRule(src.rules, prefix, src.depth, isDir)
			if rule == nil {
				continue
			}
			if rule.negated {
				break // re-included at this level; deeper prefixes decide fresh
			}
			return true, nil
		}
	}
	return false, nil
}

// ruleSource is one ignore file's rules, anchored depth segments below the
// repo root (0 for $GIT_DIR/info/exclude, whose relative patterns git
// resolves from the repository root).
type ruleSource struct {
	depth int
	rules []ignoreRule
}

// ignoreRule is one parsed ignore-file line.
type ignoreRule struct {
	negated  bool
	dirOnly  bool
	anchored bool
	segments []string
}

// parseIgnoreFile reads one ignore file. A missing file yields no rules; a
// present but unreadable/uninterpretable file is an error (fail closed).
func parseIgnoreFile(path string) ([]ignoreRule, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rules []ignoreRule
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSuffix(line, "\r")
		line = strings.TrimRight(line, " \t") // git strips unescaped trailing whitespace
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rule, ok := parseIgnoreLine(line)
		if !ok {
			return nil, fmt.Errorf("uninterpretable ignore rule in %s", path)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// parseIgnoreLine parses one rule line. ok=false marks a construct this
// evaluator cannot interpret with check-ignore-equivalent semantics (the
// caller fails closed).
func parseIgnoreLine(line string) (ignoreRule, bool) {
	var rule ignoreRule
	if strings.HasPrefix(line, `\!`) || strings.HasPrefix(line, `\#`) {
		// Escaped literal '!' / '#' — a name pattern, never a negation/comment.
		rule.segments = []string{unescapeGlob(line)}
		return rule, true
	}
	if strings.HasPrefix(line, "!") {
		rule.negated = true
		line = line[1:]
		if line == "" {
			return rule, false
		}
	}
	if strings.HasSuffix(line, "/") {
		rule.dirOnly = true
		line = strings.TrimSuffix(line, "/")
		if line == "" {
			return rule, false
		}
	}
	rule.anchored = strings.Contains(line, "/")
	if strings.HasPrefix(line, "/") {
		line = strings.TrimPrefix(line, "/")
	}
	if line == "" {
		return rule, false
	}
	for _, seg := range strings.Split(line, "/") {
		if seg == "" {
			return rule, false
		}
		if !validGlobSegment(seg) {
			return rule, false
		}
		rule.segments = append(rule.segments, seg)
	}
	return rule, true
}

// lastMatchingRule returns the last rule in the source that matches the path
// (within one source, the last matching rule decides — git's rule). baseDepth
// is the source file's directory depth below the repo root: anchored patterns
// are relative to it, per git.
func lastMatchingRule(rules []ignoreRule, relPath string, baseDepth int, isDir bool) *ignoreRule {
	var match *ignoreRule
	for i := range rules {
		if ruleMatches(&rules[i], relPath, baseDepth, isDir) {
			match = &rules[i]
		}
	}
	return match
}

// ruleMatches applies gitignore matching semantics to one rule: anchored
// patterns match from the ignore file's own directory (segment-wise, with
// "**"); basename patterns match the path's final segment; directory-only
// rules never match a file path.
func ruleMatches(rule *ignoreRule, relPath string, baseDepth int, isDir bool) bool {
	if rule.dirOnly && !isDir {
		return false
	}
	segs := strings.Split(relPath, "/")
	// A source file's patterns apply only to paths BELOW its directory
	// (git semantics): a source at baseDepth never decides a path at or
	// above that depth — in particular a basename rule inside
	// <dir>/.gitignore never matches <dir> itself, and a basename rule in
	// a/.gitignore never matches the prefix "a" (only "a/…" below it).
	if len(segs) <= baseDepth {
		return false
	}
	if rule.anchored {
		return matchSegments(rule.segments, segs[baseDepth:])
	}
	// A pattern with no slash matches the basename at any depth.
	return matchSegments(rule.segments[len(rule.segments)-1:], segs[len(segs)-1:])
}

// matchSegments matches pattern segments against path segments with "**"
// matching zero or more whole segments.
func matchSegments(pat, path []string) bool {
	if len(pat) == 0 {
		return len(path) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(path); i++ {
			if matchSegments(pat[1:], path[i:]) {
				return true
			}
		}
		return false
	}
	if len(path) == 0 {
		return false
	}
	if !globMatch(pat[0], path[0]) {
		return false
	}
	return matchSegments(pat[1:], path[1:])
}

// validGlobSegment rejects constructs the segment matcher does not interpret
// (fail closed rather than match wrong): unterminated escapes and stray
// unterminated character classes.
func validGlobSegment(seg string) bool {
	for i := 0; i < len(seg); i++ {
		switch seg[i] {
		case '\\':
			if i+1 >= len(seg) {
				return false
			}
			i++
		case '[':
			end := classEnd(seg[i:])
			if end <= 0 {
				return false
			}
			i += end - 1
		}
	}
	return true
}

// classEnd returns the length of the character class starting at "[", or 0
// when unterminated.
func classEnd(seg string) int {
	if len(seg) < 2 {
		return 0
	}
	i := 1
	if seg[i] == '!' || seg[i] == '^' {
		i++
	}
	if i < len(seg) && seg[i] == ']' {
		i++ // a ']' first in the class is a literal member
	}
	for ; i < len(seg); i++ {
		switch seg[i] {
		case '\\':
			if i+1 >= len(seg) {
				return 0
			}
			i++
		case ']':
			return i + 1
		}
	}
	return 0
}

// globMatch matches one path segment against one pattern segment with the
// gitignore wildcards: "*" (any run of non-separator bytes), "?" (any single
// byte), "[...]" (character class, "!"/"^" negation, ranges) and backslash
// escapes. Iterative with single-star backtracking.
func globMatch(pat, name string) bool {
	var p, n int
	starP, starN := -1, 0
	for n < len(name) {
		if p < len(pat) {
			if consumed, ok := matchGlobStep(pat[p:], name[n]); ok {
				p += consumed
				n++
				continue
			}
		}
		if starP >= 0 {
			starN++
			n = starN
			p = starP + 1
			continue
		}
		return false
	}
	for p < len(pat) && pat[p] == '*' {
		p++
	}
	return p == len(pat)
}

// matchGlobStep matches one pattern position against one name byte.
// consumed is the pattern bytes used; ok=false means no match at this step.
func matchGlobStep(pat string, b byte) (consumed int, ok bool) {
	switch pat[0] {
	case '*':
		return 1, true
	case '?':
		return 1, true
	case '\\':
		if len(pat) < 2 {
			return 0, false
		}
		return 2, pat[1] == b
	case '[':
		end := classEnd(pat)
		if end == 0 {
			return 0, false
		}
		return end, classMatch(pat[:end], b)
	default:
		return 1, pat[0] == b
	}
}

// classMatch reports whether byte b matches the class "[...]" (seg includes
// the brackets).
func classMatch(seg string, b byte) bool {
	i := 1
	negate := false
	if i < len(seg) && (seg[i] == '!' || seg[i] == '^') {
		negate = true
		i++
	}
	matched := false
	first := true
	for i < len(seg) {
		if seg[i] == ']' && !first {
			i++
			break
		}
		first = false
		lo, width := classLiteral(seg, i)
		i += width
		hi := lo
		if i+1 < len(seg) && seg[i] == '-' && seg[i+1] != ']' {
			hi, width = classLiteral(seg, i+1)
			i += width
		}
		if b >= lo && b <= hi {
			matched = true
		}
	}
	return matched != negate
}

// classLiteral decodes one class member starting at i (honouring escapes).
func classLiteral(seg string, i int) (byte, int) {
	if seg[i] == '\\' && i+1 < len(seg) {
		return seg[i+1], 2
	}
	return seg[i], 1
}

// unescapeGlob strips backslash escapes from a literal-name rule line.
func unescapeGlob(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' && i+1 < len(line) {
			i++
		}
		b.WriteByte(line[i])
	}
	return b.String()
}

func beUint16(b []byte) uint16 {
	return uint16(b[0])<<8 | uint16(b[1])
}

func beUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}
