// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file implements ADR-092's Auto-mode pre-flight: the filesystem
// evaluator (D7) and the network evaluator (D8). Both run BEFORE a bash
// command spawns and decide whether the turn's already-authored
// fspolicy.FSPolicy (filesystem) or network-grant state (network) already
// covers what the command needs, or whether an escalation prompt is
// required first (FR-009: "never start-then-interrupt").
//
// # Single source of truth (D7's own text)
//
// The functions here do not decide policy on their own authority — they
// evaluate the SAME fspolicy.FSPolicy value sandbox.DeriveKernelPolicy
// renders into the kernel ruleset (pkg/sandbox/derive_from_fspolicy.go).
// EvaluateFSPreflight and DeriveKernelPolicy are two readers of one input,
// not two derivations, and the Level 1 anti-drift lock test
// (pkg/sandbox/*_antidrift_test.go) asserts they agree for every
// {path, operation} pair in a fixed FSPolicy including a non-empty
// PathGrants — the property this whole file exists to make true, not merely
// documented.
//
// # Why this file does not touch pkg/tools/shell*.go
//
// ADR-092's delivery plan (spec §8.2) splits ownership: this file (L3) owns
// both pre-flight evaluators; pkg/tools/shell*.go and resolvepath.go (L4)
// own the runtime call sites that invoke them, the approval-dialog wiring,
// and ApprovalGrantStore consultation. FR-038's operation classifier is
// therefore a SELF-CONTAINED extractor here — ClassifyPathOperations — not
// a reuse of shell_path_guard.go's pathUseVerdict/newPathUseClassifier
// (which answer a narrower, guard-specific question: is this ONE reference
// read-only enough to pass the path guard). Both classifiers may run
// against the same command text; they answer different questions for
// different consumers.
package tools

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// ---------------------------------------------------------------------
// D7 — filesystem pre-flight
// ---------------------------------------------------------------------

// PathOperation is one {path, operation} pair the FR-038 classifier extracts
// from a shell command's text — the raw material the D7 filesystem
// pre-flight evaluates against the turn's fspolicy.FSPolicy.
//
// Path is the literal token exactly as it appeared in the command, NOT YET
// resolved to an absolute/realpath'd form (that resolution is the caller's
// job — see EvaluateFSPreflight's own contract — so this type carries text,
// not a decision).
type PathOperation struct {
	Path string

	// Access is the fspolicy.PathGrantAccessRead|fspolicy.PathGrantAccessWrite
	// bitmask this reference needs — the same vocabulary FR-036 requires
	// PathGrant to reuse, applied one level up at the classifier's own
	// output. Never carries PathGrantAccessExecute: a command's own exec
	// path is D3's resolve-and-verify concern, not D7's.
	Access uint64
}

// ClassifyPathOperations is the FR-038 operation classifier. It splits
// command on the same shell operators splitShellSegments already
// understands (no new parser — FR-020's own principle applied here too),
// and extracts a {path, operation} pair per data-touching reference in each
// segment.
//
// Returns ok=false when any segment contains a construct this extractor
// cannot safely reason about — an unbalanced quote, or a $()/backtick
// command/parameter substitution. This mirrors FR-020's "blind spots route
// to ask" posture (§5.3 R9/R10/R12-R14): a command this classifier cannot
// parse must escalate, never silently proceed as though it touched nothing.
func ClassifyPathOperations(command string) ([]PathOperation, bool) {
	var all []PathOperation
	for _, seg := range splitShellSegments(command) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		ops, ok := classifySegmentPathOperations(seg)
		if !ok {
			return nil, false
		}
		all = append(all, ops...)
	}
	return all, true
}

