// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — ADR-046 P1 resolver core. ResolvePath is the single,
// mandatory chokepoint every path-taking tool MUST route through (FR-003,
// FR-034): it roots relative paths at the turn's effective working directory
// (FR-004), gates absolute/escaping paths by the effective filesystem_scope
// (FR-005), resolves symlinks and anchors confinement on the realpath
// (FR-006), and — critically — never hands a resolved path back as a bare
// string for a tool to os.Open independently. Instead it returns a
// *PathHandle backed by a Go 1.24 os.Root, so every subsequent I/O operation
// is enforced at the syscall boundary on every call, closing the CWE-357
// TOCTOU gap that validatePathWithAllowPaths' "resolve-then-return-a-string"
// shape left open (BLOCK #1 in the ADR-046 grill).
//
// P1 scope: this file implements confined-vs-unrestricted resolution only
// (fspolicy.FSScopeConfined / fspolicy.FSScopeUnrestricted). The ask/allow
// tri-state (fspolicy.FSScopeAsk / FSScopeAllow) is P2 — toolName/callID are
// threaded through the signature for that future ask-flow + audit dimension
// but are NOT consulted for any decision here (Constraint #6: no invented
// default, no early branch on a scope this package doesn't yet implement).
//
// ADR-063 / spec unified-file-access-and-mounts FR-2: op IS now consulted —
// see FSOp's own doc comment and ResolvePath's resolution-order comment
// below for the operation-aware decision table.

package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// FSOp classifies the filesystem operation a ResolvePath caller is about to
// perform. Originally threaded through as an inert seam for the P2 ask-flow
// and the FR-035 audit dimension (ADR-046 P1 discarded it via `_ = op`) —
// ADR-063 / spec unified-file-access-and-mounts FR-2 makes it load-bearing:
// ResolvePath's decision for a path OUTSIDE the effective working directory
// now depends on op:
//
//	FSOpRead, FSOpList, FSOpSend  allowed anywhere except the secret set
//	                              (fspolicy.IsCarveOut, checked unconditionally
//	                              regardless of op — see ResolvePath below)
//	FSOpWrite, FSOpServe          work dir or a mount (policy.AllowedRoots)
//	                              only — never open, regardless of Scope
//	FSOpExec                      per the ADR-062 kernel model — left on the
//	                              pre-FR-2 Scope-based path unchanged
//
// The zero value is REFUSED, not defaulted (FR-2.4): every real call site
// already passes one of the named constants below, so an empty FSOp
// reaching ResolvePath is a caller bug, and ResolvePath fails loudly on it
// rather than silently taking whichever branch a switch's default case
// would otherwise fall into.
type FSOp string

const (
	FSOpRead  FSOp = "read"
	FSOpWrite FSOp = "write"
	FSOpList  FSOp = "list"
	FSOpExec  FSOp = "exec"
	FSOpServe FSOp = "serve"

	// FSOpSend distinguishes a disclosure to a chat channel (send_file) from
	// an ordinary read, purely for audit and any future ask-flow (FR-2.3a).
	// It carries NO additional path restriction beyond the open-read rule —
	// the operator explicitly rejected a path-based "publish" gate for
	// send_file (spec FR-2.3): it would have been bypassable in one extra
	// step (read_file the path, paste the contents into the chat message),
	// so the real gate is tool policy (Constraint #6), which already exists,
	// is explicit per agent, and is hard-validated with no defaults.
	FSOpSend FSOp = "send"
)

// ADR-072 (skill activation and loading) D10/D10.3 / D6.1.1 — the skill read
// gate and the write-path audit hook. Both live here because ResolvePath is
// the one chokepoint every file tool passes through (D6.1.1's own reasoning
// for moving the write audit off the authoring tool applies just as much to
// the read gate: enumerating tools is how a gate silently falls behind).
//
// # D10.3 revision — the gate covers a skill's INSTRUCTION FILE, not its
// # whole directory, and Part B (project skills) is gone entirely
//
// The original Part A/Part B split (denying an entire skill directory, both
// shelves) broke every skill that bundles a sibling file its own
// instructions tell the agent to read or run — "run the script next to me"
// is the ordinary shape of a real Claude Code skill, not an edge case (the
// plan-spec skill used to build ADR-072's own spec bundles four such files).
// D10.3 narrows what "using a skill goes through the tool" (§6.6) actually
// requires: an agent must load a skill's INSTRUCTIONS through the Skill
// tool's own grant check; a bundled script/template/reference file is inert
// without those instructions and reads/executes normally like any other
// file. So:
//
//   - Part A (the installed registry, FR-057/FR-061a-c) narrows from "the
//     whole registry skill directory" to "the skill's instruction file
//     specifically" — see isSkillInstructionFileLeaf below. A bundled
//     sibling file of even an UNGRANTED registry skill becomes plain-
//     readable; only the instruction file itself stays gated. This is a
//     deliberate, accepted cost (an author could hide instructions in a
//     bundled reference file) against the alternative, which breaks most
//     real skills that bundle anything.
//   - Part B (project skills inside a mount, the original FR-058/FR-059) is
//     REMOVED entirely, for reads. D4.1 already makes the mount itself the
//     grant — every agent in that workspace may load every skill in that
//     mount via the Skill tool regardless — so a read-gate over content the
//     agent can already load on demand protects nothing; its only
//     observable effect was breaking bundled project-skill files the same
//     way. classifySkillsGatePath below still classifies a project-shelf
//     path (for the D6.1.1 write-audit tag, FR-071a's "shelf" field, which
//     applies regardless of file type) — it is simply never used to DENY a
//     read/list/send for that shelf any more; see the op-dispatch switch
//     further down in ResolvePath.
//
// # This gate is the app layer's ONLY skills rule — there is no backstop
// # underneath it
//
// An earlier revision of this comment described the gate below as
// defense-in-depth behind a broader mechanism: `skills` had joined fspolicy's
// SecretEntriesAlwaysPathOnly, that list fed SecretEntriesRelative ->
// SecretPaths -> buildCarveOuts, and so fspolicy.IsCarveOut — consulted a few
// lines above this gate in ResolvePath — refused EVERY path under
// $OMNIPUS_HOME/skills before this classification ever ran.
//
// That inverted the intended behaviour and made D10.3 dead code in
// production: the coarse deny answered first, so a registry skill's bundled
// helper file was refused exactly as it had been before the narrowing, and
// even the instruction file's refusal came from the wrong mechanism (a bare
// ErrCarveOut, with no explanation naming the Skill tool). It survived review
// because every test of this gate builds an fspolicy.FSPolicy literal with
// CarveOuts left nil — a shape no production caller produces.
//
// So `skills` is no longer in the app layer's carve-out roots
// (fspolicy.appCarveOutSecretPaths, ADR-072 D10.3), and the classification
// below is the sole, authoritative app-layer rule for both shelves. It has to
// be correct on its own: there is nothing behind it.
//
// The KERNEL layer still denies the whole $OMNIPUS_HOME/skills directory to
// spawned children on POSIX (fspolicy.KernelDeniedPathsFor still reads
// SecretEntriesAlwaysPathOnly). That asymmetry is deliberate and documented —
// narrowing the kernel deny to instruction files needs the Linux child-only
// spike ADR-072 D10.2/§6.8 flags — and it is why `bash` cannot read a skill's
// bundled file that read_file now can. On Windows there is no kernel backend
// at all and nothing is closed there; D10.2 states exactly that.
//
// # Recognised locations
//
// A project skill's directory names ("<mount>/.claude/skills/" or
// "<mount>/.omnipus/skills/") are duplicated here (rather than imported)
// because pkg/skills/project.go does not export them; pkg/tools already
// imports pkg/skills elsewhere (skills_search.go, skills_install.go) with no
// cycle, but this package's classification must not depend on that
// package's internal naming staying exported.
//
// A mount's own instruction file (CLAUDE.md/AGENTS.md at the mount ROOT) is
// deliberately NOT under either recognised subdirectory, so it is never
// classified by this predicate and stays ordinarily readable (D7's
// always-injected instruction layer denying it would hide nothing) — no
// special-casing required, it falls out of the location test.
var skillsGateProjectSubdirs = []string{
	filepath.Join(".omnipus", "skills"),
	filepath.Join(".claude", "skills"),
}

// Shelf discriminators for classifySkillsGatePath's return value and the
// FR-071a write-audit record's "shelf" field. Both still classify for audit
// purposes under D10.3 — only the registry shelf's classification still
// feeds a read-deny; see isSkillInstructionFileLeaf and the op-dispatch
// switch in ResolvePath.
const (
	skillShelfRegistry = "registry"
	skillShelfProject  = "project"
)

