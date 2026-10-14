// Package fspolicy computes the single, authoritative filesystem-access
// policy for an agent turn (ADR-046, unified filesystem/workspace model,
// FR-036). EffectiveFSPolicy is the one function of record consumed by both
// the app-layer path resolver (ResolvePath, P1/P2) and the future per-child
// Landlock ruleset builder (P3) — so the two enforcement backends can never
// drift apart.
//
// This package is intentionally a leaf: stdlib only (no internal imports).
// It must NOT import pkg/config or pkg/tools — pkg/tools already imports
// pkg/sandbox, and P3 wires pkg/sandbox -> fspolicy, so importing either
// here would risk an import cycle; every $OMNIPUS_HOME-shaped value this
// package needs is passed in by the caller instead.
package fspolicy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FSScope is the resolved filesystem-access posture for a turn.
//
// P1 only ever produces FSScopeConfined or FSScopeUnrestricted (mapped from
// today's restrict=true/restrict=false). FSScopeAsk and FSScopeAllow are
// RESERVED for the P2 filesystem_scope (allow/ask/deny) rollout so this type
// is already exhaustive for the switches P2/P3 will add. No P1 code path
// ever returns FSScopeAsk or FSScopeAllow — there is no invented default
// here (Constraint #6).
type FSScope string

const (
	// FSScopeConfined is the P1 equivalent of today's restrict=true: the
	// agent is confined to its effective working directory.
	FSScopeConfined FSScope = "confined"

	// FSScopeUnrestricted is the P1 equivalent of today's restrict=false:
	// host-filesystem access, preserving pre-ADR-046 back-compat behavior.
	FSScopeUnrestricted FSScope = "unrestricted"

	// FSScopeAsk is RESERVED for P2's filesystem_scope=ask. No P1 code path
	// returns this value.
	FSScopeAsk FSScope = "ask"

	// FSScopeAllow is RESERVED for P2's filesystem_scope=allow. No P1 code
	// path returns this value.
	FSScopeAllow FSScope = "allow"
)

// PathGrant access bits (ADR-092 FR-036). These MUST stay bit-for-bit
// identical to sandbox.AccessRead/AccessWrite/AccessExecute
// (pkg/sandbox/sandbox.go) — the same vocabulary reused, not a new one, per
// FR-036's own text. They are duplicated as untyped constants here, rather
// than imported, because this package is a stdlib-only leaf (see the package
// comment: it must not import pkg/sandbox, and pkg/sandbox already imports
// this package, so the reverse import would cycle).
// pkg/sandbox/pathgrant_bits_test.go pins the equivalence with a same-value
// assertion so the two constant sets cannot drift apart unnoticed.
const (
	PathGrantAccessRead    uint64 = 1 << 0
	PathGrantAccessWrite   uint64 = 1 << 1
	PathGrantAccessExecute uint64 = 1 << 2
)

// PathGrant is one bash-scoped filesystem widening approved through the D7
// pre-flight escalation (ADR-092 FR-016/FR-036): EXACTLY one path and
// EXACTLY the access class the pre-flight verdict found missing — never a
// subtree grant the way AllowedRoots is.
type PathGrant struct {
	// Path is the resolved (realpath'd, symlinks followed), absolute path
	// this grant covers.
	Path string

	// Access is the PathGrantAccessRead|PathGrantAccessWrite|
	// PathGrantAccessExecute bitmask this grant adds.
	Access uint64
}

// AllowRoot is one explicit allowance inside $OMNIPUS_HOME (amendment §2 D3,
// chief ruling 2026-10-13): a path the default-deny home guard re-admits, and
// whether that admission carries write. A Writable=false entry is read (and
// execute) only — a write through a read-only entry is refused. An entry can
// never reopen a protected-inventory path: the inventory is evaluated first
// and always wins.
type AllowRoot struct {
	// Path is the resolved absolute path this allowance covers (a subtree).
	Path string

	// Writable grants write on top of read when true; read-only when false.
	Writable bool
}