// classifySegmentPathOperations classifies ONE already-split segment
// (§5.4's C1-C8 dataset is entirely single-segment). Command-specific rules
// come first — the copy-like family (cp/ln/install: sources read, last arg
// write), mv (sources read+write, last arg write), dd (if=/of=), the
// write-only family (rm/tee/touch/mkdir/chmod/chown/truncate: every
// absolute-path argument is a write — chmod/chown's mode/owner argument and
// truncate/install's flag values are never absolute paths, so they are
// filtered out by absShellPathArgs without a special case), rsync/scp
// (write-capable family with a network shape — remote endpoints like
// `user@host:/path` are not absolute local paths and are invisible to this
// classifier by design, so a single surviving local path could be either
// upload source or download destination and is treated as a write, the
// safe direction), and sed (a write ONLY when an in-place flag, `-i` or
// `-i<suffix>`, is present — otherwise sed reads and writes stdout, not the
// file). Everything else falls through to the default rule: a redirection
// target (`>`, `>>`) is always a write, and any other bare absolute-path
// argument to an unrecognized command is treated as a read (the
// conservative default — see EvaluateFSPreflight's own note that a
// read-only verdict is a no-op for every non-ReadConfined agent, so
// over-classifying a word as "read" never produces a spurious escalation —
// which is exactly why every command here defaults toward WRITE rather than
// silently falling through to this read-only default: an under-classified
// write is the actual escalation gap, not an over-classified read).
func classifySegmentPathOperations(segment string) ([]PathOperation, bool) {
	words, ok := tokenizeShellWords(segment)
	if !ok {
		return nil, false
	}
	if len(words) == 0 {
		return nil, true
	}

	head, args := resolveShellHead(words)

	var ops []PathOperation
	redirectTargets := map[int]struct{}{}
	for i := 0; i < len(args); i++ {
		w := args[i]
		switch {
		case (w == ">" || w == ">>") && i+1 < len(args) && isAbsShellPath(args[i+1]):
			ops = append(ops, PathOperation{Path: args[i+1], Access: fspolicy.PathGrantAccessWrite})
			redirectTargets[i+1] = struct{}{}
			i++
		case strings.HasPrefix(w, ">>") && isAbsShellPath(w[2:]):
			ops = append(ops, PathOperation{Path: w[2:], Access: fspolicy.PathGrantAccessWrite})
			redirectTargets[i] = struct{}{}
		case strings.HasPrefix(w, ">") && isAbsShellPath(w[1:]):
			ops = append(ops, PathOperation{Path: w[1:], Access: fspolicy.PathGrantAccessWrite})
			redirectTargets[i] = struct{}{}
		}
	}

	switch strings.ToLower(head) {
	case "cp", "ln", "install":
		paths := absShellPathArgs(args, redirectTargets)
		if len(paths) < 2 {
			return ops, true
		}
		for _, p := range paths[:len(paths)-1] {
			ops = append(ops, PathOperation{Path: p, Access: fspolicy.PathGrantAccessRead})
		}
		ops = append(ops, PathOperation{Path: paths[len(paths)-1], Access: fspolicy.PathGrantAccessWrite})
	case "mv":
		paths := absShellPathArgs(args, redirectTargets)
		if len(paths) < 2 {
			return ops, true
		}
		for _, p := range paths[:len(paths)-1] {
			ops = append(ops, PathOperation{Path: p, Access: fspolicy.PathGrantAccessRead | fspolicy.PathGrantAccessWrite})
		}
		ops = append(ops, PathOperation{Path: paths[len(paths)-1], Access: fspolicy.PathGrantAccessWrite})
	case "dd":
		for _, a := range args {
			if p, cut := strings.CutPrefix(a, "if="); cut && isAbsShellPath(p) {
				ops = append(ops, PathOperation{Path: p, Access: fspolicy.PathGrantAccessRead})
			}
			if p, cut := strings.CutPrefix(a, "of="); cut && isAbsShellPath(p) {
				ops = append(ops, PathOperation{Path: p, Access: fspolicy.PathGrantAccessWrite})
			}
		}
	case "rsync", "scp":
		// Remote endpoints (`user@host:/path`) never look like an absolute
		// local path, so they are invisible here by design (network
		// transfer is D8's concern). A single surviving local path could be
		// either an upload's source or a download's destination — treated
		// as a write, the safe direction (see this function's own doc
		// comment).
		paths := absShellPathArgs(args, redirectTargets)
		switch len(paths) {
		case 0:
			// no-op: nothing local to classify.
		case 1:
			ops = append(ops, PathOperation{Path: paths[0], Access: fspolicy.PathGrantAccessWrite})
		default:
			for _, p := range paths[:len(paths)-1] {
				ops = append(ops, PathOperation{Path: p, Access: fspolicy.PathGrantAccessRead})
			}
			ops = append(ops, PathOperation{Path: paths[len(paths)-1], Access: fspolicy.PathGrantAccessWrite})
		}
	case "sed":
		hasInPlace := false
		for _, a := range args {
			if a == "-i" || strings.HasPrefix(a, "-i") {
				hasInPlace = true
				break
			}
		}
		access := fspolicy.PathGrantAccessRead
		if hasInPlace {
			access = fspolicy.PathGrantAccessWrite
		}
		for _, p := range absShellPathArgs(args, redirectTargets) {
			ops = append(ops, PathOperation{Path: p, Access: access})
		}
	case "rm", "tee", "touch", "mkdir", "chmod", "chown", "truncate":
		for _, p := range absShellPathArgs(args, redirectTargets) {
			ops = append(ops, PathOperation{Path: p, Access: fspolicy.PathGrantAccessWrite})
		}
	case "curl":
		// R4 fix (2026-09-24 security review): `-o`/`--output` name the
		// LOCAL FILE curl writes the response body to — a write, not a
		// read. Before this fix these fell through to the default branch
		// below (a bare absolute-path argument classified as read), so a
		// D7 write escalation never fired for `curl -o /etc/x https://a`.
		ops = append(ops, writeOutputFlagOps(args, redirectTargets,
			map[string]bool{"o": true}, map[string]bool{"output": true})...)
	case "wget":
		// R4 fix: `-O`/`--output-document` are wget's write-target flags,
		// the same shape as curl's `-o`/`--output` above.
		ops = append(ops, writeOutputFlagOps(args, redirectTargets,
			map[string]bool{"O": true}, map[string]bool{"output-document": true})...)
	case "aria2c":
		// R4 fix: `-o`/`--out` names the output FILE, `-d`/`--dir` the
		// output DIRECTORY — both are write targets aria2c creates/
		// overwrites, never something it reads.
		ops = append(ops, writeOutputFlagOps(args, redirectTargets,
			map[string]bool{"o": true, "d": true}, map[string]bool{"out": true, "dir": true})...)
	default:
		for _, p := range absShellPathArgs(args, redirectTargets) {
			ops = append(ops, PathOperation{Path: p, Access: fspolicy.PathGrantAccessRead})
		}
	}

	return ops, true
}

