// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"path/filepath"
	"strings"
)

// SecretEntriesRelative is THE definition of what must never be reachable
// inside $OMNIPUS_HOME. ADR-063 D3, spec FR-3.1/FR-3.2. It is the union of two
// lists that used to live apart, and each half protected something the other
// missed:
//
//	master.key, credentials.json  — both lists had these; root of trust
//	config.json, cli.token        — kernel only. config.json lets a child turn
//	                                its own sandbox off on the next boot;
//	                                cli.token is a LIVE gateway bearer token
//	entities                      — both; per-agent tool policy
//	agents, workspaces            — app layer only; cross-agent isolation
//	auth.json, backups            — NEITHER list had these. Both are live
//	                                credential disclosures, and both were found
//	                                by review rather than by the merge; see
//	                                SecretEntriesAlways for the detail.
//
// # Why this lives in pkg/fspolicy and not pkg/sandbox
//
// It is data about the $OMNIPUS_HOME layout, so the leaf package is where it
// belongs on merit. It is also the only place it CAN live: pkg/tools imports
// pkg/sandbox, and ADR-046's P3 wires pkg/sandbox -> pkg/fspolicy, so a
// definition in pkg/sandbox that fspolicy had to import would be an import
// cycle. An earlier draft of the spec specified exactly that and would not have
// compiled. pkg/sandbox.SecretEntriesRelative is an alias for this.
//
// # It is split, and the split is load-bearing
//
// SecretEntriesAlways needs no context. SecretEntriesPerTurn (agents/,
// workspaces/) only means anything once a work dir is known, because those
// roots contain the caller's OWN directory and denying them outright locks an
// agent out of the place it is meant to work. Use SecretPathsAlways where there
// is no turn (kernel boot) and DeniedPathsFor where there is. This combined
// list exists for the app layer, which always has a work dir, and for callers
// that genuinely want the whole vocabulary.
var SecretEntriesRelative = append(append([]string{}, SecretEntriesAlways...), SecretEntriesPerTurn...)

// SecretEntriesAlways is the part of the set that needs NO turn context: no
// agent, in any shape, ever has a legitimate reason to reach these.
//
// This is the subset the KERNEL BOOT policy can use directly, because at boot
// there is no work dir to compare against.
//
// # backups and auth.json — added after the set was re-derived against a live
// install rather than carried over from the two old lists
//
// Two separate reviewers each found a different entry missing here, which is
// the signature of a list that was merged rather than re-checked. Both misses
// were live credential disclosures reachable in ONE tool call, because
// post-FR-2.2 reads are open and send_file (FSOpSend) carries no path
// restriction by operator decision — the carve-out check in
// tools.ResolvePath is the ONLY thing standing between an agent and the file.
//
//	backups    $OMNIPUS_HOME/backups/*.tar.gz. gateway's createTarGz
//	           (pkg/gateway/rest_settings.go) archives ALL of $OMNIPUS_HOME
//	           excluding only logs/ and backups/ — so every archive contains
//	           master.key, credentials.json, config.json, cli.token, auth.json,
//	           entities/, agents/ and workspaces/. Denying the originals while a
//	           tarball of all of them sits readable beside them is a deny that
//	           reads as correct and protects nothing. Before this branch,
//	           FSScopeConfined refused it for a reason unrelated to secrecy;
//	           opening reads removed that accidental protection.
//	auth.json  A LEGACY plaintext OAuth store. pkg/auth used to write
//	           per-provider AccessToken and RefreshToken here as PLAINTEXT
//	           JSON, outside the encrypted credential store; that writer has
//	           since been deleted (see AuthCredential in pkg/auth/store.go)
//	           and nothing in Omnipus creates the file any more. The entry
//	           stays because deleting the WRITER does not delete the FILE:
//	           any install that ever ran the old code still has one on disk,
//	           still full of live-looking tokens, and still one plain
//	           read_file away — no case trick and no race needed.
//
// Neither has any legitimate agent-facing reader: backups are produced and
// consumed by the gateway's own settings endpoints, and auth.json is read only
// by pkg/auth itself inside the gateway process, which is not sandbox-confined.
// So both belong in the ALWAYS half — there is no turn shape in which reaching
// them is correct, and no own-tree exception that could apply.
var SecretEntriesAlways = []string{
	"master.key",
	"credentials.json",
	"config.json",
	"cli.token",
	"entities",
	"auth.json",
	"backups",

	// system — $OMNIPUS_HOME/system/, which holds audit.jsonl, its rotated
	// audit-YYYY-MM-DD.jsonl siblings, audit-chain-checkpoint.json (the HMAC
	// chain's tamper-evidence anchor), token_budget.json, and state.json. The
	// v0.2 HMAC chain (pkg/audit/hmac.go) detects a sandboxed child MODIFYING
	// an entry; it does nothing to stop `rm system/audit.jsonl` or `: >
	// system/audit.jsonl` — an unlink or truncate needs no read and produces
	// no entry to verify. Demonstrated against a real sandboxed child before
	// this line existed. Denied as a whole directory, not just audit.jsonl:
	// every file in system/ is gateway-internal bookkeeping with no
	// legitimate agent-tool reader (run_doctor's own os.Stat on this
	// directory runs in-process in the gateway, unsandboxed, so it is
	// unaffected), and a per-filename list is exactly the kind of hand-copied
	// enumeration that fell behind twice already (see backups/auth.json
	// above) — a future rotated-file naming tweak or a new file dropped in
	// system/ must not need a second edit here to stay covered.
	"system",
}

