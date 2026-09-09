package fspolicy

import (
	"path/filepath"
	"strings"
)

// buildCarveOuts returns the fixed, $OMNIPUS_HOME-anchored carve-out roots
// that are always denied regardless of scope (FR-017): the master key file,
// the encrypted credential store, and the whole agents/, workspaces/, and
// entities/ subtrees. These are whole-subtree roots — never enumerated per
// agent id or workspace id.
//
// entities/ (ADR-054 D2/D4, added 2026-07-25) holds the per-entity JSON
// records pkg/entity persists (starting with entities/agents/<id>.json — the
// AgentConfig record split out of config.json). This carve-out is CRITICAL
// and non-negotiable: an AgentConfig record carries the Constraint #6 tool-
// policy map, Locked, and Default. Without entities/ on this list, an agent
// running under FSScopeUnrestricted could rewrite EVERY agent's security
// record directly — granting itself every tool, clearing Locked, or setting
// Default:true to hijack routing — which is strictly worse than the
// agents/-only carve-out this replaces (that at least shielded other
// agents' directories). See ADR-054 §4 ("MANDATORY — entities must be added
// to the carve-out list").
//
// omnipusHome MUST already be an absolute, realpath'd location; the caller,
// EffectiveFSPolicy, resolves it before calling this.
func buildCarveOuts(omnipusHome string) []string {
	// ADR-063 D3 / spec FR-3.1: one definition, shared with the kernel layer.
	//
	// This used to be its own five-entry literal. It is now SecretPaths, which
	// is the union of this list and the kernel layer's — each half protected
	// something the other missed. Concretely, the app layer gains:
	//
	//   config.json  — a child that can write it turns its own sandbox off
	//   cli.token    — a LIVE gateway bearer token, previously readable by any
	//                  agent running unrestricted, through read_file
	//   config.json.bak-*, credentials.json.*, master.key.*
	//                — a copy of a secret is a secret; a real install carries
	//                  a config backup holding the same token
	//
	// and the kernel layer gains agents/ and workspaces/ from here. Keeping two
	// lists in step by hand is what produced that divergence, so there is now
	// exactly one and neither layer owns it.
	//
	// appCarveOutSecretPaths, not SecretPaths: ADR-072 D10.3 carves out ONE
	// documented exception to "both layers deny the same list" —
	// SecretEntriesAlwaysPathOnly (`skills`) stays a whole-directory deny at
	// the kernel layer while the app layer gates it at file granularity
	// instead, in pkg/tools/resolvepath.go (a skill's instruction file is
	// routed through the Skill tool; its bundled helper scripts, templates and
	// reference files are ordinary readable files). Denying the directory here
	// as well would both break every skill that bundles anything AND silently
	// override that finer gate, since ResolvePath consults IsCarveOut before
	// it. It is still derived from the one list, not a second hand-copied one
	// — see appCarveOutSecretPaths.
	return appCarveOutSecretPaths(omnipusHome)
}