// skillInstructionFileLeaves are the filenames D10.3's narrowed Part A gates
// — a skill's INSTRUCTIONS, not its directory. Matched case-sensitively
// against the final path component only (the existing custom-agent-format
// precedent: structured AGENT.md singular, with legacy AGENTS.md plural
// still loading as fallback — see CLAUDE.md's "Custom-agent format" note).
var skillInstructionFileLeaves = map[string]bool{
	"SKILL.md":  true,
	"AGENT.md":  true,
	"AGENTS.md": true,
}

// isSkillInstructionFileLeaf reports whether rawPath's final path component
// is one of a skill's recognised instruction filenames (D10.3). Applied only
// to the REGISTRY shelf's classification — project-shelf paths are never
// read-denied at all under D10.3, regardless of leaf name.
func isSkillInstructionFileLeaf(rawPath string) bool {
	return skillInstructionFileLeaves[filepath.Base(rawPath)]
}

// isSkillInstructionFile applies isSkillInstructionFileLeaf to BOTH spellings
// of the path under judgement: as the caller wrote it, and fully resolved.
// See classifySkillsGate for why one spelling is not enough — an innocuously
// named symlink in the work dir has a leaf of "notes.md" and a realpath leaf
// of "SKILL.md".
func isSkillInstructionFile(rawPath, realAbs string) bool {
	if isSkillInstructionFileLeaf(rawPath) {
		return true
	}
	return realAbs != "" && isSkillInstructionFileLeaf(realAbs)
}

// registrySkillsRoot returns the install-wide skills directory
// ($OMNIPUS_HOME/skills), realpath-resolved when it already exists on disk.
// Computed fresh on every call (matches config.OmnipusHomeDir's own
// intentionally-not-memoised contract) so a test that changes $OMNIPUS_HOME
// mid-process is honoured immediately, and mirrors the exact
// filepath.Join(<home>, "skills") computation pkg/agent/context.go's
// globalSkillsDir uses, so this package's classification and the loader's
// own idea of "the registry shelf" can never point at two different paths.
func registrySkillsRoot() string {
	raw := filepath.Clean(filepath.Join(config.OmnipusHomeDir(), "skills"))
	if resolved, err := filepath.EvalSymlinks(raw); err == nil {
		return filepath.Clean(resolved)
	}
	return raw
}

// classifySkillsGatePath reports which shelf (if any) governs rawPath for
// D10's read gate and D6.1.1's write audit — "" / false when rawPath names
// no recognised skills location at all.
//
// The leaf (final path component) is deliberately left UNRESOLVED —
// resolveAncestorRealpath resolves only the ancestor chain, exactly as
// safeRelPath already relies on elsewhere in this file. This is required,
// not merely convenient: FR-078 requires the read gate to apply "the same
// real-path check" discovery already does, so a SKILL.md that is itself a
// symlink pointing OUTSIDE its shelf's root must still be refused as that
// shelf's file — classifying by where the symlink's TARGET resolves to
// would instead let it fall through to the open-read rule that governs
// wherever the target actually lives, silently defeating the gate for
// exactly the file FR-078 names.
//
// A rawPath whose ancestor chain does not exist at all (e.g. a brand-new
// path with no existing parent) is not classified — returns false. That is
// the safe direction for both callers: the read gate has nothing existing
// to refuse, and D6.1 already requires writes to stay ungated regardless.
func classifySkillsGatePath(rawPath string, policy fspolicy.FSPolicy) (shelf string, ok bool) {
	lexical, err := lexicalAbsPath(rawPath, policy.WorkDir)
	if err != nil {
		return "", false
	}
	resolvedDir, remainder, err := resolveAncestorRealpath(lexical)
	if err != nil {
		return "", false
	}
	return classifySkillsGateCandidate(filepath.Join(resolvedDir, remainder), policy)
}

// classifySkillsGateCandidate is classifySkillsGatePath's containment test,
// over an already-absolute candidate path.
//
// # Containment is judged by filesystem IDENTITY, not by comparing bytes
//
// fspolicy.CoversForDeny, never pkg/tools' own isWithinWorkspace (which is
// filepath.Rel + IsLocal, i.e. a byte comparison). This is the deny side of
// exactly the defect pkg/fspolicy/pathidentity.go's header records: on APFS —
// case-insensitive by default, and filepath.EvalSymlinks does NOT
// canonicalise case — $OMNIPUS_HOME/SKILLS/foo/SKILL.md and
// $OMNIPUS_HOME/skills/foo/SKILL.md are ONE file to the kernel and two
// different strings to filepath.Rel. That same byte comparison, applied to
// the secret set, was measured serving the live gateway bearer token and the
// master key on a real APFS volume through this very resolver. The gate here
// is now the app layer's only skills rule (see the block comment above), so
// it has to use the primitive that closes case, Unicode-normalization and
// symlink-spelling aliases rather than the one that does not.
//
// CoversForDeny falls back to a case-FOLDED comparison when the filesystem
// cannot answer (the container does not exist yet) — the fail-safe direction
// for a deny, and the reason this must not use CoversForGrant, whose fallback
// is byte-exact.
//
// Residual, stated rather than implied: a HARD LINK to a skill's instruction
// file, planted under a different name outside the skills root, is a distinct
// directory entry whose ancestor chain contains no skills-root inode, so
// neither identity nor bytes classify it. Creating one requires `link(2)` —
// no in-process tool offers it, so the only route is `bash`, which the kernel
// layer still denies the whole skills directory to on POSIX. It is therefore
// open only where ADR-072 D10.2 already states nothing is closed: Windows and
// any platform with no kernel backend.
func classifySkillsGateCandidate(candidate string, policy fspolicy.FSPolicy) (shelf string, ok bool) {
	if fspolicy.CoversForDeny(registrySkillsRoot(), candidate) {
		return skillShelfRegistry, true
	}

	for _, root := range policy.AllowedRoots {
		cleanRoot := filepath.Clean(root)
		if cleanRoot == "" || cleanRoot == "." {
			// A mount root that is empty or relative names no absolute
			// subtree; classifying against it could only produce a
			// meaningless comparison against the process's own cwd.
			continue
		}
		for _, sub := range skillsGateProjectSubdirs {
			if fspolicy.CoversForDeny(filepath.Join(cleanRoot, sub), candidate) {
				return skillShelfProject, true
			}
		}
	}
	return "", false
}

// classifySkillsGate judges BOTH spellings of the path a caller supplied: the
// path AS WRITTEN (ancestors resolved, leaf left alone) and its FULLY
// RESOLVED realpath. A match on either classifies.
//
// Both are required, and each covers what the other cannot:
//
//   - The path as written (FR-078) catches a skill's own instruction file
//     that is itself a symlink pointing OUT of its shelf. Classifying only
//     the resolved form would follow that symlink to wherever it lands and
//     let the file fall through to the open-read rule that governs the
//     target's location — defeating the gate for exactly the file FR-078
//     names.
//   - The resolved realpath catches the opposite direction: a symlink
//     planted anywhere the agent can write (its own work dir, a mount)
//     pointing AT a registry skill's instruction file. Its basename is
//     whatever the attacker chose and its ancestor chain is nowhere near the
//     skills root, so the as-written form classifies it as an ordinary file.
//     Until ADR-072 D10.3's app-layer split this direction was covered
//     incidentally by fspolicy.IsCarveOut, which tests the resolved path;
//     with `skills` out of the app carve-out list, closing it is this gate's
//     own job.
func classifySkillsGate(rawPath, realAbs string, policy fspolicy.FSPolicy) (shelf string, ok bool) {
	if shelf, ok := classifySkillsGatePath(rawPath, policy); ok {
		return shelf, true
	}
	if realAbs == "" {
		return "", false
	}
	return classifySkillsGateCandidate(filepath.Clean(realAbs), policy)
}

// skillsGateDenialError builds the typed refusal ResolvePath returns for a
// read/list/send of a registry skill's instruction file (D10.3's narrowed
// Part A). Only ever called with shelf == skillShelfRegistry — D10.3 removed
// Part B's project-shelf read denial entirely (see the doc comment above
// classifySkillsGatePath's declaration), and the caller below additionally
// requires isSkillInstructionFileLeaf(rawPath) before reaching this
// function, so a registry skill's bundled sibling files are never denied
// here either.
func skillsGateDenialError(rawPath string) error {
	return fmt.Errorf("%w: %q is an installed registry skill's instruction file — load it with the Skill tool, not a file tool (ADR-072 D10.3 Part A)",
		ErrCarveOut, rawPath)
}