// writeOutputFlagOps extracts WRITE PathOperations for a network-fetch
// command family (curl, wget, aria2c) whose output-destination flag names
// the LOCAL FILE (or directory) the command writes the fetched content to —
// R4's fix (2026-09-24 security review): these were previously invisible to
// every command-specific case above and fell through to the default branch,
// which classifies a bare absolute-path argument as a READ, so a D7 write
// escalation never fired for `curl -o /etc/x https://a`.
//
// shortFlags/longFlags name the flag (without leading dashes) that takes
// the write target as its value — either the NEXT argument word
// (`-o /path`, `--output /path`) or, for a long flag, attached via `=`
// (`--output=/path`). Only absolute-path values are recorded (isAbsShellPath
// — matching every other case in this file, which is absolute-path-only
// throughout).
func writeOutputFlagOps(args []string, redirectTargets map[int]struct{}, shortFlags, longFlags map[string]bool) []PathOperation {
	var ops []PathOperation
	for i := 0; i < len(args); i++ {
		if _, isRedir := redirectTargets[i]; isRedir {
			continue
		}
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--"):
			name, val, hasEq := strings.Cut(a[2:], "=")
			if !longFlags[name] {
				continue
			}
			if hasEq {
				if isAbsShellPath(val) {
					ops = append(ops, PathOperation{Path: val, Access: fspolicy.PathGrantAccessWrite})
				}
				continue
			}
			if i+1 < len(args) && isAbsShellPath(args[i+1]) {
				ops = append(ops, PathOperation{Path: args[i+1], Access: fspolicy.PathGrantAccessWrite})
				i++
			}
		case strings.HasPrefix(a, "-") && len(a) >= 2:
			name := a[1:2]
			if !shortFlags[name] {
				continue
			}
			if len(a) > 2 {
				// Attached value with no separator (`-o/etc/x`).
				if val := a[2:]; isAbsShellPath(val) {
					ops = append(ops, PathOperation{Path: val, Access: fspolicy.PathGrantAccessWrite})
				}
				continue
			}
			if i+1 < len(args) && isAbsShellPath(args[i+1]) {
				ops = append(ops, PathOperation{Path: args[i+1], Access: fspolicy.PathGrantAccessWrite})
				i++
			}
		}
	}
	return ops
}

