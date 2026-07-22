// Package sandbox — git-evidence protection layer (ADR-053 D17 + spike caveat 3).
//
// # Why this file exists
//
// The Plan/Play engine keeps a hidden go-git repository at "<workspace>/work/.git"
// (created in-process by the git-evidence layer, pkg/gitevidence). Boundary
// commits into that repo are the Judge's rung-1 evidence — the tamper-evident
// record of exactly what each attempt changed. The go-git spike (ADR §6.1)
// returned GO but surfaced caveat 3, verified empirically:
//
//	A go-git repository is a byte-identical standard ".git". The real `git`
//	CLI can `commit --amend`, `rebase`, `rm`, `reset`, or directly rewrite
//	.git/objects against it. Therefore a tool-surface deny of the git verbs is
//	NOT sufficient — an agent with `bash` can forge the evidence trail behind
//	the engine's back.
//
// D17 (FR-152) resolves this with a PAIR of controls:
//
//	1. Operation-level policy on the agent-facing git surface:
//	   allow {log, blame, show, diff}; deny {commit, amend, rebase, rm}.
//	   Because commits happen in-process via go-git, the agent-callable git
//	   surface is what this policy gates — see CheckGitOp.
//	2. A kernel/sandbox-level block on agent shell access to work/.git/, so the
//	   `git commit --amend` / `rm -rf .git` bypass through `bash`/exec is denied
//	   before the child ever runs — see GitGuard.InspectExec, wired into
//	   hardened_exec.Run.
//
// # Enforcement layer and its honest limits
//
// The block is expressed at the exec chokepoint (bash-policy), NOT at the
// Landlock layer, for two grounded reasons:
//
//   - Landlock is a hierarchical ALLOW-list and cannot subtract: an allow rule
//     on a parent directory grants that access to the entire subtree, and a
//     nested rule can only ADD rights, never remove them (verified by the
//     kernel walk in security/landlock/fs.c and documented in this package's
//     own DefaultChildPolicy carve-out). work/.git sits under the blanket-RW
//     $OMNIPUS_HOME grant, so "writable work/ but read-only work/.git" is not
//     expressible in a single Landlock ruleset without enumerating and
//     re-granting the entire home tree per-directory — infeasible and fragile.
//   - Landlock is frequently unavailable at runtime (containers, this project's
//     Fly.io target: ABI probes 0, SelectBackend returns the fallback). The
//     exec-chokepoint guard runs on EVERY platform, which is exactly the
//     graceful-degradation contract (Constraint #4) the spec requires.
//
// The exec guard is deterministic and robust for the direct vectors (a git
// verb run against a protected repo; a mutating command naming a protected
// .git path; a `sh -c` wrapper doing either). It is best-effort against a
// fully adversarial obfuscated shell (path indirection via env/base64, a
// statically-linked custom binary that opens .git via raw syscalls) — that
// residual is LIM-02 class and would require the kernel layer, which cannot
// express this policy here. The guard therefore FAILS CLOSED: any git verb
// that is not on the read allow-list, run against a protected repo, is denied.

package sandbox

import (
	"path/filepath"
	"strings"
	"sync"
)

// gitReadVerbs is the D17 allow-set: the git subcommands that only READ the
// object store / working tree and produce evidence the Judge relies on
// (log/blame/show/diff). Anything not in this set is denied against a
// protected repo — deny-by-default / fail-closed, so a new mutating verb added
// by a future git release is blocked without a code change here.
var gitReadVerbs = map[string]struct{}{
	"log":   {},
	"blame": {},
	"show":  {},
	"diff":  {},
}