// HomeGuard carries the default-deny protection of $OMNIPUS_HOME
// (amendment §2 D1/D5): the home anchor and the explicit allowances inside
// it. It lives on FSPolicy so the ONE predicate (IsCarveOut) — and through it
// ResolvePath, grep, the bash command check and the D7 pre-flight — inherits
// the protection, with no second construction site.
//
// A zero HomeGuard (Home == "") means "no home guard in force": every policy
// built before this field existed, and every hand-constructed test policy,
// keeps exactly its pre-#1284 behaviour. EffectiveFSPolicy always populates
// it.
type HomeGuard struct {
	// Home is the resolved (realpath'd) $OMNIPUS_HOME this guard denies by
	// default. Empty disables the guard.
	Home string

	// Allow are the explicit allowances inside Home (amendment §2 D3):
	// base entries (media, skills helper files) from BaseHomeGuard, the
	// turn's session-scoped upload dirs, and workspace mounts. Inventory
	// paths are never re-admitted by any entry.
	Allow []AllowRoot
}

// FSPolicy is the effective, fully-resolved filesystem policy for one agent
// turn — the single source of record both the app-layer resolver and the
// future kernel ruleset builder consume (FR-036).
type FSPolicy struct {
	// WorkDir is the realpath (symlinks resolved) of the effective working
	// directory for the turn.
	WorkDir string

	// Scope is the resolved access posture for the turn.
	Scope FSScope

	// CarveOuts are the fixed $OMNIPUS_HOME-anchored roots that are always
	// denied regardless of Scope: master.key, credentials.json, the whole
	// agents/ subtree, and the whole workspaces/ subtree (FR-017).
	CarveOuts []string

	// AllowedRoots are additional roots the turn may WRITE to, beyond WorkDir.
	//
	// ADR-063 FR-6.1: a workspace's mounts land here — populated by
	// tools.ResolveTurnFSPolicy from workspace.AllowedMountRoots, and rendered
	// into the kernel policy as write grants by sandbox.DeriveKernelPolicy. It
	// is nil only for a turn with no mounts.
	//
	// (This field was nil for every caller before mounts existed, and the
	// comment here said so. It is now load-bearing: code that treats it as
	// permanently empty — for instance by skipping a containment check
	// "because it can't match anything" — silently drops a real grant.)
	//
	// A mount grants WRITE and nothing else, because post-ADR-062 reads need
	// no grant at all. It cannot reopen a secret: IsCarveOut and DeniedPathsFor
	// take no AllowedRoots parameter, so mounting even $HOME yields "write to
	// $HOME minus the secret set" (asserted in mount_secret_independence_test.go).
	AllowedRoots []string

	// PathGrants are bash-scoped, single-path widenings approved through the
	// D7 pre-flight escalation flow (ADR-092 FR-016/FR-036,
	// pkg/tools/preflight.go's filesystem evaluator). Unlike AllowedRoots (a
	// subtree WRITE grant populated from workspace mounts), a PathGrant is
	// exactly one path and exactly the access class the pre-flight verdict
	// found missing — read, write, or both — and sandbox.DeriveKernelPolicy
	// renders each one as one additional PathRule, additive to the
	// WorkDir/AllowedRoots rendering (never a parallel derivation).
	//
	// Tool scope (FR-036): this field is populated ONLY by the bash tool's
	// own ResolveTurnFSPolicy call site, from an optional grant-overlay
	// parameter that call site alone supplies. It is nil for every other of
	// the eleven ResolveTurnFSPolicy callers (edit.go, filesystem.go,
	// grep.go, send_file.go, web_serve.go, request_mount.go, the browser
	// tools) — a bash-approved widening never reaches them. This package has
	// no caller visibility to enforce that scoping itself (stdlib-only leaf,
	// package comment); it is enforced entirely by which call sites choose
	// to populate this field.
	//
	// Never a secret-set reopener (FR-037): Validate refuses a policy whose
	// PathGrants names a path IsCarveOut already denies, so a malformed or
	// buggy grant can never reach a kernel-rendering step at all, not merely
	// be refused by the pre-flight's own (separate) decision not to prompt
	// for one. See Validate's PathGrants loop.
	PathGrants []PathGrant

	// SetupRoots are workspace WORK directories (workspaces/<id>/work) this
	// turn may reach although they sit under the workspaces/ carve-out root:
	// the cross-workspace setup authority only Omnipus holds
	// (omnipus-system-agent FR-006, L1 amendment §2.2 A8). Each one is exempt
	// from the carve-out exactly the way the turn's own WorkDir is — only the
	// path inside that work directory, never the workspace record
	// (workspaces/<id>.json), its .omnipus room, or anything else under the
	// root. They are also write grants (WithSetupRoots adds them to
	// AllowedRoots), so writes there go through the same os.Root-anchored
	// mount handle.
	//
	// App layer only, for now: the kernel derivation (KernelDeniedPathsFor,
	// DeriveKernelPolicy) does not read this field, so a bash child of the
	// same turn is still denied these directories — the safe direction. The
	// kernel half arrives with the #1284 allow-list (WU-23).
	//
	// nil for every turn of every other agent.
	SetupRoots []string

	// ReadConfined extends WorkDir confinement to the three operations that
	// ADR-063 FR-2.2 deliberately left open outside the secret set —
	// FSOpRead, FSOpList and FSOpSend (ADR-084 JUDGE-FR-060).
	//
	// Threat model. ADR-063 made reads open because a read is not a
	// modification and the secret set (master.key, credentials.json,
	// agents/, workspaces/) already covered what mattered. The Judge breaks
	// that assumption: $OMNIPUS_HOME/sessions/, tasks/, plans/ and memory/
	// are in NEITHER SecretEntriesAlways nor SecretEntriesPerTurn, so an
	// unconfined verifier turn can read every transcript in the install —
	// which makes the verifier's own target-session lock
	// (tools.VerifierSessionScopeAllows) bypassable by opening the transcript
	// file directly instead of calling inspect_session.
	//
	// It governs ALL THREE operations in that branch on purpose. Narrowing it
	// to FSOpRead was considered and rejected: the Judge is granted
	// list_directory, so a read-only flag would leave
	// `list_directory $OMNIPUS_HOME/sessions/` wide open and close only half
	// the hole. FSOpSend is the third because send_file resolves through the
	// same branch.
	//
	// A posture flag, not an enumeration. The rejected alternative was a
	// carve-out list naming sessions/, tasks/, plans/, memory/,
	// tasks_evidence/ and logs/. An enumeration of what to hide drifts the
	// first time a new state directory is added; a posture does not.
	//
	// POLARITY, stated so it cannot be inverted by accident: the zero value
	// means NOT confined. Every policy built without an explicit
	// readConfined=true is byte-identical to the pre-ADR-084 policy, so every
	// non-System agent's FSOpRead/FSOpList/FSOpSend reach is exactly as
	// ADR-063 FR-2.2 left it — open outside the secret set, independent of
	// Scope. That is asserted by
	// TestEffectiveFSPolicy_ReadConfinedOnlyForSystemAgents.
	//
	// It is orthogonal to Scope. Scope still governs FSOpWrite/FSOpServe/
	// FSOpExec exactly as before; ReadConfined adds nothing to, and removes
	// nothing from, those three. It is also strictly additive to the
	// carve-out check, which runs unconditionally and earlier — a confined
	// policy can never make a previously-denied path reachable.
	ReadConfined bool

	// HomeGuard is the #1284 default-deny protection of $OMNIPUS_HOME
	// (amendment §2 D1/D5): the named protected inventory always wins, every
	// other path under Home is denied unless an Allow entry or the turn's
	// WorkDir re-admits it. Evaluated by IsCarveOut BEFORE the CarveOuts
	// loop, so the app-layer resolver, grep, the bash command check and the
	// D7 pre-flight all inherit it through that one predicate. Zero value
	// (Home == "") disables the leg — the documented pre-#1284 behaviour for
	// hand-constructed policies.
	HomeGuard HomeGuard
}