// skillsRegistryNonReadDenialError refuses a WRITE, SERVE or EXEC whose path
// lands in the installed registry ($OMNIPUS_HOME/skills).
//
// D10.3 narrows the registry gate on the READ side only. Every other op kept
// the answer it already had: until D10.3 the whole registry was an
// fspolicy.IsCarveOut root, so ResolvePath refused every op under it
// unconditionally. Removing it from the app carve-out list (so bundled files
// could be read) would otherwise have opened writes as a side effect —
// narrowly but really, since a mount that CONTAINS $OMNIPUS_HOME is allowed
// with a warning (pkg/workspace/mount.go's CheckMountTarget refuses only a
// target inside it), and a write under a matched mount root is granted. An
// agent could then clobber another skill's instructions through write_file.
// Nothing in D10.3 asks for that, so it stays refused.
//
// This costs the sanctioned authoring path nothing: create_skill/edit_skill/
// install_skill write the registry through pkg/skills' own confined, raw I/O
// (see the lint allowlist entry for skills_install.go), never through
// ResolvePath — which is also why this deny is exactly behaviour-preserving
// rather than a new restriction.
func skillsRegistryNonReadDenialError(rawPath string, op FSOp) error {
	return fmt.Errorf("%w: %q is inside the installed skill registry ($OMNIPUS_HOME/skills), which no file tool may %s — use the skill authoring tools (ADR-072 D10/D10.3)",
		ErrCarveOut, rawPath, op)
}

// skillsWriteAuditFields is the FR-071a write-audit record content captured
// at ResolvePath time as plain strings rather than a stored
// context.Context — storing a context on a long-lived struct is poor Go
// practice regardless of whether a linter happens to catch it, and every
// field FR-071a requires (agent, workspace, tool) is string-shaped anyway,
// so there is nothing else ctx would buy here. Carried on the PathHandle so
// WriteFile can emit the audit AFTER the write actually succeeds (FR-071c:
// audit follows a real write, it does not merely follow permission to
// attempt one).
type skillsWriteAuditFields struct {
	shelf       string
	toolName    string
	agentID     string
	sessionID   string
	workspaceID string
}

// skillsWriteAuditMu / skillsWriteAuditLogger — a process-wide audit.Logger
// for the D6.1.1/FR-071 write hook, installed once via
// SetSkillsWriteAuditLogger. Mirrors pkg/audit/hmac.go's
// SetProcessChainKey/processChainKey pattern exactly, and for the identical
// reason: ResolvePath is a single free function reached from many different
// tool structs' Execute methods (filesystem.go, edit.go, the future
// authoring verbs, ...), each of which already carries its OWN per-tool
// *audit.Logger field (t.auditLogger, set via each tool's own
// SetAuditLogger) — threading a new parameter onto ResolvePath's fixed
// FR-003 signature to reach this one path-resolver-level hook would require
// editing every one of those call sites instead. A later integration phase
// wires this at gateway boot, alongside the other audit-logger wiring
// (mirrors this exact codebase's own precedent for "a later integration
// phase wires it up" — see pkg/skills/loader.go's three-shelf SkillsLoader).
// nil (this package's zero state, and every test binary that never calls
// the setter) makes the hook a no-op, matching emitFileWriteAudit's own
// "auditLog == nil is best-effort no-op" contract in path_audit.go — an
// install that has not wired this yet loses nothing it has today.
var (
	skillsWriteAuditMu     sync.RWMutex
	skillsWriteAuditLogger *audit.Logger
)

// SetSkillsWriteAuditLogger installs (or, with nil, clears) the process-wide
// audit.Logger ResolvePath's write hook uses. Safe to call multiple times
// (idempotent, mirrors SetProcessChainKey); the last caller wins.
func SetSkillsWriteAuditLogger(l *audit.Logger) {
	skillsWriteAuditMu.Lock()
	defer skillsWriteAuditMu.Unlock()
	skillsWriteAuditLogger = l
}

func getSkillsWriteAuditLogger() *audit.Logger {
	skillsWriteAuditMu.RLock()
	defer skillsWriteAuditMu.RUnlock()
	return skillsWriteAuditLogger
}

// SkillWriteAuditEvent is the audit event name for a D6.1.1/FR-071 write
// record. FR-018c states that FR-018's Skill-tool-CALL records (load/
// search, audited elsewhere by the phase implementing D3.1/FR-018 — not
// this file) and FR-071a's WRITE records are "two distinct record shapes
// under one audit event kind". That phase had not landed at the time this
// was written, so the shared kind name it will define is not yet known;
// this is defined locally so this package's write audit is not blocked on
// cross-package coordination. pkg/audit.IsValidEventName treats an
// unrecognized event name as a loud warn-once rather than a rejected entry
// (see its own doc comment) — the safe degrade if a later phase reconciles
// this under a different shared constant. Flagged in this task's final
// report as a point a later integration pass should reconcile.
const SkillWriteAuditEvent = "skill.write"

// emitSkillPathWriteAudit records ONE FR-071/FR-071a audit entry: a write
// whose resolved path landed under a recognised skills directory,
// whichever tool performed it. Best-effort — mirrors emitFileWriteAudit's
// contract in path_audit.go: a nil logger is a silent no-op, and a Log
// failure is itself logged (FR-071c) rather than returned, because the
// write it describes has ALREADY happened by the time this runs (see
// PathHandle.WriteFile) and must not be undone or fail the turn over an
// audit-append failure.
func emitSkillPathWriteAudit(fields skillsWriteAuditFields, resolvedPath string) {
	l := getSkillsWriteAuditLogger()
	if l == nil {
		return
	}
	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     SkillWriteAuditEvent,
		Decision:  audit.DecisionAllow,
		AgentID:   fields.agentID,
		SessionID: fields.sessionID,
		Tool:      fields.toolName,
		Details: map[string]any{
			"shelf":        fields.shelf,
			"path":         resolvedPath,
			"workspace_id": fields.workspaceID,
			"op":           "write",
		},
	}
	if err := l.Log(entry); err != nil {
		logger.WarnCF("resolvepath", "skills write audit log write failed",
			map[string]any{"error": err.Error(), "agent_id": fields.agentID, "path": resolvedPath})
	}
}

// PathHandle is the sanctioned I/O handle ResolvePath returns. root is nil
// exactly when the resolved path was granted under fspolicy.FSScopeUnrestricted
// via the legacy host-fs back-compat path (an absolute, or escaping, path that
// the effective scope permits); in that case abs is the resolved absolute
// path and every method operates on it directly via the plain os package,
// matching the pre-ADR-046 unrestricted ("hostFs") behavior. When root is
// non-nil, every method resolves rel underneath it via os.Root, so an
// attacker who swaps a path component between ResolvePath's own realpath
// check and the actual I/O call cannot escape the root — the kernel/runtime
// re-resolves and re-enforces containment at the moment of the syscall, not
// merely at a prior string check (FR-006's TOCTOU-hardness).
//
// policy is carried on the handle so the root==nil (host-fs) branch of every
// I/O method below can re-verify FR-017's carve-out protection AT I/O TIME,
// not merely at the earlier ResolvePath resolve — see
// recheckUnrestrictedCarveOut (HIGH #3, ADR-046 P1 review). The root!=nil
// (confined) branch never consults it — os.Root's own re-resolution already
// closes that TOCTOU gap for the confined case, and IsCarveOut was already
// checked unconditionally by ResolvePath before the handle was constructed.
type PathHandle struct {
	root   *os.Root
	rel    string
	abs    string
	policy fspolicy.FSPolicy

	// skillsWriteAudit is non-nil exactly when ResolvePath classified this
	// handle's target as a recognised skills-directory path (D10) AND the
	// requested op was a write (FSOpWrite/FSOpServe) — see
	// classifySkillsGatePath. WriteFile below emits the D6.1.1/FR-071 audit
	// record from it AFTER the write actually succeeds, never before.
	skillsWriteAudit *skillsWriteAuditFields
}

// recheckUnrestrictedCarveOut re-resolves h.abs (following any symlink that
// may have been swapped in since ResolvePath first resolved it) and re-runs
// fspolicy.IsCarveOut against it, refusing with ErrCarveOut if the target now
// falls under a carve-out root. Only meaningful — and only called — on a
// host-fs (root==nil) handle: os.Root-backed (confined) I/O already
// re-resolves and re-enforces containment at the syscall boundary on every
// call, but a root==nil handle's methods do raw os.* I/O directly against
// h.abs, a string that was resolved once, at ResolvePath time, and never
// re-checked again before this fix (HIGH #3, ADR-046 P1 review) — exactly
// the CWE-357 TOCTOU shape ResolvePath's package doc otherwise claims to
// close. This does not close the general host-fs TOCTOU (a swap between
// this re-check and the os.* call immediately below it is still possible;
// that residual is inherent to string-based host filesystem I/O and is
// P3's job, via a per-child Landlock ruleset, to close for real) — it
// specifically re-verifies the ONE property FR-017 requires unconditionally
// regardless of scope: an agent must never reach master.key/
// credentials.json/another agent's home/another workspace, even under
// FSScopeUnrestricted.
func (h *PathHandle) recheckUnrestrictedCarveOut() error {
	current, err := resolveRealpathUnderWorkDir(h.abs, "")
	if err != nil {
		return fmt.Errorf("resolvepath: re-resolve %q at I/O time: %w", h.abs, err)
	}
	if fspolicy.IsCarveOut(current, h.policy) {
		return ErrCarveOut
	}
	return nil
}