// gitMutatingVerbs is the explicitly-named D17 deny-set. It exists for precise
// PolicyRule attribution and to document intent; enforcement does NOT depend on
// membership here (deny-by-default already covers every non-read verb), but a
// match yields a sharper explanation in the audit trail.
var gitMutatingVerbs = map[string]struct{}{
	"commit":        {}, // includes `commit --amend`
	"rebase":        {},
	"rm":            {},
	"reset":         {},
	"checkout":      {}, // can overwrite tracked files / move HEAD
	"switch":        {},
	"restore":       {},
	"merge":         {},
	"cherry-pick":   {},
	"revert":        {},
	"apply":         {},
	"am":            {},
	"stash":         {},
	"clean":         {},
	"gc":            {},
	"prune":         {},
	"reflog":        {}, // `reflog delete/expire` rewrites history
	"update-ref":    {},
	"update-index":  {},
	"filter-branch": {},
	"replace":       {},
	"tag":           {}, // `-d`/`-f` mutate refs
	"branch":        {}, // `-D`/`-f` mutate refs
	"push":          {}, // can push --force to rewrite a remote mirror of the evidence
	"fetch":         {},
	"pull":          {},
	"init":          {}, // re-init would clobber the engine's repo
	"clone":         {},
	"config":        {}, // writes .git/config (hooks, filters → arbitrary code on commit)
	"hook":          {},
	"worktree":      {},
	"notes":         {},
	"mv":            {},
	"add":           {}, // stages into the index the engine owns
	"symbolic-ref":  {},
}

// nonGitMutators is the set of common command basenames that can write to or
// delete files. When one of these names a path at/under a protected .git, the
// invocation is denied. Read-only tools (cat, less, grep, head, tail, file,
// stat, git-log-via-git) are intentionally ABSENT so D17 reads still work.
var nonGitMutators = map[string]struct{}{
	"rm": {}, "rmdir": {}, "unlink": {}, "shred": {},
	"mv": {}, "cp": {}, "install": {}, "ln": {}, "link": {},
	"tee": {}, "dd": {}, "truncate": {}, "touch": {},
	"chmod": {}, "chown": {}, "chgrp": {}, "chattr": {},
	"sed": {}, "perl": {}, "awk": {}, // in-place edit modes / arbitrary writes
	"mkdir": {}, "mkfifo": {}, "mknod": {},
}

// shellNames are the interpreters whose `-c <script>` argument is scanned for
// embedded git-tamper / .git-write patterns (mirrors the master.key shell-guard
// philosophy already used in pkg/tools/shell.go).
var shellNames = map[string]struct{}{
	"sh": {}, "bash": {}, "dash": {}, "zsh": {}, "ksh": {}, "ash": {}, "busybox": {},
}

// GitOpDecision is the explainable result of a D17 operation-policy check on a
// single git verb (SEC-17 style: every allow AND every deny carries a
// PolicyRule).
type GitOpDecision struct {
	Verb       string
	Allowed    bool
	PolicyRule string
}

// CheckGitOp applies the D17 operation-level policy to a git subcommand verb.
// allow {log, blame, show, diff}; deny everything else (fail-closed). This is
// the gate for the agent-callable git surface — the git-evidence layer performs
// the actual commits in-process, so a caller must never expose a raw mutating
// verb to an agent; it routes verbs through here first.
//
// verb is the git subcommand ("commit", "log", …); comparison is
// case-insensitive and tolerant of a leading "git" token being passed in.
func CheckGitOp(verb string) GitOpDecision {
	v := strings.ToLower(strings.TrimSpace(verb))
	v = strings.TrimPrefix(v, "git ")
	v = strings.TrimSpace(v)
	if v == "" {
		return GitOpDecision{
			Verb:       verb,
			Allowed:    false,
			PolicyRule: "git.op.deny: empty verb (deny-by-default)",
		}
	}
	if _, ok := gitReadVerbs[v]; ok {
		return GitOpDecision{
			Verb:       v,
			Allowed:    true,
			PolicyRule: "git.op.allow: read verb (D17 allow-set log/blame/show/diff)",
		}
	}
	if _, ok := gitMutatingVerbs[v]; ok {
		return GitOpDecision{
			Verb:       v,
			Allowed:    false,
			PolicyRule: "git.op.deny: mutating verb '" + v + "' (D17 deny-set) — commits are in-process only",
		}
	}
	return GitOpDecision{
		Verb:       v,
		Allowed:    false,
		PolicyRule: "git.op.deny: verb '" + v + "' not in D17 read allow-set (deny-by-default)",
	}
}

