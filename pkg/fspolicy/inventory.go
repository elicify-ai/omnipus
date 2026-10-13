// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"path/filepath"
	"strings"
)

// This file is the named protected inventory for amendment §2 (issue #1284):
// everything under $OMNIPUS_HOME that no agent tool and no agent-spawned
// child may discover, read or write, in any permission mode — God Mode
// included. It is ONE list serving BOTH enforcement layers: the app-layer
// predicate (IsCarveOut's home-guard leg) and the kernel enumeration
// (KernelDenyEnumeration) read it, so the two can never name different sets.
//
// The model around it (binding L1-AMENDMENTS-20261013.md §2 D1) is:
//
//	protected(path) = inInventory(path)                      -- named, always wins
//	                OR ( under($OMNIPUS_HOME, path)          -- default-deny for
//	                     AND NOT allowed(path) )             -- anything unnamed,
//	                                                         // incl. future files
//
// allowed(path) = within an explicit allow entry (HomeGuard.Allow) or within
// the turn's WorkDir (own-tree exception, narrowed by agentHomeInternal below).
//
// Deliberately NOT here: uploads (protected by the default-deny leg with the
// A4 session-scoped allow entries re-admitting the acting/root sessions —
// D2's "uploads/ (minus allow entry A4)"), media and skills (A3/A7 allow
// entries, not protected entries), and agents//workspaces/ as wholes
// (per-turn own-tree plus the default-deny leg covers other agents'
// trees — the same answer the old SecretEntriesPerTurn coarse roots gave,
// now at path granularity).

// ProtectedInventoryNames are $OMNIPUS_HOME-relative entries (files or whole
// subtrees) that are protected BY NAME — they win over every allow entry and
// every mount. Exact names are covered whether or not they exist on disk, so
// an entry the gateway creates lazily is already protected.
//
// Provenance of each row is the amendment §2 D2 table; entries that already
// existed in SecretEntriesAlways keep their original justification comments
// there and are listed here as the single D2 inventory.
var ProtectedInventoryNames = []string{
	// Root of trust / config (existing secret set).
	"master.key",
	"credentials.json",
	"config.json",
	"cli.token",
	"auth.json",
	"entities",
	"backups",
	"system",

	// Audit and gateway state (logs/, gateway.pid, gateway.port new).
	"logs",
	"gateway.pid",
	"gateway.port",

	// Sessions, transcripts, tasks.
	"sessions",
	"session_messages",
	"session_lifecycle",
	"tool_results",
	"tasks",
	"plans",
	"pins",
	"milestones",

	// Channels, mail, notifications, browser, cache, and the named
	// lazily-created dirs the amendment lists as explanatory: they are
	// covered by the default-deny leg either way; naming them keeps the
	// kernel enumeration complete for installs where they exist but the
	// walk races their creation.
	"channels",
	"notifications",
	"browser",
	"cache",
	"inbox",
	"email-watch",
	"delivery",
	"cron",

	// Global user profile (USER.md write-blocked by metadata_guard; the
	// inventory now also denies reads for every tool and child).
	"USER.md",
}

// protectedInventoryPrefixes are basename PREFIXES whose every match directly
// in $OMNIPUS_HOME is protected. Backup copies of secrets live in
// secretGlobPrefixes (secretset.go) and are checked separately; boot_epoch
// minted files are the amendment's `boot_epoch*` row.
var protectedInventoryPrefixes = []string{
	"boot_epoch",
}

// AgentHomeInternalDirs are the directory names inside an agent's home
// (agents/<id>/) that stay protected even for the agent whose home it is
// (amendment §2 D4): private memory and room state, legacy per-agent
// session files, and the skills registry (ADR-072 D10.3 — children denied;
// the app-layer file tools gate instruction files via the skills gate).
var AgentHomeInternalDirs = []string{
	".omnipus",
	"memory",
	"sessions",
	"skills",
}

// AgentHomeInternalFiles are the definition files inside an agent's home that
// stay protected from generic file tools and children (metadata_guard keeps
// its redirect messages for the file tools; this list is the underlying
// protection every consumer shares).
var AgentHomeInternalFiles = []string{
	"SOUL.md",
	"AGENT.md",
	"HEARTBEAT.md",
	"MEMORY.md",
}