// ReadFile reads the handle's target in full.
func (h *PathHandle) ReadFile() ([]byte, error) {
	if h.root == nil {
		if err := h.recheckUnrestrictedCarveOut(); err != nil {
			return nil, err
		}
		content, err := os.ReadFile(h.abs)
		if err != nil {
			return nil, wrapReadErr(err)
		}
		return content, nil
	}
	content, err := h.root.ReadFile(h.rel)
	if err != nil {
		return nil, wrapReadErr(err)
	}
	return content, nil
}

// WriteFile writes data to the handle's target atomically (temp file +
// fsync + rename), mirroring the exact atomic-write body the pre-ADR-046
// sandboxFs.WriteFile used for the confined (root-backed) case, and
// fileutil.WriteFileAtomic's contract for the unrestricted (host) case.
func (h *PathHandle) WriteFile(data []byte) error {
	if h.root == nil {
		if err := h.recheckUnrestrictedCarveOut(); err != nil {
			return err
		}
		if err := writeFileAtomicHost(h.abs, data); err != nil {
			return err
		}
		h.emitSkillsWriteAuditIfNeeded()
		return nil
	}
	if err := writeFileAtomicRoot(h.root, h.rel, data); err != nil {
		return err
	}
	h.emitSkillsWriteAuditIfNeeded()
	return nil
}

// emitSkillsWriteAuditIfNeeded fires the D6.1.1/FR-071 write audit when this
// handle was classified as a recognised skills-directory write target. Only
// ever called from WriteFile, after the write has already succeeded
// (FR-071c) — never from ResolvePath itself, which only decides whether the
// write is PERMITTED, not whether it happened.
func (h *PathHandle) emitSkillsWriteAuditIfNeeded() {
	if h == nil || h.skillsWriteAudit == nil {
		return
	}
	emitSkillPathWriteAudit(*h.skillsWriteAudit, h.abs)
}

// ReadDir lists the handle's target directory.
func (h *PathHandle) ReadDir() ([]os.DirEntry, error) {
	if h.root == nil {
		if err := h.recheckUnrestrictedCarveOut(); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(h.abs)
		if err != nil {
			return nil, fmt.Errorf("failed to read directory: %w", err)
		}
		return entries, nil
	}
	entries, err := fs.ReadDir(h.root.FS(), h.rel)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}
	return entries, nil
}

// Open opens the handle's target for reading. The returned fs.File remains
// valid even after h.Close() runs (os.Root's documented guarantee, already
// relied on by the pre-ADR-046 sandboxFs.Open) — callers may safely
// `defer handle.Close()` alongside `defer file.Close()` in either order.
func (h *PathHandle) Open() (fs.File, error) {
	if h.root == nil {
		if err := h.recheckUnrestrictedCarveOut(); err != nil {
			return nil, err
		}
		f, err := os.Open(h.abs)
		if err != nil {
			return nil, wrapOpenErr(err)
		}
		return f, nil
	}
	f, err := h.root.Open(h.rel)
	if err != nil {
		return nil, wrapOpenErr(err)
	}
	return f, nil
}

// MkdirAll creates the handle's target directory (and any missing parents).
func (h *PathHandle) MkdirAll(perm os.FileMode) error {
	if h.root == nil {
		if err := h.recheckUnrestrictedCarveOut(); err != nil {
			return err
		}
		if err := os.MkdirAll(h.abs, perm); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
		return nil
	}
	if err := h.root.MkdirAll(h.rel, perm); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	return nil
}

// Stat returns the handle's target FileInfo.
func (h *PathHandle) Stat() (os.FileInfo, error) {
	if h.root == nil {
		if err := h.recheckUnrestrictedCarveOut(); err != nil {
			return nil, err
		}
		info, err := os.Stat(h.abs)
		if err != nil {
			return nil, fmt.Errorf("failed to stat: %w", err)
		}
		return info, nil
	}
	info, err := h.root.Stat(h.rel)
	if err != nil {
		return nil, fmt.Errorf("failed to stat: %w", err)
	}
	return info, nil
}

// RealPath returns the resolved absolute path ResolvePath computed. This is
// the ONE documented exception to "never hand back a bare string" — it
// exists solely for the OS/library-boundary call sites that have no handle
// parameter to accept (exec.Cmd.Dir, and web_serve's http.Dir directory
// registration in the follow-up defects wave). Treat it as ADVISORY ONLY:
// nothing re-checks confinement between this call and the consumer's own use
// of the string, so it must never be treated as a TOCTOU-safe substitute for
// the handle's own I/O methods above. P3 (ADR-046) closes this gap for exec
// by feeding EffectiveFSPolicy into a per-child Landlock ruleset instead of
// relying on this string.
func (h *PathHandle) RealPath() (string, error) {
	if h == nil {
		return "", fmt.Errorf("resolvepath: nil path handle")
	}
	return h.abs, nil
}

// Close releases the handle's os.Root, when it holds one. Safe to call on a
// host-mode handle (root == nil) or a nil handle — both are no-ops.
func (h *PathHandle) Close() error {
	if h == nil || h.root == nil {
		return nil
	}
	return h.root.Close()
}