// Validate asserts the structural invariants ResolvePath depends on before
// consulting p for any access decision (BLOCK #1, ADR-046 P1 review — a
// zero-value or otherwise malformed FSPolicy must never reach a decision
// point silently).
//
// It is exported (rather than the lowercase "validate" a first pass at this
// review named) because the sole caller, tools.ResolvePath, lives in a
// different package — an unexported method cannot be called across a
// package boundary, so exporting it is required for the call to compile at
// all, not a scope expansion.
//
// Checks, in order:
//
//  1. WorkDir is non-empty (after TrimSpace) and absolute. A zero-value
//     FSPolicy{} (WorkDir=="") fails here.
//  2. WorkDir must never be at or above ANY of p.CarveOuts' roots — i.e. no
//     carve-out root may be within-or-equal to WorkDir. Every carve-out root
//     buildCarveOuts produces is a direct child of $OMNIPUS_HOME
//     (master.key, credentials.json, agents/, workspaces/), so a WorkDir
//     that contains one of them (WorkDir == $OMNIPUS_HOME, or an ancestor of
//     it) would put every carve-out inside IsCarveOut's own-tree exception,
//     silently disabling FR-017 (BLOCK #2 in this same review). A P1
//     policy's WorkDir is always a proper descendant of exactly one carve-
//     out root (agents/<id> or workspaces/<id>/work) — this check permits
//     that shape, and also permits P2's future "external dir" WorkDir (one
//     with no carve-out-root ancestry relationship at all — vacuously true
//     when p.CarveOuts is empty, e.g. the direct-construction shape several
//     resolver-level unit tests use).
//  3. Scope is one of the four known FSScope constants — never a stray or
//     typo'd value silently treated as some implicit default.
func (p FSPolicy) Validate() error {
	workDir := strings.TrimSpace(p.WorkDir)
	if workDir == "" {
		return fmt.Errorf("fspolicy: WorkDir is empty")
	}
	if !filepath.IsAbs(p.WorkDir) {
		return fmt.Errorf("fspolicy: WorkDir %q is not absolute", p.WorkDir)
	}

	// CoversForDeny, not a byte comparison: this check REFUSES a policy, so
	// its fail-safe direction is to match more, and a WorkDir spelled
	// "$OMNIPUS_HOME" in a different case than the carve-out roots is the same
	// directory on a case-insensitive volume (pathidentity.go's header).
	// Byte-comparing here would let exactly the malformed shape this guard
	// exists to reject slip through under an alternate spelling.
	cleanWorkDir := filepath.Clean(p.WorkDir)
	for _, root := range p.CarveOuts {
		cleanRoot := filepath.Clean(root)
		if CoversForDeny(cleanWorkDir, cleanRoot) {
			return fmt.Errorf(
				"fspolicy: WorkDir %q is at or above the carve-out root %q — this would defeat FR-017's carve-out protection",
				p.WorkDir,
				root,
			)
		}
	}

	switch p.Scope {
	case FSScopeConfined, FSScopeUnrestricted, FSScopeAsk, FSScopeAllow:
	default:
		return fmt.Errorf("fspolicy: unknown Scope %q", p.Scope)
	}

	// SetupRoots obey the same shape rule as WorkDir: absolute, and never at
	// or above a carve-out root (a setup root at $OMNIPUS_HOME would exempt
	// every carve-out at once). On top of that, each one must be exactly a
	// workspace WORK directory (FR-006, L1 amendment §2.2 A8, CHECK F05) —
	// never a whole workspace, its .omnipus room or any other entry under the
	// workspaces/ carve-out root.
	for _, root := range p.SetupRoots {
		if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
			return fmt.Errorf("fspolicy: SetupRoots entry %q is not an absolute path", root)
		}
		cleanSetup := filepath.Clean(root)
		for _, carve := range p.CarveOuts {
			if CoversForDeny(cleanSetup, filepath.Clean(carve)) {
				return fmt.Errorf("fspolicy: SetupRoots entry %q is at or above the carve-out root %q", root, carve)
			}
		}
		if !isWorkspaceWorkDir(cleanSetup, p.CarveOuts) {
			return fmt.Errorf("fspolicy: SetupRoots entry %q is not a workspace work directory (%s/<id>/%s)",
				root, setupWorkspacesEntry, setupWorkDirName)
		}
	}

	// PathGrants (ADR-092 FR-036/FR-037): each grant must be an absolute
	// path, and none may name a path IsCarveOut denies — the secret set is
	// never widenable, asserted here as a structural invariant rather than
	// left to each consumer (the app-layer guard, the pre-flight evaluator,
	// DeriveKernelPolicy) to independently filter the same case correctly.
	// A policy shaped this way is refused before it can reach any of them.
	for _, g := range p.PathGrants {
		grantPath := strings.TrimSpace(g.Path)
		if grantPath == "" {
			return fmt.Errorf("fspolicy: PathGrants entry has an empty Path")
		}
		if !filepath.IsAbs(g.Path) {
			return fmt.Errorf("fspolicy: PathGrants entry %q is not absolute", g.Path)
		}
		if g.Access == 0 {
			return fmt.Errorf("fspolicy: PathGrants entry %q has no Access bits set", g.Path)
		}
		cleanGrant := filepath.Clean(g.Path)
		if IsCarveOut(cleanGrant, p) {
			return fmt.Errorf(
				"fspolicy: PathGrants entry %q falls within the secret set — the secret set can never be widened (FR-037)",
				g.Path,
			)
		}
	}

	return nil
}