// SecretEntriesPerTurn is the part that is only meaningful WITH a work dir,
// because of the own-tree exception.
//
// `agents/` and `workspaces/` are COARSE roots: they cover every agent's home
// and every workspace, including the caller's own. Denying them outright would
// lock an agent out of the directory it is supposed to be working in — the
// exclusion would break the product while looking like hardening.
//
// So these are only excluded once a work dir is known, via DeniedPathsFor,
// which re-admits the caller's own tree under exactly the same per-root rule
// IsCarveOut applies. Using them without a work dir is a bug; that is why they
// are a separate list rather than a comment on a combined one.
var SecretEntriesPerTurn = []string{
	"agents",
	"workspaces",
}

// secretGlobPrefixes are basename PREFIXES whose every match is a secret.
//
// Protecting config.json alone was not enough: a real install carries
// `config.json.bak-<timestamp>`, written when a migration rewrites the config,
// and VERIFIED on a live install that backup held both the gateway CLI token
// and the user account list. Denying the original while a byte-identical copy
// sits readable beside it is a deny that reads as correct and protects nothing.
//
// Prefix rather than an exact list because the suffix is a timestamp: the set
// is open-ended by construction, so enumerating it would go stale the next time
// a migration runs.
var secretGlobPrefixes = []string{
	"config.json.",
	"credentials.json.",
	"master.key.",
}