// ResolvePath is the single, mandatory path-resolution chokepoint (FR-003).
// rawPath is the caller-supplied (LLM/tool-argument) path; policy is the
// turn's single source-of-record FSPolicy (fspolicy.EffectiveFSPolicy).
// toolName and callID are threaded through for the P2 ask-flow and the
// FR-035 audit dimension but drive NO decision in this implementation. op
// DOES drive a decision as of ADR-063 / spec unified-file-access-and-mounts
// FR-2 — see FSOp's doc comment and step 3 below.
//
// Resolution order:
//  0. Reject a zero-value or otherwise unrecognized op with ErrPathInvalid,
//     before any other check (FR-2.4) — every real call site already passes
//     one of the named FSOp constants, so an empty op reaching here is a
//     caller bug and must fail loudly rather than silently taking whichever
//     branch a switch's default case happens to reach.
//     0a. Validate policy's own structural invariants (BLOCK #1, ADR-046 P1
//     review) — a zero-value or otherwise malformed FSPolicy (e.g. an empty
//     WorkDir, or a WorkDir sitting at/above one of its own CarveOuts) is
//     refused with ErrPathInvalid before any access decision is made.
//  1. Reject an embedded NUL byte or any hard (non-"not exist") resolution
//     failure with ErrPathInvalid — before any policy decision.
//  2. Check fspolicy.IsCarveOut on the resolved realpath, UNCONDITIONALLY —
//     regardless of op or scope, an agent must never reach
//     master.key/credentials.json/another agent's home/another workspace
//     (FR-017).
//  3. If the resolved realpath falls outside policy.WorkDir (a leading ".."
//     escape, a mid-string ".." reentry, an absolute path elsewhere, or a
//     symlink that resolves outside), dispatch on op (FR-2.2):
//     - FSOpRead, FSOpList, FSOpSend: allowed anywhere except the secret
//     set already refused at step 2 — a legacy host-fs PathHandle
//     (root==nil) is returned, independent of policy.Scope.
//     - FSOpWrite, FSOpServe: allowed only when the realpath also falls
//     within one of policy.AllowedRoots (a workspace mount) — refused
//     with ErrOutsideScope otherwise, independent of policy.Scope. This
//     is the operation-aware split from the pre-FR-2 behaviour, where
//     Scope alone decided every op identically.
//     - FSOpExec: unchanged from the pre-FR-2 behaviour (per the ADR-062
//     kernel model) — dispatches on policy.Scope exactly as every op
//     used to: fspolicy.FSScopeUnrestricted returns a host-fs handle;
//     fspolicy.FSScopeConfined refuses with ErrOutsideScope;
//     fspolicy.FSScopeAsk/FSScopeAllow are P2 seams this package does
//     not implement yet, so they are refused with ErrPathInvalid rather
//     than silently falling through to some invented default
//     (Constraint #6).
//  4. Otherwise (the realpath falls within policy.WorkDir — including an
//     absolute path that simply happens to resolve inside it, matching the
//     pre-ADR-046 sandboxFs/getSafeRelPath contract every existing
//     path-taking tool test relies on) the path is resolved through a fresh
//     os.Root opened at policy.WorkDir, and every subsequent I/O call on the
//     returned handle is enforced at the syscall boundary (FR-006).
func ResolvePath(
	ctx context.Context,
	policy fspolicy.FSPolicy,
	toolName, callID string,
	op FSOp,
	rawPath string,
) (*PathHandle, error) {
	// ctx and toolName are now consumed below by the ADR-072 D10/D6.1.1
	// skills-gate check (ToolAgentID/ToolTranscriptSessionID/ToolWorkspaceID
	// read ctx; toolName is carried onto a write handle's audit fields).
	// callID remains a P2 seam only — the ask-flow will consult it once
	// filesystem_scope=ask lands. Referenced here only to make the current
	// no-op deliberate rather than silently unused.
	_ = callID

	switch op {
	case FSOpRead, FSOpList, FSOpSend, FSOpWrite, FSOpExec, FSOpServe:
		// known, explicit op — proceed.
	default:
		// FR-2.4: the zero value (and any unrecognized string) is refused
		// loudly rather than defaulted. Every production call site already
		// passes one of the named constants above.
		return nil, fmt.Errorf("%w: FSOp %q is invalid — ResolvePath requires an explicit, known FSOp (the zero value is refused, not defaulted)",
			ErrPathInvalid, op)
	}

	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPathInvalid, err)
	}

	// realWorkDir is policy.WorkDir resolved through the exact same
	// symlink-following, walk-up-on-not-exist logic used for realAbs below
	// (HIGH, macOS CI: pkg/gateway matrix job, PR #597). fspolicy.FSPolicy's
	// own doc comment documents WorkDir as already "the realpath (symlinks
	// resolved)", and fspolicy.EffectiveFSPolicy — the sole constructor every
	// real production caller reaches ResolvePath through — upholds that by
	// calling its own realpath() before returning. But ResolvePath is FR-003's
	// single MANDATORY chokepoint, not a function entitled to blindly trust an
	// invariant it cannot itself enforce: fspolicy.FSPolicy.Validate's own doc
	// comment carves out "the direct-construction shape several resolver-level
	// unit tests use", i.e. constructing an FSPolicy{WorkDir: ...} by hand
	// without going through EffectiveFSPolicy — and on macOS, t.TempDir()
	// returns a path under /var, itself a symlink to /private/var. Without
	// this, realAbs (always resolved, a few lines below) and an unresolved
	// policy.WorkDir never share a prefix, and isWithinWorkspace falsely
	// rejects every legitimate in-workspace path as escaping scope. Resolving
	// unconditionally here is idempotent (a no-op) when the caller already
	// resolved WorkDir, and closes the gap when it didn't.
	realWorkDir, err := resolveRealpathUnderWorkDir(policy.WorkDir, "")
	if err != nil {
		return nil, fmt.Errorf("%w: resolve working directory %q: %w", ErrPathInvalid, policy.WorkDir, err)
	}

	realAbs, err := resolveRealpathUnderWorkDir(rawPath, policy.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPathInvalid, err)
	}

	if fspolicy.IsCarveOut(realAbs, policy) {
		return nil, ErrCarveOut
	}

	// ADR-072 D10.3 (read gate, narrowed) / D6.1.1 (write audit): classify the
	// path against the two recognised skills locations — the installed
	// registry and any mount's project-skills subdirectory — BEFORE the
	// ordinary in-workspace/outside-workspace dispatch below, so the gate
	// applies uniformly regardless of which branch would otherwise handle
	// this op and regardless of policy.Scope (matching the carve-out check
	// just above, which is itself unconditional for the same reason).
	//
	// The carve-out check above does NOT cover the registry skills directory
	// (ADR-072 D10.3 removed it from the app layer's carve-out roots so this
	// finer gate could exist at all — see the block comment on
	// skillsGateProjectSubdirs). This is the whole app-layer rule for skills;
	// nothing underneath it will catch what it misses.
	//
	// D10.3: only a REGISTRY-shelf path whose leaf is a recognised
	// instruction filename is refused for a read/list/send (FR-057/061a-c) —
	// a bundled sibling file, and any project-shelf path at all (D4.1 already
	// makes the mount the grant; Part B's directory-wide project deny is
	// removed, it protected nothing), are left to the ordinary open-read rule
	// below. A write is left to proceed exactly as it otherwise would (D6.1:
	// writes are not gated by the read gate) but carries the classification
	// forward onto the returned handle so WriteFile can audit it once the
	// write actually succeeds (FR-071/071a/071c) — write auditing still
	// applies to BOTH shelves and to every file in a classified directory,
	// not just instruction files, since D6.1.1's audit trail is about who
	// wrote into a recognised skills location, not what they wrote.
	var skillsWriteAudit *skillsWriteAuditFields
	if shelf, matched := classifySkillsGate(rawPath, realAbs, policy); matched {
		switch op {
		case FSOpRead, FSOpList, FSOpSend:
			if shelf == skillShelfRegistry && isSkillInstructionFile(rawPath, realAbs) {
				return nil, skillsGateDenialError(rawPath)
			}
		case FSOpWrite, FSOpServe:
			if shelf == skillShelfRegistry {
				return nil, skillsRegistryNonReadDenialError(rawPath, op)
			}
			skillsWriteAudit = &skillsWriteAuditFields{
				shelf:       shelf,
				toolName:    toolName,
				agentID:     ToolAgentID(ctx),
				sessionID:   ToolTranscriptSessionID(ctx),
				workspaceID: ToolWorkspaceID(ctx),
			}
		case FSOpExec:
			if shelf == skillShelfRegistry {
				return nil, skillsRegistryNonReadDenialError(rawPath, op)
			}
		}
	}

	if !isWithinWorkspace(realAbs, realWorkDir) {
		// FR-2.2: the decision now depends on op, not solely on
		// policy.Scope. fspolicy.IsCarveOut has already refused the secret
		// set unconditionally above (step 2), before op is ever consulted.
		switch op {
		case FSOpRead, FSOpList, FSOpSend:
			// Reads (and sends — FR-2.3/FR-2.3a: send_file follows the open-
			// read rule, governed by tool policy rather than a path
			// restriction) are allowed anywhere outside the secret set,
			// independent of policy.Scope.
			return &PathHandle{abs: realAbs, policy: policy}, nil

		case FSOpWrite, FSOpServe:
			// Writes, and web_serve (FR-2.3b: preserved exactly, not a new
			// rule — web_serve already hardcodes restrict=true/
			// FSScopeConfined at every call site, so this produces the same
			// outcome today; it also now honors a workspace mount, which
			// Scope-based dispatch never could), are confined to the work
			// dir or an explicitly granted mount — never open, regardless of
			// policy.Scope.
			//
			// The handle returned here MUST be os.Root-backed, anchored at the
			// matched mount root itself — never a host-fs (root==nil) handle
			// carrying realAbs as a bare string. A mount's contents belong to
			// whoever the operator pointed the mount at, which can be the SAME
			// agent turn that is about to write through it: nothing stops that
			// turn from swapping an ANCESTOR directory under the mount (e.g.
			// turning a subdirectory it already created into a symlink that
			// escapes every granted root) between this resolve and the actual
			// write. A host-fs handle's recheckUnrestrictedCarveOut only
			// re-verifies the secret set at I/O time, not containment in
			// policy.AllowedRoots — so that swap would let the write land
			// outside the mount entirely, the exact TOCTOU FR-006 exists to
			// close for policy.WorkDir. Anchoring an os.Root at the mount root
			// gives the write the identical protection WorkDir writes already
			// get: the ancestor chain is re-resolved fresh, confined to the
			// root, at the moment of the syscall — not merely at this earlier
			// string check. See matchedAllowedRoot's doc comment for the
			// remaining, narrower residual this does not close.
			if root, ok := matchedAllowedRoot(realAbs, policy.AllowedRoots); ok {
				handle, mountErr := newMountRootHandle(root, rawPath, realAbs, policy)
				if mountErr != nil {
					return nil, mountErr
				}
				handle.skillsWriteAudit = skillsWriteAudit
				return handle, nil
			}
			return nil, fmt.Errorf("%w: %q resolves to %q, outside the effective working directory %q and no mount covers it",
				ErrOutsideScope, rawPath, realAbs, policy.WorkDir)

		case FSOpExec:
			// Unchanged from the pre-FR-2 behaviour (per the ADR-062 kernel
			// model): every op used to dispatch on Scope alone, and exec
			// stays on that exact path.
			switch policy.Scope {
			case fspolicy.FSScopeConfined:
				return nil, fmt.Errorf("%w: %q resolves to %q, outside the effective working directory %q",
					ErrOutsideScope, rawPath, realAbs, policy.WorkDir)
			case fspolicy.FSScopeUnrestricted:
				return &PathHandle{abs: realAbs, policy: policy}, nil
			case fspolicy.FSScopeAsk, fspolicy.FSScopeAllow:
				return nil, fmt.Errorf("%w: filesystem_scope %q is not yet supported by ResolvePath (P2)",
					ErrPathInvalid, policy.Scope)
			default:
				return nil, fmt.Errorf("resolvepath: internal error: unknown filesystem scope %q", policy.Scope)
			}

		default:
			// Unreachable: op was already validated to be one of the six
			// known constants above. Kept as defense-in-depth so a future
			// FSOp addition that forgets to extend this switch fails loudly
			// instead of silently taking an unintended branch.
			return nil, fmt.Errorf("%w: FSOp %q has no ResolvePath decision rule", ErrPathInvalid, op)
		}
	}

	// rel is computed against realWorkDir (resolved), not the possibly-
	// unresolved policy.WorkDir — the second half of the same fix: even when
	// policy.WorkDir already arrives pre-resolved (production's normal
	// EffectiveFSPolicy path), rawPath is whatever spelling the caller
	// supplied and is NEVER resolved by ResolvePath itself — an absolute
	// rawPath built from the pre-resolution spelling of the workspace (e.g.
	// under /var on macOS) would trip the exact same false "escapes the
	// working directory" rejection this fix closes for isWithinWorkspace
	// above.
	//
	// Critically, rawPath itself (not realAbs) is still what safeRelPath
	// resolves against: safeRelPath only ever resolves rawPath's ANCESTOR
	// (dirname) chain, via resolveAncestorRealpath, never its final
	// component. Passing realAbs here instead — which resolveRealpathUnderWorkDir
	// resolves in full, following a symlink AT THE LEAF too — was tried and
	// reverted: it collapsed a leaf like "toctou_link" to whatever it
	// happened to point at when ResolvePath ran, baking that target into rel
	// and handing os.Root a path that no longer even names the symlink,
	// which silently defeats FR-006's TOCTOU protection (proven by
	// TestResolvePath_IOThroughOsRoot_NoTOCTOU: the swapped-symlink read
	// started succeeding instead of being refused fresh at I/O time). Only
	// the ancestor prefix needs normalizing to fix the macOS bug; the leaf
	// must stay exactly as the caller spelled it so os.Root re-resolves it
	// fresh at I/O time, symlink swap and all.
	//
	// realAbs == realWorkDir is handled separately, short-circuiting straight
	// to rel="." rather than through safeRelPath's ancestor-preserving logic:
	// when rawPath names the workspace root itself (e.g. list_directory
	// called with no path, or with the root's own — possibly unresolved —
	// spelling), there IS no leaf below the root to protect, but
	// resolveAncestorRealpath unconditionally treats rawPath's OWN final
	// path segment as one, walking one directory too far up (proven by
	// TestFilesystemTool_ListDir_Success, which passes the workspace root as
	// "path" and, without this, was rejected as "../<root's own name>
	// escapes the working directory" even with no symlink involved at all).
	var rel string
	if realAbs == realWorkDir {
		rel = "."
	} else {
		rel, err = safeRelPath(realWorkDir, rawPath)
		if err != nil {
			return nil, err
		}
	}

	root, err := os.OpenRoot(policy.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("resolvepath: open working directory root %q: %w", policy.WorkDir, err)
	}

	return &PathHandle{root: root, rel: rel, abs: realAbs, policy: policy, skillsWriteAudit: skillsWriteAudit}, nil
}