// EffectiveFSPolicy computes the single, authoritative FSPolicy for a turn
// (FR-036).
//
// WorkDir is the realpath of turnWorkDir when non-empty, else the realpath
// of agentHome. Scope maps from restrict (true -> FSScopeConfined, false ->
// FSScopeUnrestricted); P1 never produces FSScopeAsk or FSScopeAllow.
// CarveOuts are always the four omnipusHome-anchored roots built by
// buildCarveOuts.
//
// AllowedRoots is left nil HERE and populated by the caller: this package is a
// stdlib-only leaf (see the package comment) and cannot import pkg/workspace to
// read a workspace's mounts. tools.ResolveTurnFSPolicy fills it in immediately
// afterwards, and that is the shape every production caller sees — so a nil
// AllowedRoots on a returned policy means "this turn has no mounts", not "this
// field is unused".
//
// agentID and workspaceID are accepted but UNUSED in P1's computation — they
// keep the signature stable for the P2 per-agent/per-workspace scope seam
// (filesystem_scope resolution, most-restrictive-wins).
//
// Fails closed: returns a non-nil error (and the zero FSPolicy) whenever
// omnipusHome or the effective working directory cannot be resolved to an
// absolute, realpath'd location, so a caller can never fall through to a
// half-computed policy.
//
// ReadConfined is always false on the policy this returns. A caller that
// needs the ADR-084 JUDGE-FR-060 read-confined posture calls
// EffectiveFSPolicyWithReadConfined instead and passes the flag explicitly —
// see that function's doc comment for why the flag is a parameter rather
// than something read off ctx.
func EffectiveFSPolicy(
	ctx context.Context,
	agentHome, turnWorkDir string,
	restrict bool,
	omnipusHome, agentID, workspaceID string,
) (FSPolicy, error) {
	return EffectiveFSPolicyWithReadConfined(
		ctx, agentHome, turnWorkDir, restrict, omnipusHome, agentID, workspaceID, false,
	)
}