// tokenizeShellWords splits s on unquoted whitespace, honoring single and
// double quotes (no expansion). Returns ok=false on an unbalanced quote or
// on any $ or ` byte outside single quotes — a command/parameter
// substitution this extractor refuses to guess through.
//
// The ONE exception is a bare, standalone `$?` word (bash's last-exit-status
// special parameter, 2026-09-24 fix) — unquoted, not glued to any other
// character on either side (so `$?` alone is accepted; `/etc/passwd$?`,
// `$?x`, and `"$?"` are NOT — they still disqualify below, same as before).
// That narrow shape matters: this classifier's callers (D7/D8, below) use
// the RETURNED TEXT as the literal path/host they check permissions
// against, so accepting `$?` glued onto a larger token would let a
// classification run against a fake literal string ("/etc/passwd$?") while
// the real shell resolves a DIFFERENT path at run time ("/etc/passwd0",
// once `$?` actually expands) — a check/enforce mismatch this fix must not
// introduce. A bare `$?` word carries no such risk: unlike `$(...)`/
// backtick substitution or a `${...}`/`$VAR`/`$1` variable or
// positional-parameter expansion, its value is always a small integer the
// shell itself just set, never attacker- or file-path-influenced, and by
// itself it can never resolve to a path or a host. Refusing to tokenize it
// made a command as ordinary as `true; echo $?` (exactly the idiom this
// classifier's own D7/D8 callers are asked to run — see
// conformance-design-chat-e2e.spec.ts's t0 goal-claim steer) an unparseable
// FR-020 blind spot, escalating to a live "could not be classified"
// approval card with no operator present to answer it.
func tokenizeShellWords(s string) ([]string, bool) {
	var words []string
	var cur strings.Builder
	inSingle, inDouble, hasWord := false, false, false

	flush := func() {
		if hasWord {
			words = append(words, cur.String())
			cur.Reset()
			hasWord = false
		}
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
			hasWord = true
		case c == '"' && !inSingle:
			inDouble = !inDouble
			hasWord = true
		case c == '\\' && !inSingle && i+1 < len(s):
			cur.WriteByte(s[i+1])
			hasWord = true
			i++
		case c == '$' && !inSingle && !inDouble && !hasWord && i+1 < len(s) && s[i+1] == '?' &&
			(i+2 == len(s) || s[i+2] == ' ' || s[i+2] == '\t'):
			// Bare standalone `$?` — see the doc comment above for exactly
			// why this is the one safe shape to accept.
			words = append(words, "$?")
			i++
		case (c == '$' || c == '`') && !inSingle:
			return nil, false
		case !inSingle && !inDouble && (c == ' ' || c == '\t'):
			flush()
		default:
			cur.WriteByte(c)
			hasWord = true
		}
	}
	if inSingle || inDouble {
		return nil, false
	}
	flush()
	return words, true
}

// resolveShellHead strips leading `VAR=value` assignment tokens (`VAR=1 cmd
// args`) and returns the resolved command word plus its remaining
// arguments. Returns an empty head for an all-assignment or empty word list.
func resolveShellHead(words []string) (string, []string) {
	i := 0
	for i < len(words) && isShellAssignment(words[i]) {
		i++
	}
	if i >= len(words) {
		return "", nil
	}
	return words[i], words[i+1:]
}

