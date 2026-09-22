// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file implements ADR-091's Auto-mode pre-flight: the filesystem
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
// ADR-091's delivery plan (spec §8.2) splits ownership: this file (L3) owns
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
// come first (cp/mv/dd/rm/tee each have a well-known argument shape);
// everything else falls through to the default rule: a redirection target
// (`>`, `>>`) is always a write, and any other bare absolute-path argument
// to an unrecognized command is treated as a read (the conservative default
// — see EvaluateFSPreflight's own note that a read-only verdict is a no-op
// for every non-ReadConfined agent, so over-classifying a word as "read"
// never produces a spurious escalation).
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
	case "cp":
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
	case "rm", "tee":
		for _, p := range absShellPathArgs(args, redirectTargets) {
			ops = append(ops, PathOperation{Path: p, Access: fspolicy.PathGrantAccessWrite})
		}
	default:
		for _, p := range absShellPathArgs(args, redirectTargets) {
			ops = append(ops, PathOperation{Path: p, Access: fspolicy.PathGrantAccessRead})
		}
	}

	return ops, true
}

// tokenizeShellWords splits s on unquoted whitespace, honoring single and
// double quotes (no expansion). Returns ok=false on an unbalanced quote or
// on any $ or ` byte outside single quotes — a command/parameter
// substitution this extractor refuses to guess through.
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
}

// NeedsEscalation reports whether v requires the D8 Auto escalation prompt.
func (v NetworkPreflightVerdict) NeedsEscalation() bool {
	return v.Flagged && !v.Contained
}

// EvaluateNetworkPreflight decides whether a bash command needs the D8
// escalation prompt before it spawns. granted is the caller's own record of
// whether this session already holds the network-widening grant (FR-044) —
// this evaluator does not read ApprovalGrantStore itself; that store is
// owned by a different lane (pkg/security/approvalgrants.go, L4).
func EvaluateNetworkPreflight(command string, granted bool) NetworkPreflightVerdict {
	flagged := ClassifyNetworkNeed(command)
	if !flagged {
		return NetworkPreflightVerdict{
			Contained:  true,
			PolicyRule: "command not classified as network-capable — bash's Auto per-turn deny-by-default stands unchallenged (an unclassified raw socket is still kernel-denied, FR-044's honest gap)",
		}
	}
	if granted {
		return NetworkPreflightVerdict{
			Flagged: true, Contained: true,
			PolicyRule: "command classified as network-capable, and a network grant is already recorded for this session (FR-044) — no re-prompt",
		}
	}
	return NetworkPreflightVerdict{
		Flagged:    true,
		PolicyRule: "command classified as network-capable and no network grant recorded — Auto escalation required (FR-042/FR-043)",
	}
}