// matchedAllowedRoot reports whether candidate falls on or under any of roots
// (FR-2.2/FR-6.1: a workspace mount grants write access to its host_path),
// and returns WHICH root matched. candidate MUST already be realpath-resolved
// by the caller — this performs no I/O of its own.
//
// This is LIVE, not a placeholder: ResolveTurnFSPolicy populates
// policy.AllowedRoots from workspace.AllowedMountRoots (ADR-063 FR-6.1), so
// every write or serve inside a mounted host directory is admitted here and
// nowhere else. The comment that used to sit here said the opposite — that
// AllowedRoots was "always nil in P1" and this was "a no-op for every
// production caller" — which stopped being true when mounts landed. Anyone
// deciding whether this path needs validation should read it as load-bearing.
//
// Reuses isWithinWorkspace (filesystem.go) — the exact same containment test
// ResolvePath already applies to policy.WorkDir itself — for each candidate
// root, rather than a second, possibly-drifting implementation.
//
// Surfacing the SPECIFIC matched root (rather than a bool, which is all the
// pre-fix isWithinAnyRoot returned) is what lets ResolvePath's FSOpWrite/
// FSOpServe branch anchor an os.Root there instead of handing back a bare
// abs string — see newMountRootHandle, and its call site's comment, for why
// that anchoring is the fix for a real TOCTOU rather than cosmetic.
func matchedAllowedRoot(candidate string, roots []string) (string, bool) {
	for _, root := range roots {
		if isWithinWorkspace(candidate, root) {
			return root, true
		}
	}
	return "", false
}

// newMountRootHandle builds the os.Root-backed PathHandle for a write/serve
// that resolved OUTSIDE policy.WorkDir but inside mountRoot (one entry of
// policy.AllowedRoots, already realpath-resolved at mount-CREATE time — see
// workspace.Mount.HostPath's own doc comment).
//
// # Why this cannot just return &PathHandle{abs: realAbs, policy: policy}
//
// That was the pre-fix shape, and it is exactly the CWE-357 TOCTOU
// resolvepath.go's own package doc claims to close everywhere: a root==nil
// handle's I/O methods operate on h.abs, a STRING resolved once here and
// never re-resolved before the actual syscall (recheckUnrestrictedCarveOut
// re-verifies only the secret set at I/O time, not containment in
// AllowedRoots). Nothing stops the same turn that is about to write from
// swapping an ancestor directory under mountRoot — say turning
// "<mountRoot>/002/sub" from a real directory into a symlink pointing
// anywhere on the host — between this function returning and the caller's
// next WriteFile/MkdirAll call. The attacker legitimately owns the mount's
// contents (it is their own repository), so driving both sides of that race
// from one turn needs no privilege escalation at all.
//
// # Why anchoring an os.Root here closes it
//
// This is the SAME mechanism policy.WorkDir writes already get (the bottom
// of ResolvePath, above): the returned handle's rel is computed against
// mountRoot via safeRelPath — ancestors resolved now, for the escaping-
// symlink DETECTION this function itself needs to do below, but the LEAF
// left exactly as the caller spelled it, so os.Root re-resolves the whole
// relative chain fresh, confined to mountRoot, at the moment of the actual
// syscall (FR-006). A swapped ancestor is then caught by the kernel/runtime
// at I/O time, not merely by an earlier string check.
//
// # Residual race — what this does NOT close
//
// os.Root's re-resolution happens once, at the moment of each I/O call — a
// swap landing between os.Root's internal walk and the underlying syscall it
// issues is not eliminated by anything short of kernel-level confinement
// (Landlock/Seatbelt on the spawned-child path, not this app-layer resolver).
// That residual is identical in kind to the one policy.WorkDir writes already
// carry (see PathHandle's own doc comment) — this fix brings mount writes up
// to the SAME level WorkDir writes already had, it does not invent a stronger
// guarantee than WorkDir gets. What it removes is the much larger, much more
// practical window this function's doc above describes: an unbounded amount
// of time between ResolvePath returning a handle and the caller's own,
// arbitrarily-later WriteFile/MkdirAll call — not just the width of one
// syscall.
func newMountRootHandle(mountRoot, rawPath, realAbs string, policy fspolicy.FSPolicy) (*PathHandle, error) {
	lexicalAbs, err := lexicalAbsPath(rawPath, policy.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPathInvalid, err)
	}

	var rel string
	if realAbs == mountRoot {
		rel = "."
	} else {
		// lexicalAbs is always absolute (lexicalAbsPath's contract), so
		// safeRelPath always takes its "absolute rawPath" branch here — the
		// same resolveAncestorRealpath-based, leaf-preserving computation the
		// policy.WorkDir case above uses, just anchored at mountRoot instead.
		rel, err = safeRelPath(mountRoot, lexicalAbs)
		if err != nil {
			return nil, err
		}
	}

	// os.OpenRoot needs a DIRECTORY. A workspace mount always is one, but
	// AllowedRoots also carries the single-path grants
	// ResolvePathAllowingPatterns injects for an operator's AllowWritePaths
	// regex — and those name a FILE, which may not exist yet (the whole point
	// of a write). Opening that as a root fails with ENOENT and the write is
	// refused with a message about a "mount root" the operator never
	// configured.
	//
	// Anchor at the nearest existing directory instead and re-derive rel from
	// there. This does not widen anything: containment was already decided by
	// matchedAllowedRoot against the granted root, and the handle can only ever
	// address the single rel path computed here. os.Root's escape protection
	// still applies from the anchor.
	anchor := mountRoot
	if fi, statErr := os.Stat(anchor); statErr != nil || !fi.IsDir() {
		// Walk up to the deepest ancestor that EXISTS and is a directory. The
		// grant may name a file several levels below the last existing
		// directory (write_file creates intermediate dirs), so stopping at the
		// immediate parent is not enough.
		anchor = filepath.Dir(mountRoot)
		for anchor != "/" && anchor != "." {
			if fi, statErr := os.Stat(anchor); statErr == nil && fi.IsDir() {
				break
			}
			anchor = filepath.Dir(anchor)
		}
		rel, err = safeRelPath(anchor, lexicalAbs)
		if err != nil {
			return nil, err
		}
	}

	root, err := os.OpenRoot(anchor)
	if err != nil {
		return nil, fmt.Errorf("resolvepath: open mount root %q: %w", anchor, err)
	}

	return &PathHandle{root: root, rel: rel, abs: realAbs, policy: policy}, nil
}