// ExecDecision is the explainable result of GitGuard.InspectExec.
type ExecDecision struct {
	Allowed    bool
	Reason     string // human-readable, safe to surface to the agent
	PolicyRule string // machine-stable rule id (SEC-17), empty when Allowed
	GitDir     string // the protected .git this decision concerns, if any
	Verb       string // the git verb, if a git invocation was recognised
}

// allow is the canonical "no objection" decision.
func allowExec() ExecDecision { return ExecDecision{Allowed: true} }

// GitGuard tracks the set of workspace directories whose "<dir>/.git" is a
// protected, engine-owned evidence repository, and decides whether a given
// exec invocation would tamper with one.
//
// It is safe for concurrent use. Protected work dirs are stored canonicalized
// (absolute, symlink-resolved) so path comparisons are traversal- and
// symlink-safe.
type GitGuard struct {
	mu       sync.RWMutex
	workdirs map[string]struct{} // canonical workspace dir → protected <dir>/.git
}

// NewGitGuard returns an empty guard. Until a repo is registered with Protect,
// InspectExec allows everything (zero false positives when the git layer is
// inactive).
func NewGitGuard() *GitGuard {
	return &GitGuard{workdirs: make(map[string]struct{})}
}

// Protect registers workDir so that "<workDir>/.git" becomes a protected
// evidence repository: the exec guard will deny any bash/exec invocation that
// mutates it. workDir is the directory that CONTAINS .git (i.e. the go-git
// working tree root), matching the fixed convention "<workspace>/work".
//
// The path is canonicalized; a relative path is resolved against the current
// working directory. Registering the same dir twice is idempotent.
func (g *GitGuard) Protect(workDir string) error {
	canon, err := canonicalizePath(workDir)
	if err != nil {
		// Fall back to a cleaned absolute path when the dir does not yet exist
		// (Protect may be called before the working tree is created). This
		// keeps protection active for a not-yet-materialized work dir.
		abs, aerr := filepath.Abs(workDir)
		if aerr != nil {
			return aerr
		}
		canon = filepath.Clean(abs)
	}
	g.mu.Lock()
	g.workdirs[canon] = struct{}{}
	g.mu.Unlock()
	return nil
}

// Unprotect removes a previously-registered work dir (e.g. when a workspace is
// torn down). Unknown dirs are ignored.
func (g *GitGuard) Unprotect(workDir string) {
	canon, err := canonicalizePath(workDir)
	if err != nil {
		if abs, aerr := filepath.Abs(workDir); aerr == nil {
			canon = filepath.Clean(abs)
		} else {
			canon = filepath.Clean(workDir)
		}
	}
	g.mu.Lock()
	delete(g.workdirs, canon)
	g.mu.Unlock()
}

// ProtectedWorkdirs returns a snapshot of the registered work dirs (canonical).
func (g *GitGuard) ProtectedWorkdirs() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, 0, len(g.workdirs))
	for d := range g.workdirs {
		out = append(out, d)
	}
	return out
}

// ProtectedGitDir returns the protected .git path for a work dir
// ("<workDir>/.git"), canonicalized the same way Protect stores it. It does not
// require the dir to be registered — it is a pure path helper for callers.
func ProtectedGitDir(workDir string) string {
	canon, err := canonicalizePath(workDir)
	if err != nil {
		if abs, aerr := filepath.Abs(workDir); aerr == nil {
			canon = filepath.Clean(abs)
		} else {
			canon = filepath.Clean(workDir)
		}
	}
	return filepath.Join(canon, ".git")
}

// resolvedGitDirs returns the canonical "<workDir>/.git" for every registered
// work dir. Caller must not hold the lock.
func (g *GitGuard) resolvedGitDirs() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, 0, len(g.workdirs))
	for d := range g.workdirs {
		out = append(out, filepath.Join(d, ".git"))
	}
	return out
}

// resolvedWorkdirs returns the canonical work dirs. Caller must not hold lock.
func (g *GitGuard) resolvedWorkdirs() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, 0, len(g.workdirs))
	for d := range g.workdirs {
		out = append(out, d)
	}
	return out
}