// workspaceInternalDir is the per-workspace internal directory
// (workspaces/<id>/.omnipus) — shared room, carry-forward summaries. The
// kernel already denied it in workspace-rooted turns; the inventory now
// names it for every tool and child.
const workspaceInternalDir = ".omnipus"

// workspaceWorkDirName is the working subtree of a workspace
// (workspaces/<id>/work) — the one part of a workspace that is the turn's
// own working area.
const workspaceWorkDirName = "work"

// SkillInstructionLeaves are the filenames D10.3's narrowed Part A gates — a
// skill's INSTRUCTIONS, not its directory. They are single-sourced HERE
// (the #1284 predicate denies them under the registry skills shelf, A7) and
// pkg/tools' skills gate reads them through IsSkillInstructionLeaf so the
// ResolvePath refusal message and the predicate can never name different
// files. Keys are lowercase; the lookup folds case (deny side).
var SkillInstructionLeaves = map[string]bool{
	"skill.md":  true,
	"agent.md":  true,
	"agents.md": true,
}

// IsSkillInstructionLeaf reports whether name — a final path component — is
// one of the gated skill instruction filenames. Deny-side: folds case.
func IsSkillInstructionLeaf(name string) bool {
	return SkillInstructionLeaves[strings.ToLower(name)]
}

// BaseAllowRelative are the $OMNIPUS_HOME-relative subtrees the app layer
// re-admits for every turn without per-turn context (amendment §2 D3):
//
//	media  (A3) — downloaded media and attachment staging, read-only
//	skills (A7) — bundled skill helper files, read-only, FILE TOOLS ONLY;
//	              instruction files stay behind the skills gate in
//	              pkg/tools/resolvepath.go, and spawned children keep the
//	              whole-directory kernel deny (CoversChildOnlySecretPath)
var BaseAllowRelative = []string{
	"media",
	"skills",
}

// HomeGlobPrefixes returns the protected basename prefixes that live directly
// in home (boot_epoch*), as absolute path prefixes. Distinct from
// SecretBackupPathPrefixes so each rule keeps its own reason.
func HomeGlobPrefixes(home string) []string {
	if home == "" {
		return nil
	}
	clean := filepath.Clean(home)
	out := make([]string, 0, len(protectedInventoryPrefixes))
	for _, prefix := range protectedInventoryPrefixes {
		out = append(out, filepath.Join(clean, prefix))
	}
	return out
}

// hasProtectedInventoryPrefix reports whether name (a basename directly in
// $OMNIPUS_HOME) matches a protected prefix. DENY-side: folds case, because
// on a case-insensitive volume BOOT_EPOCH-1 IS boot_epoch-1, and over-matching
// a distinctly-named sibling in a directory Omnipus owns withholds only an
// access no agent has a legitimate claim to.
func hasProtectedInventoryPrefix(name string) bool {
	folded := strings.ToLower(name)
	for _, prefix := range protectedInventoryPrefixes {
		if strings.HasPrefix(folded, strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}

// BaseHomeGuard builds the turn-independent half of the home guard: the home
// anchor plus the A3/A7 read-only allow entries. Per-turn callers
// (tools.ResolveTurnFSPolicy) append the session-scoped uploads entries and
// the workspace mounts afterwards.
func BaseHomeGuard(home string) HomeGuard {
	if home == "" {
		return HomeGuard{}
	}
	clean := filepath.Clean(home)
	allow := make([]AllowRoot, 0, len(BaseAllowRelative))
	for _, name := range BaseAllowRelative {
		allow = append(allow, AllowRoot{Path: filepath.Join(clean, name), Writable: false})
	}
	return HomeGuard{Home: clean, Allow: allow}
}

// KernelBaseBranches returns the turn-independent allowed branches of the
// KERNEL enumeration (amendment §2 D3, "children:" column): media is the one
// base entry children may read (A3). Skills is deliberately absent — children
// keep the whole-directory deny (A7 / ADR-072 D10.3), and uploads are
// protected for children (A4 names no child access).
func KernelBaseBranches(home string) []KernelBranch {
	if home == "" {
		return nil
	}
	return []KernelBranch{{Path: filepath.Join(filepath.Clean(home), "media"), Writable: false}}
}