// ResolveTurnFSPolicy resolves the single, authoritative FSPolicy for a turn
// (FR-036), using the exact parameter shape every generic path-taking tool's
// Execute method previously assembled by hand (MEDIUM #7, ADR-046 P1
// review): WorkDir prefers the per-turn Workspace re-root (TurnWorkspaceDir)
// when the agent is a Workspace CoreTeam member, else falls back to
// agentHome; agent/workspace identity and $OMNIPUS_HOME come from ctx and
// config.OmnipusHomeDir() respectively. Centralizing this removes what were
// 9 hand-duplicated call sites (filesystem.go x3, edit.go x2, send_file.go,
// web_serve.go x2, browser/tools.go) — each a chance for the shape to drift.
func ResolveTurnFSPolicy(ctx context.Context, agentHome string, restrict bool) (fspolicy.FSPolicy, error) {
	home := config.OmnipusHomeDir()
	workspaceID := ToolWorkspaceID(ctx)

	policy, err := fspolicy.EffectiveFSPolicy(
		ctx, agentHome, TurnWorkspaceDir(ctx), restrict,
		home, ToolAgentID(ctx), workspaceID,
	)
	if err != nil {
		return policy, err
	}

	// ADR-063 FR-6.1: the workspace's mounts become the turn's additional
	// WRITE roots. Reads need no grant post-FR-2, so a mount grants write and
	// nothing else — see ADR-063 D4.
	//
	// This is the one place it can be done. Every path-taking tool routes
	// through this function (9 call sites), so populating AllowedRoots here
	// reaches all of them at once; doing it per tool is exactly the
	// hand-duplication this function was created to remove.
	//
	// It CANNOT reopen a secret. IsCarveOut and DeniedPathsFor take no
	// AllowedRoots parameter at all, and ResolvePath consults the carve-out
	// before it ever looks at a mount — so mounting even $HOME yields "write to
	// $HOME minus the secret set". That independence is asserted structurally
	// in pkg/fspolicy/mount_secret_independence_test.go rather than by example,
	// because it is what makes the operator's warn-and-allow decision safe.
	//
	// A workspace with no mounts yields nil, which is the pre-mount behaviour
	// exactly.
	//
	// Mounts must follow the work dir, not the turn-carried workspace_id.
	// EffectiveFSPolicy's WorkDir above already prefers TurnWorkspaceDir(ctx)
	// — the CoreTeam-resolved re-root pkg/agent/workspace_reroot.go's
	// resolveTurnWorkDirOrRefuse computes via
	// workspace.FindForAgentPreferring(home, agentID, optWorkspaceID) — over
	// agentHome, regardless of whether this turn carries an explicit
	// workspace_id at all. A CLI/ProcessDirect turn (`omnipus <agent> "..."`)
	// and a scheduled/heartbeat turn never set tools.WithWorkspaceID (that is
	// deliberate — see workspace_reroot.go's "does NOT touch
	// tools.WithWorkspaceID/memory-room routing (FR-030)" note), so
	// ToolWorkspaceID(ctx) is empty even though the work dir was re-rooted
	// into a CoreTeam workspace. Looking mounts up only by ToolWorkspaceID
	// then silently drops every mount the operator granted on that
	// workspace: the agent gets the work dir but not the write grants that
	// go with it, and a write into a mounted folder is refused with "no
	// mount covers it" despite the mount existing.
	//
	// Resolve the SAME way the work dir was resolved when workspaceID is
	// empty, so the two never disagree about which workspace this turn is
	// rooted in. This does not change behaviour when workspaceID is already
	// set — FindForAgentPreferring is consulted only on the empty path — and
	// it does not set tools.WithWorkspaceID or otherwise touch memory-room
	// routing; it only decides which workspace's mounts apply here.
	mountWorkspaceID := workspaceID
	if mountWorkspaceID == "" {
		if wsID, found := workspace.FindForAgentPreferring(home, ToolAgentID(ctx), workspaceID); found {
			mountWorkspaceID = wsID
		}
	}
	if mountWorkspaceID != "" {
		policy.AllowedRoots = workspace.AllowedMountRoots(home, mountWorkspaceID)
	}
	return policy, nil
}

// ResolvePathAllowingPatterns bridges the operator-configured AllowRead/
// WritePaths regex axis (isAllowedPath/matchesAllowedPath in filesystem.go —
// a feature orthogonal to filesystem_scope; ADR-046 does not touch it,
// "only rewire its callers") onto ResolvePath, which has no patterns
// parameter of its own by design (FR-003's signature is fixed).
//
// When rawPath (resolved the SAME lexical way validatePathWithAllowPaths did
// — a plain absolute join, deliberately NOT symlink-resolved, so a
// pattern anchored on a symlink alias like allowedDir still matches before
// resolution) matches one of patterns, this re-resolves through ResolvePath
// with the scope forced to fspolicy.FSScopeUnrestricted for this single
// call — mirroring the pre-ADR-046 whitelistFs's "allow-listed path bypasses
// workspace confinement" behavior. Critically, FR-017's carve-out check
// still runs unconditionally either way, because ResolvePath always checks
// it first regardless of scope — an improvement over whitelistFs, which
// bypassed it entirely.
func ResolvePathAllowingPatterns(
	ctx context.Context,
	policy fspolicy.FSPolicy,
	toolName, callID string,
	op FSOp,
	rawPath string,
	patterns []*regexp.Regexp,
) (*PathHandle, error) {
	if len(patterns) > 0 {
		lexicalAbs := rawPath
		if !filepath.IsAbs(lexicalAbs) {
			lexicalAbs = filepath.Join(policy.WorkDir, lexicalAbs)
		}
		lexicalAbs = filepath.Clean(lexicalAbs)
		if isAllowedPath(lexicalAbs, patterns) {
			granted := policy
			granted.Scope = fspolicy.FSScopeUnrestricted

			// FR-2.2 (ADR-063): FSOpWrite/FSOpServe no longer become open by
			// Scope alone (see ResolvePath's outside-WorkDir switch) — Scope
			// forcing above only still matters for FSOpRead/List/Send/Exec,
			// which is why it is kept, not removed. This regex allow-list
			// axis predates FR-2 and is orthogonal to filesystem_scope (see
			// this function's own doc comment above); to keep granting a
			// write/serve the operator already vetted via an AllowWritePaths
			// pattern, resolve rawPath the SAME way ResolvePath itself is
			// about to (resolveRealpathUnderWorkDir, the identical function,
			// same inputs -> identical output) and add that ONE resolved
			// location to a call-scoped copy of AllowedRoots. This grants
			// exactly the path already vetted by isAllowedPath above — never
			// a wider Unrestricted-for-writes reopening, which would defeat
			// FR-2.2/FR-2.5's headline change for every OTHER outside-WorkDir
			// write.
			if resolvedGrant, resolveErr := resolveRealpathUnderWorkDir(rawPath, policy.WorkDir); resolveErr == nil {
				grantedRoots := make([]string, 0, len(policy.AllowedRoots)+1)
				grantedRoots = append(grantedRoots, policy.AllowedRoots...)
				grantedRoots = append(grantedRoots, resolvedGrant)
				granted.AllowedRoots = grantedRoots
			}

			return ResolvePath(ctx, granted, toolName, callID, op, rawPath)
		}
	}
	return ResolvePath(ctx, policy, toolName, callID, op, rawPath)
}

// lexicalAbsPath joins rawPath under workDir (when rawPath is relative,
// FR-004) and Cleans the result, WITHOUT resolving any symlink — the
// pre-resolution "lexical" absolute spelling that resolveRealpathUnderWorkDir
// then resolves in full, and that the FSOpWrite/FSOpServe mount branch of
// ResolvePath (below) resolves only the ANCESTOR chain of, via
// resolveAncestorRealpath/safeRelPath — see those functions' doc comments for
// why the leaf must stay unresolved there.
//
// Fails closed on an embedded NUL byte: Go's os/syscall layer would reject
// such a path at the first real syscall anyway (BytePtrFromString), but
// checking it here — before any I/O — makes the rejection deterministic and
// platform-independent rather than depending on exactly which syscall happens
// to touch the string first.
func lexicalAbsPath(rawPath, workDir string) (string, error) {
	if strings.IndexByte(rawPath, 0) != -1 {
		return "", fmt.Errorf("embedded NUL byte in path")
	}
	if filepath.IsAbs(rawPath) {
		return filepath.Clean(rawPath), nil
	}
	return filepath.Clean(filepath.Join(workDir, rawPath)), nil
}