// IsProtectedGitPath reports whether path (absolute or relative to cwd) is at or
// under any registered protected .git directory. Symlinks and ".." traversal in
// path are resolved best-effort before comparison.
func (g *GitGuard) IsProtectedGitPath(path string) bool {
	resolved := resolvePathBestEffort(path, "")
	for _, gitDir := range g.resolvedGitDirs() {
		if pathIsUnder(resolved, gitDir) {
			return true
		}
	}
	return false
}

// CheckGitPathWrite reports whether writing to / mutating path (absolute or
// relative to the process cwd) is permitted. It DENIES any write at or under a
// protected .git — D17 allows reads (log/blame/show/diff) but never writes to
// the evidence store. Cooperative file-writing tools (the write_file / edit
// surface, distinct from bash/exec) call this so the .git write-block covers
// the non-exec surface too. Reads are out of scope here (this is a write gate).
func (g *GitGuard) CheckGitPathWrite(path string) ExecDecision {
	resolved := resolvePathBestEffort(path, "")
	if p, gd := g.matchesProtectedGit(resolved); p {
		return ExecDecision{
			Allowed:    false,
			GitDir:     gd,
			Reason:     "write to protected git-evidence path " + path + " is denied (D17: reads only)",
			PolicyRule: "git.sandbox.deny: write under protected .git (D17)",
		}
	}
	return allowExec()
}

// enclosingProtectedWorkdir reports the canonical protected work dir that
// contains dir (or equals it), if any. Used to decide whether a git invocation
// whose effective cwd is dir operates on a protected repo.
func (g *GitGuard) enclosingProtectedWorkdir(dir string) (string, bool) {
	for _, wd := range g.resolvedWorkdirs() {
		if pathIsUnder(dir, wd) {
			return wd, true
		}
	}
	return "", false
}

// resolvePathBestEffort turns p into an absolute, cleaned, symlink-resolved
// path. Relative paths are joined onto base (or the process cwd when base is
// empty). Canonicalization failures (path does not exist yet) degrade to a
// cleaned absolute path so the guard never silently allows on a resolve error.
func resolvePathBestEffort(p, base string) string {
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		if base != "" {
			p = filepath.Join(base, p)
		} else {
			p = filepath.Clean(p)
		}
	}
	if canon, err := canonicalizePath(p); err == nil {
		return canon
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return filepath.Clean(abs)
}

// baseName returns the command basename lowercased, stripping any directory and
// (best-effort) a trailing platform suffix. E.g. "/usr/bin/git" → "git".
func baseName(cmd string) string {
	b := filepath.Base(strings.TrimSpace(cmd))
	return strings.ToLower(b)
}

// InspectExec is the bash-policy .git/ block. It decides whether launching argv
// (with the child's working directory cwd) would tamper with a protected
// evidence repository, and returns an explainable ExecDecision.
//
// Decision order (fail-closed):
//  1. No protected repos registered → allow (guard inactive).
//  2. `git` invocation → parse global options (-C, --git-dir, -c, --no-pager,
//     …) to find the EFFECTIVE repo location and the subcommand verb. If the
//     effective repo is protected and the verb is not a D17 read verb → deny.
//  3. Shell wrapper (`sh -c <script>`) whose effective cwd is a protected repo,
//     or whose script names a protected .git path with a mutating construct →
//     deny.
//  4. Any command whose arguments name a path at/under a protected .git AND
//     whose basename is a known file mutator → deny.
//  5. Otherwise → allow.
func (g *GitGuard) InspectExec(argv []string, cwd string) ExecDecision {
	if len(argv) == 0 {
		return allowExec()
	}
	g.mu.RLock()
	active := len(g.workdirs) > 0
	g.mu.RUnlock()
	if !active {
		return allowExec()
	}

	cmd := baseName(argv[0])

	// (2) Direct git invocation.
	if cmd == "git" {
		if d := g.inspectGitArgs(argv[1:], cwd); d.PolicyRule != "" || !d.Allowed {
			return d
		}
		// git invocation that does not target a protected repo, or a read verb.
		return allowExec()
	}

	// (3) Shell wrapper: scan `-c <script>`.
	if _, isShell := shellNames[cmd]; isShell {
		if d := g.inspectShell(argv[1:], cwd); !d.Allowed {
			return d
		}
	}

	// (4) Non-git file mutator naming a protected .git path.
	if _, isMut := nonGitMutators[cmd]; isMut {
		for _, tok := range argv[1:] {
			if tok == "" || strings.HasPrefix(tok, "-") {
				continue
			}
			resolved := resolvePathBestEffort(tok, cwd)
			if p, gd := g.matchesProtectedGit(resolved); p {
				return ExecDecision{
					Allowed:    false,
					GitDir:     gd,
					Reason:     "command '" + cmd + "' would modify protected git-evidence path " + tok,
					PolicyRule: "git.sandbox.deny: mutator '" + cmd + "' targets protected .git (D17 sandbox block)",
				}
			}
		}
	}

	return allowExec()
}