// IsCarveOut reports whether resolvedAbsPath falls on or under any of
// policy.CarveOuts.
//
// This replaces the broken isCrossAgentPath (pkg/tools/filesystem.go:98-115),
// whose bug was deriving agentsRoot = filepath.Dir(absWorkspace) — correct
// only when the working dir happened to be exactly agents/<id>/, and
// silently permissive under a re-rooted workspace turn (where the working
// dir is workspaces/<id>/work/ instead). IsCarveOut instead anchors
// exclusively on policy.CarveOuts, which EffectiveFSPolicy always builds
// from the boot-known $OMNIPUS_HOME — never derived from the (re-rootable)
// working directory.
//
// resolvedAbsPath MUST already be realpath'd and filepath.Clean'd by the
// caller (typically the ResolvePath resolver) — this function performs no
// further symlink resolution of its own.
//
// It DOES stat, as of the APFS case-insensitivity fix: containment is judged
// by filesystem identity (device+inode) rather than by comparing path strings,
// because filepath.EvalSymlinks does not canonicalise case and
// $OMNIPUS_HOME/CLI.TOKEN and $OMNIPUS_HOME/cli.token are one file to the
// kernel and two strings to a byte comparison. See pathidentity.go's header
// for the measured leak, why case folding alone was not enough, and what
// residual remains. Both callers (ResolvePath and its I/O-time re-check)
// already do path I/O immediately either side of this call, so this adds no
// new class of failure — and an unanswerable stat degrades to a case-folded
// comparison here, never to "allow".
//
// Own-tree exception: a path that is also within policy.WorkDir is never
// treated as a carve-out, even when it falls under a coarse carve-out root
// (agents/ or workspaces/). This matters for the agent-home-rooted WorkDir
// shape (WorkDir == <home>/agents/<self>) — the agent's own home must not be
// a carve-out of itself. It does NOT change anything for the re-rooted
// workspace-turn shape (WorkDir == <home>/workspaces/<id>/work) — there,
// agents/<self>/ falls outside WorkDir just like any other agent's home, so
// the agent's own home stays exactly as unreachable as anyone else's during
// a workspace turn, matching today's re-root behavior.
//
// BLOCK #2 (ADR-046 P1 review): the own-tree exception is scoped PER-ROOT —
// it only exempts a path matched against carve-out root R when WorkDir is a
// PROPER DESCENDANT of that SAME root R (WorkDir != R, WorkDir strictly
// under R). A naive "cleanPath is within-or-equal WorkDir" check (with no
// relationship required between WorkDir and R) is defeatable by a
// misconfigured WorkDir: if WorkDir == $OMNIPUS_HOME itself, every one of
// policy.CarveOuts (all direct children of $OMNIPUS_HOME) would sit "within
// WorkDir" simultaneously, exempting master.key/credentials.json/agents/
// workspaces all at once — the exact bypass this fix closes.
// FSPolicy.Validate (policy.go) independently refuses to construct a policy
// shaped that way, but IsCarveOut stays correct here too, in depth, since
// callers besides ResolvePath may invoke it directly.
//
// Prefix checks (only ever reached as a fallback, when the filesystem cannot
// answer by identity) use a trailing-separator guard (matching
// isCrossAgentPath's existing precedent) so e.g. "/a/bc" is never mistaken for
// a descendant of "/a/b".
//
// The deny leg and the own-tree exception legs use DIFFERENT fallbacks, and the
// difference is load-bearing (pathidentity.go's header has the full rationale):
// the deny leg folds case, because over-matching only denies; the exception
// legs stay byte-exact, because a folded exception would re-admit agents/MIA
// when the caller's own home is agents/mia — two different agents on a
// case-sensitive volume. A single "fold everywhere" helper would have been
// wrong in the second direction.
func IsCarveOut(resolvedAbsPath string, policy FSPolicy) bool {
	cleanPath := filepath.Clean(resolvedAbsPath)
	cleanWorkDir := filepath.Clean(policy.WorkDir)

	// Backup copies of a secret, matched by PREFIX rather than found by
	// listing. Checked first and with no own-tree exception: these live
	// directly in $OMNIPUS_HOME, which is never inside a work dir (FSPolicy.
	// Validate refuses a policy shaped that way), so there is no legitimate
	// shape in which one is the caller's own file. See CoversSecretBackup for
	// the two plain-ASCII holes the enumerate-at-build-time approach left.
	if CoversSecretBackup(cleanPath, policy.CarveOuts) {
		return true
	}

	// A hard link whose other end lies inside a DIRECTORY-shaped carve-out.
	//
	// Checked here, ahead of the per-root loop, because the loop cannot answer
	// it: the alias is a real file at a real path inside the work dir, so the
	// deny leg matches on the coarse root and the own-tree exception then
	// un-matches it. Only the CONTENT belongs to someone else, and content is
	// what identity is for. See aliasesSecretDirectory for the measured leak
	// (backups/, system/, entities/ all served through the real tool chain)
	// and for the cost gates that keep this to a single stat for an ordinary
	// file.
	if aliasesSecretDirectory(cleanPath, policy) {
		return true
	}

	// Stat the candidate's ancestry ONCE and reuse it for every carve-out
	// root, rather than re-walking it per root.
	pathChain, pathChainOK := ancestorChain(cleanPath)

	for _, root := range policy.CarveOuts {
		cleanRoot := filepath.Clean(root)
		if !coversForDenyChain(cleanRoot, cleanPath, pathChain, pathChainOK) {
			continue
		}

		// cleanPath falls on or under this carve-out root. The own-tree
		// exception applies only when WorkDir is a proper descendant of
		// THIS SAME root (never when WorkDir is at or above it) AND
		// cleanPath itself falls within that WorkDir.
		if policy.WorkDir != "" &&
			!SameLocationForDeny(cleanWorkDir, cleanRoot) &&
			CoversForGrant(cleanRoot, cleanWorkDir) &&
			coversForGrantChain(cleanWorkDir, cleanPath, pathChain, pathChainOK) {
			return false
		}

		return true
	}

	return false
}

// isWithinOrEqual reports whether candidate is root itself or lives strictly
// under it, guarded by a trailing separator so "/a/bc" does not match root
// "/a/b".
//
// BYTE-EXACT, and therefore NOT safe to use directly for a deny decision on a
// case-insensitive volume — that is the defect pathidentity.go exists to fix.
// It survives as the STRICT fallback CoversForGrant falls back to, where
// byte-exactness is the conservative direction. New deny-side call sites must
// use CoversForDeny (or PathCoversFold) instead.
func isWithinOrEqual(candidate, root string) bool {
	if candidate == root {
		return true
	}
	rootWithSep := root
	if !strings.HasSuffix(rootWithSep, string(filepath.Separator)) {
		rootWithSep += string(filepath.Separator)
	}
	return strings.HasPrefix(candidate, rootWithSep)
}