// resolveRealpathUnderWorkDir resolves rawPath to an absolute, symlink-
// resolved location, joining it under workDir first when rawPath is
// relative (FR-004). It mirrors fspolicy.realpath's fail-closed
// walk-up-until-found strategy (that function is unexported and
// pkg/fspolicy is a deliberate leaf package pkg/tools must not import back
// into for this) so a not-yet-existing leaf (the common write_file case)
// still resolves through any existing symlinked ancestor, while a genuine
// hard failure (a permission error, ENAMETOOLONG, ...) — as opposed to a
// benign "does not exist yet" — is surfaced rather than silently
// swallowed into a best-effort lexical fallback.
//
// Fails closed on an embedded NUL byte: Go's os/syscall layer would reject
// such a path at the first real syscall anyway (BytePtrFromString), but
// checking it here — before any I/O — makes the rejection deterministic
// and platform-independent rather than depending on exactly which syscall
// happens to touch the string first.
func resolveRealpathUnderWorkDir(rawPath, workDir string) (string, error) {
	abs, err := lexicalAbsPath(rawPath, workDir)
	if err != nil {
		return "", err
	}

	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("resolve symlinks for %q: %w", abs, err)
	}

	dir := filepath.Dir(abs)
	remainder := filepath.Base(abs)
	for {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			return filepath.Clean(filepath.Join(resolved, remainder)), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolve ancestor %q: %w", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no existing ancestor found for %q", abs)
		}
		remainder = filepath.Join(filepath.Base(dir), remainder)
		dir = parent
	}
}

// resolveAncestorRealpath resolves ONLY the ancestor (dirname) chain of an
// absolute path, walking up exactly like resolveRealpathUnderWorkDir's own
// not-yet-existing-leaf fallback does, while leaving the final component (and
// everything below whichever ancestor first resolves) exactly as given.
//
// Unlike resolveRealpathUnderWorkDir, this NEVER takes a "resolve the whole
// path" fast path even when absPath exists and its own leaf happens to be a
// symlink. That distinction matters: safeRelPath (the sole caller) uses this
// to normalize an absolute rawPath's ancestor prefix against a realpath-
// resolved workspace root, and the resulting relative path is what
// ResolvePath ultimately hands to an os.Root-backed I/O call. If the leaf
// were resolved here too, that call would receive the leaf's symlink TARGET
// baked in instead of its name — silently defeating FR-006's TOCTOU
// protection, which depends on os.Root re-resolving the leaf fresh, at the
// moment of the actual syscall, not at this earlier lexical check.
func resolveAncestorRealpath(absPath string) (resolvedDir, remainder string, err error) {
	dir := filepath.Dir(absPath)
	remainder = filepath.Base(absPath)
	for {
		resolved, evalErr := filepath.EvalSymlinks(dir)
		if evalErr == nil {
			return filepath.Clean(resolved), remainder, nil
		}
		if !os.IsNotExist(evalErr) {
			return "", "", fmt.Errorf("resolve ancestor %q: %w", dir, evalErr)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", fmt.Errorf("no existing ancestor found for %q", absPath)
		}
		remainder = filepath.Join(filepath.Base(dir), remainder)
		dir = parent
	}
}

// safeRelPath computes the os.Root-relative path for rawPath under workDir,
// the same contract the pre-ADR-046 getSafeRelPath (filesystem.go:1263,
// deleted) provided: an absolute rawPath is made relative to workDir; the
// result must be a "local" path (filepath.IsLocal) — no leading ".." escape
// — or the call is refused. Called only after ResolvePath has already
// established (via the realpath check) that the target lies within workDir.
//
// workDir MUST already be realpath-resolved (ResolvePath passes realWorkDir,
// never the possibly-unresolved policy.WorkDir — HIGH, macOS CI regression,
// PR #597): for an absolute rawPath, only its ANCESTOR chain is resolved
// (via resolveAncestorRealpath) before being compared against workDir — the
// final path component is left exactly as the caller spelled it, matching
// resolveRealpathUnderWorkDir's own not-yet-existing-leaf contract. This is a
// second, lexical, defense-in-depth check — os.Root re-resolves rel itself
// at I/O time, following any symlinks (leaf included) fresh at that moment
// (FR-006) — so leaving the leaf unresolved here is required, not merely
// permitted: resolving it here instead would bake in whatever it pointed to
// at check time, defeating that re-resolution's TOCTOU protection.
func safeRelPath(workDir, rawPath string) (string, error) {
	rel := filepath.Clean(rawPath)
	if filepath.IsAbs(rel) {
		resolvedDir, remainder, err := resolveAncestorRealpath(rel)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrPathInvalid, err)
		}
		relDir, err := filepath.Rel(workDir, resolvedDir)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrPathInvalid, err)
		}
		rel = filepath.Join(relDir, remainder)
	}
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%w: %q escapes the working directory", ErrOutsideScope, rawPath)
	}
	return rel, nil
}

// wrapFSErr normalizes a ReadFile/Open-family error the same way the
// pre-ADR-046 hostFs/sandboxFs implementations did, so existing callers'
// substring checks ("file not found", "access denied") keep matching. verb
// is the human-readable action ("read" or "open") that appears in the
// message (MEDIUM #8, ADR-046 P1 review: wrapReadErr and wrapOpenErr were
// byte-identical apart from this one word — consolidated into a single
// implementation with the two original names kept as one-line delegators
// below so every existing call site's behavior is unchanged).
func wrapFSErr(verb string, err error) error {
	if os.IsNotExist(err) {
		return fmt.Errorf("failed to %s file: file not found: %w", verb, err)
	}
	if os.IsPermission(err) || strings.Contains(err.Error(), "escapes from parent") ||
		strings.Contains(err.Error(), "permission denied") {
		return fmt.Errorf("failed to %s file: access denied: %w", verb, err)
	}
	return fmt.Errorf("failed to %s file: %w", verb, err)
}

// wrapReadErr normalizes a ReadFile-family error. Delegates to wrapFSErr.
func wrapReadErr(err error) error {
	return wrapFSErr("read", err)
}

// wrapOpenErr normalizes an Open-family error. Delegates to wrapFSErr.
func wrapOpenErr(err error) error {
	return wrapFSErr("open", err)
}

// writeFileAtomicHost writes data to abs directly on the host filesystem
// (the fspolicy.FSScopeUnrestricted case), matching the pre-ADR-046 hostFs.
// WriteFile contract exactly (fileutil.WriteFileAtomic: temp file + fsync +
// rename, 0o600).
func writeFileAtomicHost(abs string, data []byte) error {
	return fileutil.WriteFileAtomic(abs, data, 0o600)
}

// writeFileAtomicRoot writes data to relPath underneath root atomically
// (temp file + fsync + rename + directory fsync), porting the EXACT body
// the pre-ADR-046 sandboxFs.WriteFile used — this is the confined,
// os.Root-backed write path FR-006 requires.
func writeFileAtomicRoot(root *os.Root, relPath string, data []byte) error {
	dir := filepath.Dir(relPath)
	if dir != "." && dir != "/" {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create parent directories: %w", err)
		}
	}

	// Use atomic write pattern with explicit sync for flash storage
	// reliability. Using 0o600 (owner read/write only) for secure default
	// permissions.
	tmpRelPath := fmt.Sprintf(".tmp-%d-%d", os.Getpid(), time.Now().UnixNano())

	tmpFile, err := root.OpenFile(tmpRelPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open temp file: %w", err)
	}

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		if rmErr := root.Remove(tmpRelPath); rmErr != nil {
			logger.WarnCF("filesystem", "failed to remove temp file after write error",
				map[string]any{"error": rmErr.Error()})
		}
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// CRITICAL: Force sync to storage medium before rename. This ensures
	// data is physically written to disk, not just cached.
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		if rmErr := root.Remove(tmpRelPath); rmErr != nil {
			logger.WarnCF("filesystem", "failed to remove temp file after sync error",
				map[string]any{"error": rmErr.Error()})
		}
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		if rmErr := root.Remove(tmpRelPath); rmErr != nil {
			logger.WarnCF("filesystem", "failed to remove temp file after close error",
				map[string]any{"error": rmErr.Error()})
		}
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := root.Rename(tmpRelPath, relPath); err != nil {
		if rmErr := root.Remove(tmpRelPath); rmErr != nil {
			logger.WarnCF("filesystem", "failed to remove temp file after rename error",
				map[string]any{"error": rmErr.Error()})
		}
		return fmt.Errorf("failed to rename temp file over target: %w", err)
	}

	// Sync directory to ensure rename is durable.
	if dirFile, err := root.Open("."); err == nil {
		if syncErr := dirFile.Sync(); syncErr != nil {
			logger.WarnCF(
				"filesystem",
				"directory sync failed after atomic write",
				map[string]any{"error": syncErr.Error()},
			)
		}
		dirFile.Close()
	}

	return nil
}