// isShellAssignment reports whether w has the shape of a POSIX shell
// variable assignment: NAME=value, NAME starting with a letter or
// underscore and containing only letters, digits, and underscores after
// that.
func isShellAssignment(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	name := w[:eq]
	for j, r := range name {
		switch {
		case j == 0 && (r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')):
		case j > 0 && (r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')):
		default:
			return false
		}
	}
	return true
}

// isAbsShellPath reports whether s looks like an absolute filesystem path
// token — the only shape this classifier reasons about (matching the §5.4
// dataset, which is absolute-path-only throughout).
func isAbsShellPath(s string) bool {
	return strings.HasPrefix(s, "/")
}

// absShellPathArgs returns, in order, every argument in args that is an
// absolute path and not a flag (`-x`), excluding any index already recorded
// as a redirection target (redirectTargets) and any redirection operator
// token itself.
func absShellPathArgs(args []string, redirectTargets map[int]struct{}) []string {
	var out []string
	for i, a := range args {
		if _, isRedir := redirectTargets[i]; isRedir {
			continue
		}
		if a == ">" || a == ">>" || strings.HasPrefix(a, ">") || strings.HasPrefix(a, "-") {
			continue
		}
		if isAbsShellPath(a) {
			out = append(out, a)
		}
	}
	return out
}

// FSPreflightVerdict is D7's per-{path,operation} outcome (FR-009/FR-016).
// It is the pre-flight's own explainable decision (mirroring SEC-17's
// audit-log shape at the evaluator level): exactly one of Refused,
// Contained, or "needs escalation" (neither flag set) is true.
type FSPreflightVerdict struct {
	// Path is the resolved (realpath'd) absolute path this verdict covers —
	// the caller's resolved form, not necessarily the raw command-text
	// token ClassifyPathOperations produced.
	Path string

	// Access is the fspolicy.PathGrantAccessRead|Write bitmask evaluated.
	Access uint64

	// Refused is true when Path falls within the secret set (FR-037): the
	// command must be refused outright — never escalated, never prompted.
	Refused bool

	// Contained is true when WorkDir, AllowedRoots, an existing PathGrant,
	// or the open-reads posture (ADR-063 D2) already covers Access for
	// Path — no escalation needed.
	Contained bool

	// PolicyRule explains which rule produced the verdict.
	PolicyRule string
}

// NeedsEscalation reports whether v requires the D7 Auto escalation prompt:
// neither refused outright nor already contained.
func (v FSPreflightVerdict) NeedsEscalation() bool {
	return !v.Refused && !v.Contained
}

// EvaluateFSPreflight decides, for ONE already-resolved absolute path and
// the access class a command needs there, whether the turn's FSPolicy
// already covers it, the path must be refused outright as part of the
// secret set (FR-037), or an Auto escalation is required.
//
// path must already be realpath'd/resolved by the caller. This function
// performs no filesystem I/O of its own beyond what fspolicy.IsCarveOut and
// fspolicy.CoversForGrant do internally — path resolution itself (symlinks,
// cwd-relative lexical join) is the caller's concern, stated once in
// FR-013's honest-gap note: a pre-flight check cannot see symlink
// resolution at ACCESS time, only at evaluation time.
//
// readConfined mirrors fspolicy.FSPolicy.ReadConfined's own effect
// (ADR-084 JUDGE-FR-060). For an ordinary (non-ReadConfined) agent, reads
// are open outside the secret set (ADR-063 D2) — Scope/WorkDir/AllowedRoots
// govern writes only — so a read-only Access is always Contained without
// even consulting WorkDir/AllowedRoots/PathGrants (S18's corrected
// precondition: a {P, read} widening can never be produced for an ordinary
// agent). For a ReadConfined agent, both read and write need containment.
func EvaluateFSPreflight(policy fspolicy.FSPolicy, path string, access uint64, readConfined bool) FSPreflightVerdict {
	clean := filepath.Clean(path)

	if fspolicy.IsCarveOut(clean, policy) {
		return FSPreflightVerdict{
			Path: clean, Access: access, Refused: true,
			PolicyRule: "path is within the secret set (CarveOuts) — refused outright, never escalated (FR-037)",
		}
	}

	needsWrite := access&fspolicy.PathGrantAccessWrite != 0
	needsOnlyRead := access&fspolicy.PathGrantAccessRead != 0 && !needsWrite

	if needsOnlyRead && !readConfined {
		return FSPreflightVerdict{
			Path: clean, Access: access, Contained: true,
			PolicyRule: "read access outside the secret set is open for this agent (ADR-063 D2) — no grant needed",
		}
	}

	if fsPreflightContained(policy, clean, access) {
		return FSPreflightVerdict{
			Path: clean, Access: access, Contained: true,
			PolicyRule: "path is within WorkDir, an AllowedRoots write grant, or an existing PathGrant covering this access",
		}
	}

	return FSPreflightVerdict{
		Path: clean, Access: access,
		PolicyRule: "path is outside WorkDir/AllowedRoots/any existing PathGrant for this access — Auto escalation required (FR-009)",
	}
}

// fsPreflightContained checks clean against WorkDir, AllowedRoots, and
// PathGrants, in that order — the same three fields (and the same priority)
// sandbox.DeriveKernelPolicy renders into the kernel policy, which is what
// the Level 1 anti-drift lock test (pkg/sandbox/*_antidrift_test.go)
// exists to keep true: it does not compare this function against
// guardCommand's own independent containment scan (shell_path_guard.go,
// L4-owned), it compares THIS function's verdict against
// DeriveKernelPolicy's actual rendering.
func fsPreflightContained(policy fspolicy.FSPolicy, clean string, access uint64) bool {
	if policy.WorkDir != "" && fspolicy.CoversForGrant(filepath.Clean(policy.WorkDir), clean) {
		// WorkDir is rendered as a full RWX PathRule (DefaultPolicyForModel),
		// so it satisfies any access class this evaluator ever asks about.
		return true
	}

	// AllowedRoots are WRITE grants only (fspolicy.FSPolicy's own doc
	// comment on the field) — they satisfy a write-only need and never
	// stand in for a read requirement a ReadConfined agent has.
	writeOnly := access != 0 && access&^fspolicy.PathGrantAccessWrite == 0
	if writeOnly {
		for _, root := range policy.AllowedRoots {
			if fspolicy.CoversForGrant(filepath.Clean(root), clean) {
				return true
			}
		}
	}

	for _, g := range policy.PathGrants {
		if filepath.Clean(g.Path) == clean && g.Access&access == access {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------
// D8 — network pre-flight
// ---------------------------------------------------------------------

// networkCapableBinaries is FR-043's curated, operator-extendable default
// set of network-capable binaries the classifier flags on sight. An
// operator extension point lives in the grant-store/config wiring a
// different lane owns (L4); this is the classifier's fixed default.
var networkCapableBinaries = map[string]struct{}{
	"git": {}, "curl": {}, "wget": {}, "ssh": {}, "scp": {}, "rsync": {},
	"npm": {}, "pnpm": {}, "yarn": {}, "pip": {}, "pip3": {},
	"apt": {}, "apt-get": {}, "yum": {}, "dnf": {},
	"docker": {}, "gh": {},
	"aws": {}, "az": {}, "gcloud": {}, "kubectl": {},
}

// ClassifyNetworkNeed is the FR-043 network-need classifier: it flags a
// command via (a) a resolved head in networkCapableBinaries for any segment
// (reusing the same per-segment split ClassifyPathOperations uses), or (b)
// a literal http:// or https:// token anywhere in the command text.
//
// An unparseable segment (unbalanced quote, command/parameter substitution)
// is treated as FLAGGED rather than silently passed through — the same
// fail-closed "blind spot" posture FR-020 states for the rule matcher,
// applied here to the network question instead of the filesystem one.
func ClassifyNetworkNeed(command string) bool {
	if strings.Contains(command, "http://") || strings.Contains(command, "https://") {
		return true
	}
	for _, seg := range splitShellSegments(command) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		words, ok := tokenizeShellWords(seg)
		if !ok {
			return true
		}
		head, _ := resolveShellHead(words)
		if _, known := networkCapableBinaries[strings.ToLower(head)]; known {
			return true
		}
	}
	return false
}

// urlHostRe extracts the host portion of a literal http(s):// token —
// ExtractNetworkHosts's primary source. Stops at the first byte that ends a
// hostname in a URL (`/`, `:`, `?`, `#`, whitespace, or a quote/backtick a
// shell word could still be carrying) without needing a full URL parse,
// matching ClassifyNetworkNeed's own "literal http(s):// token anywhere in
// the command text" scan (it does not require the URL to be a clean,
// standalone shell word either).
var urlHostRe = regexp.MustCompile(`(?i)https?://([a-z0-9](?:[a-z0-9.-]*[a-z0-9])?)(?:[:/?#'"` + "`" + `]|\s|$)`)

// ExtractNetworkHosts is the D-13 fix's (2026-09-24 security review)
// best-effort hostname extractor: founder decision B point 1, "The D8
// classifier extracts the hosts the command names (from URLs, or host:port
// args of known network binaries) and puts them on the approval card."
//
// Two sources, deliberately independent of ClassifyNetworkNeed's own
// networkCapableBinaries membership test (a host can be extracted from a
// binary ClassifyNetworkNeed did not flag by name, e.g. a plain
// `some-tool https://example.com`, and equally a flagged binary may carry
// no extractable host at all, e.g. `npm install` — see BlindHostNeed):
//
//  1. every literal `http://`/`https://` URL's host (urlHostRe);
//  2. for ssh/scp/rsync specifically, the remote side of a
//     `[user@]host[:path]` argument (hostFromRemoteSpec) — the one shape
//     those three commands use to name a network endpoint that is NOT a
//     URL at all.
//
// Returns hosts lower-cased and de-duplicated, in first-seen order. This is
// explicitly NOT exhaustive (see BlindHostNeed's own doc comment and the
// ADR-092 D8 "honest gap" section) — a host built at runtime (variable
// expansion, a config file, DNS discovered by the program itself) is
// invisible to a text scan by construction.
func ExtractNetworkHosts(command string) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(h string) {
		h = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, h)
	}

	for _, m := range urlHostRe.FindAllStringSubmatch(command, -1) {
		add(m[1])
	}

	for _, seg := range splitShellSegments(command) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		words, ok := tokenizeShellWords(seg)
		if !ok {
			continue
		}
		head, args := resolveShellHead(words)
		switch strings.ToLower(head) {
		case "ssh", "scp", "rsync":
			for _, a := range args {
				if h, ok := hostFromRemoteSpec(a); ok {
					add(h)
				}
			}
		}
	}
	return out
}

// hostFromRemoteSpec extracts the hostname from an ssh/scp/rsync remote
// endpoint argument: `[user@]host[:path]`. Returns ok=false for a flag
// (`-p`), a bare local path (no `@`, and starting with `/`, `.`, or `~`),
// or an empty result — the caller must not guess in any of those cases.
func hostFromRemoteSpec(spec string) (string, bool) {
	if spec == "" || strings.HasPrefix(spec, "-") {
		return "", false
	}
	s := spec
	if at := strings.IndexByte(s, '@'); at >= 0 {
		s = s[at+1:]
	} else if strings.HasPrefix(s, "/") || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "~") {
		// No "@" and shaped like a local path (scp/rsync's own source or
		// destination argument, not a remote endpoint) — not a host.
		return "", false
	}
	if colon := strings.IndexByte(s, ':'); colon >= 0 {
		s = s[:colon]
	}
	if s == "" || strings.ContainsAny(s, "/\\") {
		return "", false
	}
	return s, true
}