// EffectiveFSPolicyWithReadConfined is EffectiveFSPolicy plus the ADR-084
// JUDGE-FR-060 read-confinement posture, supplied as an EXPLICIT parameter.
//
// Why a parameter and not ctx. This function already accepts a ctx and
// already throws it away (see below). Reading the posture off that ctx would
// make a security-relevant decision depend on a value this package cannot
// see being set, cannot type-check, and cannot fail loudly about when it is
// missing — the exact "silently unconfined" failure JUDGE-FR-060b forbids.
// An explicit bool forces the decision to be made, in code, at the call
// site: tools.ResolveTurnFSPolicy reads the engine-set turn fact
// (tools.ReadConfined) and hands the answer down.
//
// Why a second function rather than an eighth parameter on EffectiveFSPolicy.
// JUDGE-FR-060b asks for a signature change so every existing call site is
// forced to consider the flag. Every one of those call sites lives in a file
// owned by another wave of this delivery (pkg/tools/shell.go and eight test
// files), which this wave may not touch. The polarity requirement — unset
// means NOT confined — makes the delegating wrapper above exactly equivalent
// for them, and this file states, in one place, that a caller which does not
// name the posture does not get it. Recorded as a deliberate deviation in
// this wave's report.
func EffectiveFSPolicyWithReadConfined(
	ctx context.Context,
	agentHome, turnWorkDir string,
	restrict bool,
	omnipusHome, agentID, workspaceID string,
	readConfined bool,
) (FSPolicy, error) {
	// P2 seam: agentID/workspaceID will drive per-agent/per-workspace
	// filesystem_scope resolution once that lands. Referenced here only to
	// make the current no-op deliberate rather than silently unused.
	_ = agentID
	_ = workspaceID
	_ = ctx // reserved for future ask-flow/audit context threading (P2)

	resolvedHome, err := realpath(omnipusHome)
	if err != nil {
		return FSPolicy{}, fmt.Errorf("fspolicy: resolve omnipus home %q: %w", omnipusHome, err)
	}

	effectiveWorkDir := turnWorkDir
	if effectiveWorkDir == "" {
		effectiveWorkDir = agentHome
	}
	// BLOCK #1 (ADR-046 P1 review): filepath.Abs("") resolves to the
	// process's own current working directory rather than erroring, so
	// without this explicit check an agent/turn with no home AND no
	// TurnWorkspaceDir would silently fail OPEN — confining the "turn" to
	// wherever the omnipus process itself happens to be running from,
	// rather than refusing to produce a policy at all. Fail closed instead:
	// no working directory is a misconfiguration, never a valid policy.
	if strings.TrimSpace(effectiveWorkDir) == "" {
		return FSPolicy{}, fmt.Errorf("fspolicy: no working directory (agentHome and turnWorkDir both empty)")
	}
	resolvedWorkDir, err := realpath(effectiveWorkDir)
	if err != nil {
		return FSPolicy{}, fmt.Errorf("fspolicy: resolve working dir %q: %w", effectiveWorkDir, err)
	}

	scope := FSScopeUnrestricted
	if restrict {
		scope = FSScopeConfined
	}

	return FSPolicy{
		WorkDir:      resolvedWorkDir,
		Scope:        scope,
		CarveOuts:    buildCarveOuts(resolvedHome),
		AllowedRoots: nil,
		ReadConfined: readConfined,
		HomeGuard:    BaseHomeGuard(resolvedHome),
	}, nil
}