// matchesProtectedGit reports whether resolved is at/under any protected .git.
func (g *GitGuard) matchesProtectedGit(resolved string) (bool, string) {
	for _, gitDir := range g.resolvedGitDirs() {
		if pathIsUnder(resolved, gitDir) {
			return true, gitDir
		}
	}
	return false, ""
}

// inspectGitArgs parses a `git` command's arguments (everything after argv[0]),
// determines the effective repo dir and subcommand verb, and applies D17.
//
// Global options handled (per git(1)): -C <path> (repeatable, cumulative),
// --git-dir[=]<path>, --work-tree[=]<path>, -c <name=value> / -c=<..>,
// --no-pager / -p / --paginate, --bare, --no-replace-objects, --literal-pathspecs,
// --exec-path[=], --namespace[=], --config-env[=]. Unknown leading options are
// skipped conservatively; the first non-option token is the verb.
func (g *GitGuard) inspectGitArgs(args []string, cwd string) ExecDecision {
	effCwd := cwd
	if effCwd == "" {
		effCwd = "."
	}
	var gitDirOverride string
	verb := ""

	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-C":
			if i+1 < len(args) {
				effCwd = joinDir(effCwd, args[i+1])
				i += 2
				continue
			}
			i++
		case strings.HasPrefix(a, "-C"):
			effCwd = joinDir(effCwd, a[len("-C"):])
			i++
		case a == "--git-dir":
			if i+1 < len(args) {
				gitDirOverride = args[i+1]
				i += 2
				continue
			}
			i++
		case strings.HasPrefix(a, "--git-dir="):
			gitDirOverride = a[len("--git-dir="):]
			i++
		case a == "--work-tree" || a == "-c" || a == "--namespace" ||
			a == "--exec-path" || a == "--config-env":
			// Option that consumes the next token; skip both.
			i += 2
		case strings.HasPrefix(a, "--work-tree=") ||
			strings.HasPrefix(a, "-c=") ||
			strings.HasPrefix(a, "--namespace=") ||
			strings.HasPrefix(a, "--exec-path=") ||
			strings.HasPrefix(a, "--config-env="):
			i++
		case a == "-p" || a == "--paginate" || a == "--no-pager" ||
			a == "--bare" || a == "--no-replace-objects" ||
			a == "--literal-pathspecs" || a == "--glob-pathspecs" ||
			a == "--noglob-pathspecs" || a == "--icase-pathspecs" ||
			a == "--no-optional-locks" || a == "--html-path" ||
			a == "--version" || a == "--help":
			i++
		case strings.HasPrefix(a, "-"):
			// Unknown global option; skip it (conservative — do not treat as verb).
			i++
		default:
			verb = a
			// Remaining args after the verb (args[i+1:]) are the subcommand's
			// own arguments; we look at them below for `commit --amend` detail.
			return g.decideGit(verb, args[i+1:], effCwd, gitDirOverride, cwd)
		}
	}
	// No verb found (e.g. `git --version`) → nothing to gate.
	return allowExec()
}