// hasSecretBackupPrefix reports whether a basename is a BACKUP COPY of a
// secret — `config.json.bak-<ts>`, `credentials.json.<anything>`,
// `master.key.<anything>`.
//
// DENY-side, so it folds case unconditionally, for the same reason
// IsSecretName does: on a case-insensitive volume `CONFIG.JSON.bak-1` IS the
// backup, and over-matching a distinctly-named sibling under $OMNIPUS_HOME
// merely withholds access to a file with no legitimate agent-facing claimant.
func hasSecretBackupPrefix(name string) bool {
	folded := strings.ToLower(name)
	for _, prefix := range secretGlobPrefixes {
		if strings.HasPrefix(folded, strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}

// CoversSecretBackup reports whether candidate is a backup copy of a secret
// sitting directly beside the carve-out roots — i.e. directly in
// $OMNIPUS_HOME — WITHOUT requiring the file to exist.
//
// # Why this exists as a rule rather than as more entries in the list
//
// secretBackupPaths discovers backups by LISTING $OMNIPUS_HOME at the moment
// the policy is built. That is the wrong tool for a set that must cover files
// which do not exist yet, and it left two plain-ASCII holes — no Unicode, no
// case trick, no race window narrower than a turn:
//
//	write   IsCarveOut($OMNIPUS_HOME/config.json.bak-9999) was FALSE for a
//	        file not on disk, so a write landed there. Harmless on its own;
//	        it is the read half that matters.
//	read    A backup the GATEWAY writes during a turn (a migration rewriting
//	        config.json does exactly this) is absent from that turn's carve-out
//	        list and stays readable by it for the rest of the turn. A real
//	        install's config.json.bak carries the gateway CLI token and the
//	        user account list — verified on a live install, which is why the
//	        prefixes are in the secret set at all.
//
// The exact names in SecretEntriesAlways are returned whether or not they
// exist, precisely so a credential file created a moment later is already
// covered. The prefix set needs the same treatment, and a prefix rule gives it
// by construction. secretBackupPaths survives for the two consumers that
// genuinely need a finite path LIST (the kernel deny list and the Linux
// sibling-granting walk); it is no longer the only way a backup is recognised.
//
// The parent-directory test is identity-based (SameLocationForDeny), so it is
// immune to the case and normalization spellings pathidentity.go documents,
// and it anchors on the carve-out roots themselves rather than on a separately
// passed home path — there is no second copy of "where is $OMNIPUS_HOME" to
// drift out of step with the first.
func CoversSecretBackup(candidate string, carveOuts []string) bool {
	clean := filepath.Clean(candidate)
	if !hasSecretBackupPrefix(filepath.Base(clean)) {
		return false
	}
	dir := filepath.Dir(clean)
	for _, root := range carveOuts {
		if SameLocationForDeny(dir, filepath.Dir(filepath.Clean(root))) {
			return true
		}
	}
	return false
}

// SecretBackupPathPrefixes returns the absolute path PREFIXES under home whose
// every match is a secret backup: `<home>/config.json.`,
// `<home>/credentials.json.`, `<home>/master.key.`.
//
// It is the kernel-layer counterpart to CoversSecretBackup, and it exists for
// the same reason: a backup the gateway writes DURING a turn is absent from any
// list enumerated before that turn started. Measured against a real child under
// /usr/bin/sandbox-exec, with the whole enumerated deny list in place:
//
//	cat <home>/config.json              -> Operation not permitted
//	cat <home>/config.json.bak-<ts>     -> TOKEN-IN-BACKUP   (created mid-turn)
//
// macOS renders each prefix as an anchored regex deny, which is the only
// Seatbelt filter that can cover a path that does not exist yet: subpath and
// literal both name a specific path, and `config.json.bak-1` is not "under"
// `config.json` in the subpath sense. Linux needs nothing — the grant-based
// walk never grants $OMNIPUS_HOME itself, only its existing children, so an
// entry created later carries no grant at all.
//
// Returns nil for an empty home, matching SecretPaths.
func SecretBackupPathPrefixes(home string) []string {
	if home == "" {
		return nil
	}
	clean := filepath.Clean(home)
	out := make([]string, 0, len(secretGlobPrefixes))
	for _, prefix := range secretGlobPrefixes {
		out = append(out, filepath.Join(clean, prefix))
	}
	return out
}

// SecretPaths returns the absolute path of every secret under home: the exact
// SecretEntriesRelative names, then any existing entry whose basename starts
// with a secretGlobPrefixes entry.
//
// The exact names are returned whether or not they exist — a credential file
// created after this call must already be covered, and returning only extant
// paths would leave a window in which it is created and readable. The prefix
// matches are necessarily discovered by listing, so they cover what is on disk
// at call time.
//
// Returns nil for an empty home so a caller with no configured home does not
// produce denies rooted at "/".
func SecretPaths(home string) []string {
	if home == "" {
		return nil
	}
	clean := filepath.Clean(home)
	out := make([]string, 0, len(SecretEntriesRelative)+4)
	seen := make(map[string]struct{}, len(SecretEntriesRelative)+4)
	for _, name := range SecretEntriesRelative {
		p := filepath.Join(clean, name)
		out = append(out, p)
		seen[p] = struct{}{}
	}
	for _, p := range secretBackupPaths(clean) {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// SecretPathsAlways returns only the context-free part of the set: the entries
// no agent may reach in any shape. Use this where there is no work dir to
// compare against — the kernel policy computed at boot, before any turn exists.
//
// Using SecretPaths there instead would deny `agents/` and `workspaces/`
// wholesale to every child, locking every agent out of its own working
// directory. That is not a hypothetical: it is what the first cut of this
// change did, and four tests caught it immediately.
func SecretPathsAlways(home string) []string {
	if home == "" {
		return nil
	}
	clean := filepath.Clean(home)
	out := make([]string, 0, len(SecretEntriesAlways)+4)
	for _, name := range SecretEntriesAlways {
		out = append(out, filepath.Join(clean, name))
	}
	return append(out, secretBackupPaths(clean)...)
}

// DeniedPathsFor returns the paths a turn with this work dir must not reach:
// every SecretEntriesAlways entry, plus the SecretEntriesPerTurn roots MINUS
// the one containing the caller's own work dir.
//
// The exception is deliberately the same per-root rule IsCarveOut applies, and
// it is narrow: a root R is re-admitted only when workDir is a PROPER
// DESCENDANT of R. Two consequences that are easy to get backwards, and are
// both intended:
//
//   - Agent-home-rooted turn (workDir == <home>/agents/<self>): `agents` is
//     re-admitted, so the agent reaches its own home. It still cannot reach
//     ANOTHER agent's home, because the app layer's IsCarveOut applies the same
//     rule at path granularity.
//   - Re-rooted workspace turn (workDir == <home>/workspaces/<id>/work):
//     `agents` is NOT re-admitted — during a workspace turn an agent's own home
//     is as unreachable as anyone else's, matching today's behaviour exactly.
//
// An empty workDir re-admits nothing, which is the safe direction: it yields
// the full set.
func DeniedPathsFor(home, workDir string) []string {
	if home == "" {
		return nil
	}
	clean := filepath.Clean(home)
	out := make([]string, 0, len(SecretEntriesRelative)+4)
	for _, name := range SecretEntriesAlways {
		out = append(out, filepath.Join(clean, name))
	}
	for _, name := range SecretEntriesPerTurn {
		root := filepath.Join(clean, name)
		if workDir != "" && isProperDescendant(filepath.Clean(workDir), root) {
			continue // own tree — re-admitted
		}
		out = append(out, root)
	}
	return append(out, secretBackupPaths(clean)...)
}

// isProperDescendant reports whether child is strictly inside parent. Equality
// is NOT a descendant: a work dir sitting exactly ON a carve-out root does not
// earn the exception, or pointing a turn at `agents/` itself would re-admit
// every agent's home at once.
//
// GRANT-side (a true answer RE-ADMITS a root that would otherwise be denied),
// so it resolves by filesystem identity where it can and falls back to a
// BYTE-EXACT comparison where it cannot — never a case-folded one. Folding here
// would be a widening: on a case-sensitive volume agents/MIA and agents/mia are
// two different agents' homes, and a folded "own tree" test would re-admit the
// `agents` root for a work dir that is not actually inside it. See
// pathidentity.go's header for why the deny and grant directions get different
// fallbacks.
//
// The identity leg also makes the equality guard real rather than textual: a
// work dir spelled AGENTS that IS the agents root on a case-insensitive volume
// is now recognised as equal, and correctly earns no exception.
func isProperDescendant(child, parent string) bool {
	if SameLocationForDeny(child, parent) {
		return false
	}
	if CoversForGrant(parent, child) {
		return true
	}
	return strings.HasPrefix(child, parent+string(filepath.Separator))
}

// IsSecretName reports whether a $OMNIPUS_HOME-relative entry name is a secret,
// by exact match or backup prefix. Used by the Linux sibling-granting walk,
// which decides per directory entry whether to grant it.
//
// DENY-side (a true answer WITHHOLDS a grant), and it compares NAMES rather
// than paths, so there is no inode to ask: the comparison folds case
// unconditionally. On a case-insensitive volume that is required for
// correctness — an entry listed as `Config.json` IS config.json. On a
// case-sensitive volume it merely withholds a grant from a distinctly-named
// sibling under $OMNIPUS_HOME, which is the safe direction and has no
// legitimate claimant (the layout is Omnipus-owned).
//
// The Unicode residual that pathidentity.go documents for path folding does not
// bite here: every name in the set is pure ASCII, and the argument is a real
// directory entry, so no NFC/NFD or sharp-s variant can collide with one.
func IsSecretName(name string) bool {
	folded := strings.ToLower(name)
	for _, prefix := range secretGlobPrefixes {
		if strings.HasPrefix(folded, strings.ToLower(prefix)) {
			return true
		}
	}
	for _, s := range SecretEntriesRelative {
		if strings.EqualFold(s, name) {
			return true
		}
	}
	return false
}