// setupWorkspacesEntry and setupWorkDirName spell the only shape a setup root
// may take: $OMNIPUS_HOME/workspaces/<id>/work. They mirror the "workspaces"
// entry of SecretEntriesPerTurn and pkg/workspace's work-directory name; this
// package is a stdlib-only leaf and cannot import pkg/workspace.
const (
	setupWorkspacesEntry = "workspaces"
	setupWorkDirName     = "work"
)

// isWorkspaceWorkDir reports whether cleanRoot is exactly
// <workspaces carve-out root>/<id>/work for one of carveOuts. The comparison
// is byte-exact on cleaned paths: a setup root is a GRANT, so the strict
// spelling is the safe direction (the same direction IsCarveOut's
// ownTreeExempts takes through CoversForGrant). A policy with no workspaces
// carve-out root has nothing a setup root could be exempt from, so every
// setup root is refused there.
func isWorkspaceWorkDir(cleanRoot string, carveOuts []string) bool {
	if filepath.Base(cleanRoot) != setupWorkDirName {
		return false
	}
	workspaceDir := filepath.Dir(cleanRoot)
	id := filepath.Base(workspaceDir)
	if id == "" || id == "." || id == ".." || id == string(filepath.Separator) || strings.Contains(id, "..") {
		return false
	}
	parent := filepath.Dir(workspaceDir)
	for _, carve := range carveOuts {
		cleanCarve := filepath.Clean(carve)
		if filepath.Base(cleanCarve) == setupWorkspacesEntry && cleanCarve == parent {
			return true
		}
	}
	return false
}

