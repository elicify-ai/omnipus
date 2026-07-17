// Package fspolicy computes the single, authoritative filesystem-access
// policy for an agent turn (ADR-046, unified filesystem/workspace model,
// FR-036). EffectiveFSPolicy is the one function of record consumed by both
// the app-layer path resolver (ResolvePath, P1/P2) and the future per-child
// Landlock ruleset builder (P3) — so the two enforcement backends can never
// drift apart.
//
// This package is intentionally a leaf: it imports only pkg/config. It must
// NOT import pkg/tools — pkg/tools already imports pkg/sandbox, and P3 wires
// pkg/sandbox -> fspolicy, so importing pkg/tools here would create an
// import cycle.
package fspolicy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

	// AllowedRoots is always nil in P1. Reserved for P2's per-path allow
	// seam (approved "ask" grants, additional allow-listed roots).
	AllowedRoots []string
}

// EffectiveFSPolicy computes the single, authoritative FSPolicy for a turn
// (FR-036).
//
// WorkDir is the realpath of turnWorkDir when non-empty, else the realpath
// of agentHome. Scope maps from restrict (true -> FSScopeConfined, false ->
// FSScopeUnrestricted); P1 never produces FSScopeAsk or FSScopeAllow.
// CarveOuts are always the four omnipusHome-anchored roots built by
// buildCarveOuts. AllowedRoots is always nil in P1.
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
// resolveExistingAncestor in pkg/tools/filesystem.go, extended to preserve
// the intended suffix so a not-yet-materialized work/ dir still yields the
// intended path rather than collapsing to its existing parent.
//
// Fails closed: returns a non-nil error if path cannot be made absolute, or
// if resolution hits a non-"not exist" error at any point in the walk (e.g.
// a permission error, or an invalid path such as one containing an embedded
// NUL byte) — it never falls back to returning an unresolved or
// partially-resolved path.
func realpath(path string) (string, error) {
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
