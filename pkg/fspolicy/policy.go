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
	// ADR-061 FR-6.1: a workspace's mounts land here — populated by
	// tools.ResolveTurnFSPolicy from workspace.AllowedMountRoots, and rendered
	// into the kernel policy as write grants by sandbox.DeriveKernelPolicy. It
	// is nil only for a turn with no mounts.
	//
	// (This field was nil for every caller before mounts existed, and the
	// comment here said so. It is now load-bearing: code that treats it as
	// permanently empty — for instance by skipping a containment check
	// "because it can't match anything" — silently drops a real grant.)
	//
	// A mount grants WRITE and nothing else, because post-ADR-060 reads need
	// no grant at all. It cannot reopen a secret: IsCarveOut and DeniedPathsFor
	// take no AllowedRoots parameter, so mounting even $HOME yields "write to
	// $HOME minus the secret set" (asserted in mount_secret_independence_test.go).
	AllowedRoots []string
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
func EffectiveFSPolicy(
	ctx context.Context,
	agentHome, turnWorkDir string,
	restrict bool,
	omnipusHome, agentID, workspaceID string,
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
	}, nil
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
	} else if !os.IsNotExist(evalErr) {
		return "", fmt.Errorf("resolve symlinks for %q: %w", cleaned, evalErr)
	}

	dir := filepath.Dir(cleaned)
	remainder := filepath.Base(cleaned)
	for {
		resolved, evalErr := filepath.EvalSymlinks(dir)
		if evalErr == nil {
			return filepath.Clean(filepath.Join(resolved, remainder)), nil
		}
		if !os.IsNotExist(evalErr) {
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