// WithSetupRoots returns a copy of p carrying roots — workspace work
// directories, each resolved to its realpath like WorkDir — as SetupRoots
// (carve-out exemption) and as AllowedRoots (write grant). It fails closed: a
// root that cannot be resolved, or a policy the result would make invalid,
// returns an error and the zero FSPolicy.
func WithSetupRoots(p FSPolicy, roots []string) (FSPolicy, error) {
	if len(roots) == 0 {
		return p, nil
	}
	out := p
	out.SetupRoots = append([]string(nil), p.SetupRoots...)
	out.AllowedRoots = append([]string(nil), p.AllowedRoots...)
	for _, root := range roots {
		resolved, err := realpath(root)
		if err != nil {
			return FSPolicy{}, fmt.Errorf("fspolicy: resolve setup root %q: %w", root, err)
		}
		out.SetupRoots = append(out.SetupRoots, resolved)
		out.AllowedRoots = append(out.AllowedRoots, resolved)
	}
	if err := out.Validate(); err != nil {
		return FSPolicy{}, err
	}
	return out, nil
}

// realpath resolves path to an absolute, symlink-resolved location. When the
// leaf (and possibly some parent components) do not yet exist on disk, it
// resolves the deepest existing ancestor through symlinks and re-attaches
// the not-yet-created remainder — the same walk-up-until-found strategy as
// resolvePathAgainstExistingAncestor in pkg/tools/filesystem.go, extended to
// preserve the intended suffix so a not-yet-materialized work/ dir still
// yields the intended path rather than collapsing to its existing parent.
//
// Fails closed: returns a non-nil error if path cannot be made absolute, or
// if resolution hits a non-"not exist" error at any point in the walk (e.g.
// a permission error, or an invalid path such as one containing an embedded
// NUL byte) — it never falls back to returning an unresolved or
// partially-resolved path. An empty (or all-whitespace) path is refused
// outright rather than being handed to filepath.Abs, which would otherwise
// silently resolve "" to the process's own current working directory
// (BLOCK #1, ADR-046 P1 review) — this is the same fail-closed guard
// EffectiveFSPolicy already applies to its own effectiveWorkDir computation,
// duplicated here so realpath is safe to call directly with any caller-
// supplied string, not just the ones EffectiveFSPolicy happens to pre-check.
func realpath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	cleaned := filepath.Clean(abs)

	if resolved, evalErr := filepath.EvalSymlinks(cleaned); evalErr == nil {
		return filepath.Clean(resolved), nil
	} else if !errors.Is(evalErr, os.ErrNotExist) {
		return "", fmt.Errorf("resolve symlinks for %q: %w", cleaned, evalErr)
	}

	dir := filepath.Dir(cleaned)
	remainder := filepath.Base(cleaned)
	for {
		resolved, evalErr := filepath.EvalSymlinks(dir)
		if evalErr == nil {
			return filepath.Clean(filepath.Join(resolved, remainder)), nil
		}
		if !errors.Is(evalErr, os.ErrNotExist) {
			return "", fmt.Errorf("resolve ancestor %q: %w", dir, evalErr)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no existing ancestor found for %q", cleaned)
		}
		remainder = filepath.Join(filepath.Base(dir), remainder)
		dir = parent
	}
}