// allHostsApproved reports whether every host in needed already appears in
// approved (case-insensitive; both are expected pre-lowercased by this
// file's own callers, but the comparison does not assume it). An empty
// needed slice is never "approved" — the caller (EvaluateNetworkPreflight)
// only calls this when hosts were actually extracted.
func allHostsApproved(needed, approved []string) bool {
	if len(needed) == 0 {
		return false
	}
	set := make(map[string]bool, len(approved))
	for _, h := range approved {
		set[strings.ToLower(h)] = true
	}
	for _, h := range needed {
		if !set[strings.ToLower(h)] {
			return false
		}
	}
	return true
}

// NetworkPreflightVerdict is D8's per-command outcome (FR-042/FR-043/FR-044).
type NetworkPreflightVerdict struct {
	// Flagged is true when ClassifyNetworkNeed flagged the command.
	Flagged bool

	// Contained is true when no escalation is needed: either the command
	// was not flagged (Auto's kernel-level deny-by-default is not
	// challenged — S52's honest gap covers what happens if the classifier
	// missed a real network need), or it was flagged and a network grant
	// already covers this session (FR-044).
	Contained bool

	// PolicyRule explains the verdict.
	PolicyRule string

	// Hosts is the D-13 fix's (2026-09-24 security review) best-effort
	// extraction of the literal hostnames this command names — from a
	// literal http(s):// URL, or the remote side of an ssh/scp/rsync
	// `[user@]host[:path]` argument (ExtractNetworkHosts). Empty when
	// Flagged is true but no host could be extracted (a "blind" network
	// need — the command was recognised as network-capable by binary name
	// alone, e.g. `npm install`, with no literal host anywhere in its
	// text) — see BlindHostNeed.
	Hosts []string
}