// decideGit applies D17 given a resolved verb, its args, the effective cwd, and
// an optional --git-dir override.
func (g *GitGuard) decideGit(verb string, verbArgs []string, effCwd, gitDirOverride, rawCwd string) ExecDecision {
	// Determine whether this git invocation operates on a protected repo.
	protected := false
	var gitDir string
	if gitDirOverride != "" {
		resolved := resolvePathBestEffort(gitDirOverride, rawCwd)
		if p, gd := g.matchesProtectedGit(resolved); p {
			protected, gitDir = true, gd
		} else {
			// A --git-dir that IS exactly a protected .git (equality, not "under").
			for _, gd := range g.resolvedGitDirs() {
				if resolved == gd {
					protected, gitDir = true, gd
					break
				}
			}
		}
	} else {
		resolvedCwd := resolvePathBestEffort(effCwd, "")
		if wd, ok := g.enclosingProtectedWorkdir(resolvedCwd); ok {
			protected = true
			gitDir = filepath.Join(wd, ".git")
		}
	}

	if !protected {
		// git operating on some other repo (or no repo) — not our concern.
		// Return an explicit allow WITH a PolicyRule so InspectExec treats the
		// git branch as handled and does not fall through to the mutator scan.
		return ExecDecision{Allowed: true, Verb: strings.ToLower(verb), PolicyRule: "git.sandbox.allow: not a protected repo"}
	}

	op := CheckGitOp(verb)
	if op.Allowed {
		return ExecDecision{
			Allowed:    true,
			Verb:       op.Verb,
			GitDir:     gitDir,
			PolicyRule: "git.sandbox.allow: read verb on protected repo (" + op.PolicyRule + ")",
		}
	}

	reason := "git '" + op.Verb + "' is denied on the protected evidence repo"
	if op.Verb == "commit" && hasAmend(verbArgs) {
		reason = "git 'commit --amend' would rewrite the protected evidence history"
	}
	return ExecDecision{
		Allowed:    false,
		Verb:       op.Verb,
		GitDir:     gitDir,
		Reason:     reason,
		PolicyRule: "git.sandbox.deny: " + op.PolicyRule,
	}
}

// hasAmend reports whether the commit subcommand args request --amend.
func hasAmend(args []string) bool {
	for _, a := range args {
		if a == "--amend" || strings.HasPrefix(a, "--amend=") {
			return true
		}
	}
	return false
}

// joinDir applies git's -C semantics: a relative path is joined onto the
// current effective dir; an absolute path replaces it.
func joinDir(cur, next string) string {
	if next == "" {
		return cur
	}
	if filepath.IsAbs(next) {
		return filepath.Clean(next)
	}
	return filepath.Clean(filepath.Join(cur, next))
}

// inspectShell scans a shell interpreter's arguments for a `-c <script>` and,
// when found, evaluates the script against the protected repos. It denies when:
//   - the effective cwd is a protected repo AND the script contains a mutating
//     git verb or a `.git`-write construct, OR
//   - the script names a protected .git path together with a mutating construct.
//
// This is the same defense-in-depth layer as the master.key/credentials.json
// shell-guard: it is not a full shell parser (LIM-02), but it closes the
// obvious `sh -c 'git commit --amend'` and `sh -c 'rm -rf .git'` vectors.
func (g *GitGuard) inspectShell(args []string, cwd string) ExecDecision {
	script := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" && i+1 < len(args) {
			script = args[i+1]
			break
		}
		if strings.HasPrefix(args[i], "-c") && len(args[i]) > 2 {
			script = args[i][2:]
			break
		}
	}
	if script == "" {
		return allowExec()
	}
	lower := strings.ToLower(script)

	resolvedCwd := resolvePathBestEffort(cwd, "")
	_, cwdProtected := g.enclosingProtectedWorkdir(resolvedCwd)

	// (a) mutating git verbs anywhere in the script, when cwd is a protected repo.
	if cwdProtected {
		for _, pat := range gitMutatingScriptPatterns() {
			if strings.Contains(lower, pat) {
				return ExecDecision{
					Allowed:    false,
					Reason:     "shell script runs a mutating git command in the protected evidence repo",
					PolicyRule: "git.sandbox.deny: shell wrapper mutates protected repo (pattern '" + pat + "')",
				}
			}
		}
	}

	// (b) explicit protected .git path token combined with any mutating
	//     construct anywhere in the script.
	if g.scriptNamesProtectedGit(script, cwd) {
		for _, m := range shellMutatingTokens() {
			if strings.Contains(lower, m) {
				return ExecDecision{
					Allowed:    false,
					Reason:     "shell script names a protected .git path with a write/delete construct",
					PolicyRule: "git.sandbox.deny: shell wrapper writes protected .git (token '" + m + "')",
				}
			}
		}
	}
	return allowExec()
}