// BlindHostNeed reports whether v is a flagged, escalation-needing command
// with NO extractable host at all — founder decision B, point 3: "If no
// host can be extracted... the card must say so, and approval must not
// silently open everything. Keep today's behaviour (ports only) and state
// it on the card." A blind-host approval therefore widens ONLY the kernel
// port rule (applyAutoNetworkPosture, unchanged) — never the egress
// proxy's host allow-list, since there is no host to grant there.
func (v NetworkPreflightVerdict) BlindHostNeed() bool {
	return v.NeedsEscalation() && len(v.Hosts) == 0
}

// NeedsEscalation reports whether v requires the D8 Auto escalation prompt.
func (v NetworkPreflightVerdict) NeedsEscalation() bool {
	return v.Flagged && !v.Contained
}

// EvaluateNetworkPreflight decides whether a bash command needs the D8
// escalation prompt before it spawns. granted is the caller's own record of
// whether this session already holds the network-widening (port-level)
// grant (FR-044); approvedHosts is the session's own D-13 host set
// (security.ApprovalGrantStore.NetworkHostsFor) — this evaluator does not
// read ApprovalGrantStore itself; that store is owned by a different lane
// (pkg/security/approvalgrants.go, L4).
//
// D-13 fix (2026-09-24 security review): before this fix, `granted` alone
// decided Contained — a session that had EVER approved network access ran
// every later network-capable command silently, regardless of which host it
// named. Now, when ExtractNetworkHosts finds at least one host, Contained
// additionally requires every one of THIS command's hosts to already be in
// approvedHosts — a session approved for example.com does not silently run
// a later command against other.test. A BLIND network need (Hosts empty —
// see BlindHostNeed) keeps the ORIGINAL ports-only behaviour unchanged
// (founder decision B, point 3: "Keep today's behaviour (ports only)").
func EvaluateNetworkPreflight(command string, granted bool, approvedHosts []string) NetworkPreflightVerdict {
	if !ClassifyNetworkNeed(command) {
		return NetworkPreflightVerdict{
			Contained:  true,
			PolicyRule: "command not classified as network-capable — bash's Auto per-turn deny-by-default stands unchallenged (an unclassified raw socket is still kernel-denied, FR-044's honest gap)",
		}
	}
	hosts := ExtractNetworkHosts(command)
	if len(hosts) == 0 {
		if granted {
			return NetworkPreflightVerdict{
				Flagged: true, Contained: true,
				PolicyRule: "command classified as network-capable with no extractable host (blind network need), and a network grant is already recorded for this session (FR-044) — no re-prompt, ports only",
			}
		}
		return NetworkPreflightVerdict{
			Flagged:    true,
			PolicyRule: "command classified as network-capable with no extractable host (blind network need) and no network grant recorded — Auto escalation required (FR-042/FR-043), ports only",
		}
	}
	if granted && allHostsApproved(hosts, approvedHosts) {
		return NetworkPreflightVerdict{
			Flagged: true, Contained: true, Hosts: hosts,
			PolicyRule: "command classified as network-capable, and every named host is already approved for this session (FR-044/D-13) — no re-prompt",
		}
	}
	return NetworkPreflightVerdict{
		Flagged: true, Hosts: hosts,
		PolicyRule: "command classified as network-capable and names a host not yet approved for this session — Auto escalation required (FR-042/FR-043/D-13)",
	}
}