// gitMutatingScriptPatterns returns lowercase substrings that indicate a
// mutating git invocation inside a shell script.
func gitMutatingScriptPatterns() []string {
	return []string{
		"git commit", "git rebase", "git rm", "git reset", "git checkout",
		"git merge", "git cherry-pick", "git revert", "git apply", "git am",
		"git clean", "git gc", "git prune", "git reflog", "git update-ref",
		"git filter-branch", "git replace", "git push", "git init", "git config",
		"git update-index", "git symbolic-ref", "git worktree", "git stash",
		"git branch -d", "git branch -f", "git tag -d", "git tag -f",
		"--amend",
	}
}

// shellMutatingTokens returns lowercase substrings that indicate a write/delete
// construct in a shell script.
func shellMutatingTokens() []string {
	return []string{
		"rm ", "rm -", "rmdir", "mv ", "cp ", "tee ", "dd ", "truncate",
		"shred", "> ", ">>", "chmod", "chown", "ln ", "install ",
		"git commit", "git rebase", "git rm", "git reset", "--amend",
	}
}

// scriptNamesProtectedGit reports whether the script text references a path that
// resolves to a protected .git. It tokenizes on shell separators and resolves
// each token that contains ".git" against cwd.
func (g *GitGuard) scriptNamesProtectedGit(script, cwd string) bool {
	fields := strings.FieldsFunc(script, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ';', '&', '|', '(', ')', '"', '\'', '=':
			return true
		}
		return false
	})
	for _, f := range fields {
		if !strings.Contains(f, ".git") {
			continue
		}
		resolved := resolvePathBestEffort(f, cwd)
		if p, _ := g.matchesProtectedGit(resolved); p {
			return true
		}
		// Also catch a bare "<protectedWorkdir>/.git" typed as the token even
		// when the .git does not exist yet (resolvePathBestEffort handles that,
		// but guard against relative ".git" when cwd is the protected repo).
		if f == ".git" || strings.HasSuffix(f, "/.git") {
			if _, ok := g.enclosingProtectedWorkdir(resolvePathBestEffort(cwd, "")); ok {
				return true
			}
		}
	}
	return false
}

// ---- Package-level default guard ------------------------------------------

// DefaultGitGuard is the process-wide guard consulted by hardened_exec.Run.
// The git-evidence layer (pkg/gitevidence, W1-git) and the Play engine (W2)
// register protected work dirs here via ProtectRepoWorkdir so that EVERY
// hardened-exec child is automatically vetted — callers do not have to remember
// to check.
var DefaultGitGuard = NewGitGuard()

// ProtectRepoWorkdir registers "<workDir>/.git" as a protected evidence repo on
// the process-wide DefaultGitGuard. This is the primary entry point W1-git/W2
// call to protect a repo path.
func ProtectRepoWorkdir(workDir string) error { return DefaultGitGuard.Protect(workDir) }

// UnprotectRepoWorkdir removes a work dir from the process-wide guard.
func UnprotectRepoWorkdir(workDir string) { DefaultGitGuard.Unprotect(workDir) }

// InspectExecForGit vets argv/cwd against the process-wide DefaultGitGuard.
func InspectExecForGit(argv []string, cwd string) ExecDecision {
	return DefaultGitGuard.InspectExec(argv, cwd)
}

// CheckGitPathWrite vets a single file-write path against the process-wide
// DefaultGitGuard (for cooperative file-tools; bash/exec goes through
// hardened_exec.Run automatically).
func CheckGitPathWrite(path string) ExecDecision {
	return DefaultGitGuard.CheckGitPathWrite(path)
}

// IsProtectedGitPath reports whether path is at/under any protected .git on the
// process-wide DefaultGitGuard.
func IsProtectedGitPath(path string) bool {
	return DefaultGitGuard.IsProtectedGitPath(path)
}
