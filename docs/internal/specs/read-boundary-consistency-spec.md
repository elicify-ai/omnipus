Status: In review

ADR: [ADR-081 — Unified Library search, general file search, and the grep engine](../architecture/ADR-081-unified-library-search-and-grep-engine.md) (D4 amendment and grill corrections of 2026-09-26) and [ADR-092 — Shell permission modes: Ask / Auto / God Mode; drop the block list; one rule format](../architecture/ADR-092-shell-permission-modes.md) (2026-09-26 revision note, D9/J2 amendment, correction note with the D8 pin fix and the MIN-004 loop-level test)

# Feature Specification: Read boundary consistency — search, read and list follow one rule

**Created**: 2026-09-26
**Issue**: https://github.com/elicify-ai/omnipus/issues/920
**Input**: founder interview record `docs/internal/specs/read-boundary-consistency-interview.md`
(decisions D1–D10, FINAL); ADR-081 and ADR-092 as amended and corrected at commit `a809546`;
ADR grill report `docs/internal/architecture/ADR-092-shell-permission-modes-review.md`;
`docs/internal/specs/unified-search-and-grep-spec.md` (FINAL) and its two review rounds.
**Evidence baseline**: branch `feature/920-read-boundary` @ `7efcc5d`.
**Supersedes (agent `grep` only)**: `unified-search-and-grep-spec.md` FR-020; FR-014 for the
agent tool; US-3 narrative and AS-7; US-2 AS-5 for a `path` argument (it still holds for the
human search bar and for symlinks met during a walk); the BDD scenarios "confinement holds
under adversarial layout" and "grep cannot leave its own workspace"; TDD rows 11 and 23.
Each carries a dated pointer to this spec.

---

## Overview

Omnipus agents have three ways to look at files: `read_file` (open one file), `list_directory`
(list one folder) and `grep` (search many files by name and content). Today they disagree
about where an agent may look. `read_file` and `list_directory` may read anywhere on the
machine except Omnipus's protected files (the "secret set": the master key, credentials,
config, other agents' homes, other workspaces and a few more). `grep` refuses any absolute
path and any `..`, so it only ever searches the agent's own workspace and mounted folders.
And with Auto-approve on, `grep` runs without asking while `read_file` of a file outside the
workspace still asks. Nothing about this is a real security boundary: the shell reads
anywhere, and `read_file` already could.

The founder decided (D1–D3) that there is one read rule, defined once, and all three tools
follow it: `grep` accepts the same paths `read_file` accepts, including absolute paths and
`..`, and still can never reach the secret set, other agents' files or other workspaces. With
Auto-approve on, all three just run, also outside the workspace; the configured tool policy
(allow / ask / deny) still decides whether Auto-approve applies at all. When no `path` is
given, `grep` still searches only the workspace and its mounted folders (D5). Search limits
stay as they are (D7).

Two narrower pieces ride along. The Judge's review turns — the only turns Omnipus runs
"read-confined" — get the same area for all three tools: the workspace plus its mounted
folders (D6, D10). Today the Judge's `grep` can search a mount while its `read_file` cannot
open a file in it. And a hidden defect found in the ADR grill (D8) is fixed: moving
`read_file`/`list_directory` to "always runs under Auto" would, without the fix, make every
Auto-approved read on an Ask policy fail. The widened `grep` also gains the protections the
other two tools already have: the skills-registry instruction-file gate, the agent-metadata
guard, and audit rows for refusals and for which folders a search covered.

---

## Existing Codebase Context

GitNexus is not available in this session (dispatch note). Every symbol below was read
directly with Read/Grep at `7efcc5d`; impact rows are Grep caller sweeps and are labelled
Inferred accordingly.

### Symbols Involved

| Symbol | Role | Context (read at `7efcc5d`) |
|---|---|---|
| `pkg/tools/resolvepath.go::ResolvePath` / `resolveValidatedPath` | calls; modifies (read-confined branch) | the single path chokepoint (ADR-063 D2). Outside `WorkDir`, `case FSOpRead, FSOpList, FSOpSend` refuses a read-confined turn outright (`if rp.policy.ReadConfined { … (read-confined turn) }`), otherwise returns a host-filesystem `PathHandle`. `fspolicy.IsCarveOut` is applied earlier, unconditionally. |
| `pkg/tools/resolvepath.go::ResolvePathAllowingPatterns` | modifies | appends the resolved path of a regex-matched `rawPath` to a call-scoped `AllowedRoots`; its comment says this "does NOT reopen a read-confined turn" — true only while the read-confined branch ignores `AllowedRoots` (ADR-081 D6 correction 1). |
| `pkg/tools/resolvepath.go::matchedAllowedRoot`, `newMountRootHandle` | reused | the write/serve branch's mount-anchored `os.Root` handle builder; reused for the read-confined mount exception (ADR-081 D6 correction 2). |
| `pkg/tools/resolvepath.go::isSkillInstructionFileLeaf`, `classifySkillsGateCandidate`, `classifySkillsGate` | reused | ADR-072 D10.3 skills-registry gate primitives; single-spelling pair for the per-visited-file check (ADR-081 MIN-003 correction). |
| `pkg/tools/grep.go::validateGrepScope` | modifies / retires | refuses `strings.HasPrefix(scope, "/")` and any `..` segment; keeps a NUL pre-check. Not Windows-safe. |
| `pkg/tools/grep.go::grepRoots`, `resolveScopedRoot`, `splitGrepScopeMount`, `normalizeGrepScope`, `guardCarveOuts`, `carveOutFS` | modifies / extends | root assembly. `carveOutFS.ReadDir` filters carve-out entries out of listings; `splitGrepScopeMount` splits on `/` only. |
| `pkg/tools/grep.go::GrepTool.Description` | rewritten (prometheus-prompt-engineer) | states "never anywhere else; … another agent's or workspace's files are never reachable". |
| `pkg/tools/filesystem.go::ReadFileTool.Description`, `ListDirTool.Description` | reviewed / rewritten | rendered into `docs/reference/built-in-tools.md`. |
| `pkg/tools/filesystem.go::ReadFileTool.AutoApproveVerdict`, `ListDirTool.AutoApproveVerdict` | deleted (dead after the class move) | ADR-092 correction note, "Dead code this leaves". |
| `pkg/tools/filesystem.go::guardMetadataPath` + `pkg/tools/metadata_guard.go::metadataFileMatch` | reused | `read_file` calls `guardMetadataPath(…, "read")`; `list_directory` does not. |
| `pkg/tools/auto_approve.go::autoApproveClasses` | modifies | `"read_file": AutoRunsIfArgs`, `"list_directory": AutoRunsIfArgs`, `"grep": AutoRuns`. |
| `pkg/tools/auto_approve.go::AutoPin`, `AutoPinForVerdict`, `RecheckAutoPin`, `resolveAutoCheckedPath` | modifies | D8 fix: `AutoPin.Class`, copy in `AutoPinForVerdict`, skip in `RecheckAutoPin` for class `runs`. |
| `pkg/agent/loop_run_turn_tools.go` | unchanged | pins every Auto-run call on an `ask` policy (`tools.WithAutoApproved(…, tools.AutoPinForVerdict(…))`). |
| `pkg/agent/verifier_adjudication.go::dispatchTurn` | unchanged | the only `tools.WithReadConfined(callCtx, true)` call in production code (Grep of `WithReadConfined(` outside tests: one hit). |
| `pkg/tools/path_audit.go::emitPathAccessDenied`, `PathAccessDeniedEvent`, `classifyPathDenialReason` | reused | event `path.access_denied`; reasons `carve_out`, `path_invalid`, `outside_workspace` from typed sentinels. |
| `pkg/audit/audit.go::validEventNames` | extends | new event name registered here. |
| `cmd/docsref/main.go::toolRows`, `autoApproveLabel`, header text | regenerates | renders `docs/reference/built-in-tools.md`; checked by `scripts/check-docs-reference.sh`. |
| `pkg/gateway/rest_tool_registry.go` (`tools.AutoApproveClassOf` → `ToolRegistryEntry.auto_approve`) | unchanged code, changed output | the SPA's "Auto:" marker for `read_file`/`list_directory` changes from `runs_if_args` to `runs`. |
| `pkg/coreagent/seed_system.go::systemAgentSeed` | comment-only | Judge seed: `read_file`, `list_directory`, `grep` allow. PlanSupervisor seed: `grep` allow, no `read_file`/`list_directory`; its comment cites the superseded FR-020 confinement. |
| `pkg/gateway/rest_library_files_search.go` | MUST NOT change | human Library search bar (ADR-081 D4, unchanged clause). |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents (Grep sweep, non-test) | d=2 / notes |
|---|---|---|---|
| `resolveValidatedPath` (read-confined branch) | **HIGH (fan-in)** — behaviour changes only when `ReadConfined` is true | every `ResolvePath` caller: `pkg/tools/shell.go`, `web_serve.go`, `browser/tools.go`, `browser/tools_interact.go`, `pkg/gateway/rest_executor_smoketest.go`, `rest_clivalidate.go`, plus `ResolvePathAllowingPatterns` → `resolveAutoCheckedPath` (`filesystem.go`, `edit.go`, `send_file.go`) | only the Judge's review turn sets `ReadConfined`; the D9 security-lead check covers it. Existing pins `TestResolvePath_ReadConfinedRefusesOutsideWorkdir`, `TestResolvePath_ReadConfinedRefusesListAndSendToo`, `TestResolvePath_ReadConfinedDoesNotReopenCarveOuts` must be re-read and updated deliberately (Regression table). Inferred. |
| `ResolvePathAllowingPatterns` | MEDIUM | `auto_approve.go` (`AutoWorkspacePath`), file tools via `resolveAutoCheckedPath` | change is a no-op unless `ReadConfined`. Inferred. |
| `RecheckAutoPin` / `AutoPin` / `AutoPinForVerdict` | **HIGH** — every Auto-approved file/browser tool call passes through it | `resolveAutoCheckedPath` (7 call sites in `filesystem.go`, `edit.go`, `send_file.go`), `browser/tools.go`, `pkg/agent/loop_run_turn_tools.go` | the zero-value class MUST keep today's re-check (fail-closed). Inferred. |
| `autoApproveClasses` | MEDIUM | `AutoApproveClassOf` → `rest_tool_registry.go`, `cmd/docsref/main.go`; `pkg/gateway/auto_approve_classification_test.go` | user-visible label change in SPA and docs. Inferred. |
| `grep.go` root assembly / `validateGrepScope` | MEDIUM | `GrepTool.Execute` only (single caller each) | 12 `grep*_test.go` files in `pkg/tools`; `TestGrepTool_OwnWorkspaceOnly` asserts the superseded rule. Inferred. |

The two HIGH rows need backend-lead's acknowledgement before GREEN starts; they are the
reason the D9 dedicated security-lead check exists.

---

## Contract Changes (contract-first — Hard Constraint #8)

**None.** Verified, not assumed:

| Surface | Why no change is needed | Evidence |
|---|---|---|
| Audit entries shown in the SPA | the new event name is a new *value* of `AuditEntry.event`, which is a free string constrained by `pattern: '^[a-z_.]+$'`; `path.search_roots` matches it | `contracts/components/schemas/AuditEntry.yaml` (`event.pattern`) |
| The SPA "Auto:" marker | `ToolRegistryEntry.auto_approve` already carries `runs` / `runs_if_args` / `asks`; only the value served for two tools changes | `contracts/components/schemas/ToolRegistryEntry.yaml` (`auto_approve`) |
| Agent tool arguments | tool schemas are not gateway/SPA wire formats; `grep`'s `path` parameter keeps its name and type | `pkg/tools/grep.go::GrepTool.Parameters` |

No new config key, no new REST route, no new WS frame.

## API and Data

- No endpoint is added, removed or changed. `POST /api/v1/library/{workspace_id}/files/search`
  (the human Library bar) is explicitly unchanged.
- Stored data: one new kind of audit row (event `path.search_roots`), appended to the existing
  audit log through the existing logger; no new file, no schema change.

---

## User Stories & Acceptance Criteria

> Phase-boundary rule: these stories describe observable behaviour only.

### User Story 1 — An agent's search reaches exactly what its reading reaches (Priority: P0)

An agent that can read a file with `read_file` should also be able to search it with `grep`,
and an agent that cannot read a file must not be able to search it either. Today an agent
asked to "find where the build config mentions `timeout` in `/srv/app`" must fall back to
`bash` or to reading files one by one, because `grep` refuses absolute paths. After this
story the three tools give the same yes/no answer for the same path.

**Why this priority**: it is the issue's core ask (D1); every other story depends on it.

**Independent Test**: for each path kind in the parity matrix (Dataset DS-1), call
`read_file`/`list_directory` and `grep` with the same `path` as the same unconfined agent and
compare: both admitted or both refused, with the refusal reason class matching.

**Acceptance Scenarios**:

1. **Given** a folder outside the workspace, its mounts and the Omnipus home, **When** an
   agent greps with that folder's absolute path, **Then** the search runs over that folder and
   returns its matches.
2. **Given** a path that uses `..` to leave the workspace, **When** an agent greps with it,
   **Then** the result is the same as greping the absolute location it resolves to.
3. **Given** a path inside the secret set, another agent's home, or another workspace,
   **When** an agent greps with it, **Then** the call is refused with the same reason class
   `read_file` gives for that path, and no match is returned.
4. **Given** no `path` argument, **When** an agent greps, **Then** the search covers exactly
   the workspace and every folder mounted into it, as before.
5. **Given** a relative `path` whose first segment is a mount's name, **When** an agent greps,
   **Then** the search covers that mount (or its sub-folder) as before; an absolute path or a
   path containing `..` is never read as a mount name.
6. **Given** a search whose location is inside the workspace (reached by any spelling),
   **When** matches return, **Then** their paths are workspace-relative; **given** any other
   location, **Then** their paths are absolute and can be passed to `read_file` unchanged.
7. **Given** a `path` that does not exist, **When** an agent greps, **Then** it gets an error
   naming the path — never a successful "0 matches".
8. **Given** a search of a very large location such as a volume root, **When** it runs,
   **Then** it stops at the unchanged search limits and says it stopped early.

### User Story 2 — A wider search never shows what reading would refuse (Priority: P0)

A search that can now walk large parts of the disk must not leak what the other two tools
refuse: protected Omnipus files met *during* the walk, skill instruction files in the skills
registry (which must be loaded through the Skill tool), and agent metadata files (which have
their own tools). Symlinks met during the walk still are not followed.

**Why this priority**: without it, D1 would open holes the interview's security notes name
explicitly.

**Independent Test**: grep a seeded tree that contains a secret-set entry, a registry skill's
`SKILL.md`, an agent's `SOUL.md` and an outward symlink, using a term that appears in each
file's name and content; none of them appears in the result.

**Acceptance Scenarios**:

1. **Given** a searched folder that contains protected Omnipus files, **When** a grep matches
   their names and contents, **Then** none of them appears in the result, by name or content.
2. **Given** a search over the skills registry, **When** the term appears in a registry
   skill's instruction file and in one of its helper files, **Then** only the helper file
   matches.
3. **Given** a project-shelf skill (inside a mounted project), **When** a grep matches its
   instruction file, **Then** the match is returned, exactly as `read_file` would read it.
4. **Given** agent metadata files (`SOUL.md`, `HEARTBEAT.md`, `AGENT.md`, legacy `MEMORY.md`
   under `agents/<id>/`) inside a searched folder, **When** a grep matches them, **Then** they
   are not returned, by name or content.
5. **Given** a symlink met during a search that points outside the searched folder, **When**
   the search runs, **Then** nothing behind the link is read; the link itself may match by
   name.
6. **Given** a `path` argument that is itself a symlink, **When** an agent greps it, **Then**
   the search covers the link's resolved location, admitted or refused exactly as `read_file`
   would admit or refuse that location.

### User Story 3 — Auto-approve treats all three reading tools alike (Priority: P0)

A person who turned Auto-approve on expects reading and searching to just run. Today
`read_file /etc/hosts` asks while `grep` runs. After this story, with the tool set to Ask and
Auto-approve on, all three run without a prompt inside and outside the workspace; Deny still
refuses; Auto-approve off still asks.

**Why this priority**: D2/D3; and without the D8 fix this change would break every
Auto-approved read on an Ask policy — a release-blocking regression.

**Independent Test**: in a real agent turn with the three tools set to Ask and Auto-approve
on, read, list and search one path inside and one outside the workspace; all six calls
return content with no approval card.

**Acceptance Scenarios**:

1. **Given** Ask and Auto-approve on, **When** an agent reads, lists or searches a location
   inside or outside the workspace, **Then** the call runs without an approval card.
2. **Given** Ask and Auto-approve off, **When** an agent reads, lists or searches any
   location, **Then** an approval card appears first.
3. **Given** Deny, **When** an agent calls any of the three, **Then** the call is refused
   whether Auto-approve is on or off.
4. **Given** Allow, **When** an agent calls any of the three, **Then** it runs with no card.
5. **Given** Ask and Auto-approve on, **When** the target is in the secret set, **Then** the
   call is refused — neither run nor asked.
6. **Given** Ask and Auto-approve on for the agent but "Never auto-approve" set on that agent,
   or Auto-approve switched off for the chat, **When** it reads outside the workspace,
   **Then** an approval card appears.
7. **Given** Ask and Auto-approve on, **When** an agent writes a file inside the workspace and
   the target is swapped to point outside between the approval decision and the write,
   **Then** the write is refused (unchanged protection for write tools).
8. **Given** Ask and Auto-approve on, **When** an agent writes outside the workspace or sends
   a file from outside it, **Then** an approval card appears (unchanged).

### User Story 4 — The Judge reviews work in mounted folders with every reading tool (Priority: P0)

The Judge reads the work it is judging. That work can live in a folder mounted into the
workspace. Today the Judge's `grep` can search that mount while its `read_file` cannot open a
file in it. After this story, during a review turn all three tools reach the workspace and its
mounted folders, and nothing else.

**Why this priority**: D6/D10; the Judge verdict quality depends on reading the reviewed work,
and the confinement is a security boundary (JUDGE-FR-060).

**Independent Test**: in a Judge review turn, read, list and search a file in a mount (all
succeed) and a file outside workspace and mounts (all refused as read-confined).

**Acceptance Scenarios**:

1. **Given** a Judge review turn, **When** the Judge reads, lists or searches a location in a
   mounted folder by its absolute path, **Then** the call succeeds.
2. **Given** a Judge review turn, **When** the Judge reads, lists or searches a location outside
   the workspace and its mounts (by absolute path, `..` or a symlink), **Then** the call is
   refused with a message stating the turn is read-confined.
3. **Given** an operator allow-path pattern that covers session transcripts, **When** the Judge
   reads a transcript file in a review turn, **Then** the read is still refused.
4. **Given** a Judge review turn, **When** a file is sent to chat from a mounted folder, **Then**
   the send is refused, as today.
5. **Given** a Judge review turn reading a file in a mount, **When** a folder above that file
   inside the mount is swapped for a link pointing outside the mount after the path was
   checked, **Then** the read fails rather than reading outside the mount.
6. **Given** the Plan Supervisor (not read-confined), **When** it greps a location outside the
   workspace that is not in the secret set, **Then** the search runs — the accepted risk of
   D10.

### User Story 5 — Every search leaves an audit trail (Priority: P1)

An operator reviewing what an agent looked at can see, in Settings → Security → Audit Log,
which folders each `grep` call searched and every refused `grep` path — the same way refused
`read_file` paths already appear.

**Why this priority**: the audit trail is how the wider reach stays accountable; P1 because
the reach itself (US-1) is the P0 change.

**Independent Test**: run one successful and one refused grep, then open the Audit Log and
find one "roots searched" entry and one "path access denied" entry for the agent.

**Acceptance Scenarios**:

1. **Given** a grep call that searched, **When** the operator opens the Audit Log, **Then**
   exactly one entry for that call lists the searched folders and the raw `path` argument.
2. **Given** a grep call refused because of its `path`, **When** the operator opens the Audit
   Log, **Then** one "path access denied" entry shows the tool, the path and the reason.
3. **Given** a grep that matched hundreds of files, **When** the operator opens the Audit Log,
   **Then** the call produced one entry, not one per file.
4. **Given** a roots-searched entry, **When** the operator expands it, **Then** it shows no
   search term and no file content.

### User Story 6 — What the product says matches what it does (Priority: P1)

The tool descriptions agents read, the user docs, the generated tool reference and the
Tools & Permissions marker all tell the same, current story.

**Why this priority**: an agent that believes `grep` is workspace-only will not use it for an
outside path; a user who reads "`read_file` of `/etc/hosts` asks" is misinformed after this
change.

**Independent Test**: read the three descriptions, `docs/tools.md`, `docs/security.md`,
`docs/reference/built-in-tools.md` and the Tools & Permissions panel; no statement contradicts
US-1 to US-5.

**Acceptance Scenarios**:

1. **Given** the shipped build, **When** an agent reads `grep`'s description, **Then** it
   learns the default search area, that `path` may be relative, a mount name or absolute,
   that protected Omnipus files and other agents' and workspaces' files are never reachable,
   and how match paths are written.
2. **Given** the user docs, **When** a user reads the Auto-approve section, **Then** it says
   reading, listing and searching run under Auto-approve inside and outside the workspace,
   and that to keep an agent from reading, both `read_file` and `grep` must be denied.
3. **Given** the Tools & Permissions panel, **When** `read_file` or `list_directory` is set to
   Ask, **Then** its marker reads "Auto: runs".
4. **Given** the generated tool reference, **When** it is regenerated, **Then** it matches the
   committed file and lists `read_file`/`list_directory` as "Runs".

### User Story 7 — Windows paths are understood the Windows way (Priority: P1)

On Windows an absolute path looks like `C:\Users\me\project` or `\\server\share\folder`, never
`/…`. The old check treated such a path as relative.

**Why this priority**: Windows is a supported platform (founder decision 2026-09-16); P1
because it is a sub-case of US-1.

**Independent Test**: on a Windows runner, grep with a drive path, a forward-slash drive path,
a UNC path and a backslash-relative path, and compare with `read_file`.

**Acceptance Scenarios**:

1. **Given** Windows, **When** an agent greps `C:\…` or `C:/…`, **Then** it is treated as an
   absolute path (never as workspace-relative, never as a mount name).
2. **Given** Windows, **When** an agent greps `\\host\share\…`, **Then** it is treated as an
   absolute path; an unreachable host gives an error, never "must be relative".
3. **Given** Windows, **When** an agent greps `notes\sub` or `my-mount\src`, **Then** the
   backslash is a separator: the first is a workspace folder, the second a mount's sub-folder.
4. **Given** Linux or macOS, **When** an agent greps `notes\sub`, **Then** the backslash is
   part of a file name, exactly as `read_file` treats it.

### Edge Cases

- `path` is the empty string → same as no `path` (US-1 AS-4).
- `path` names one regular file (anywhere admitted) → only that file is searched; its match
  path follows US-1 AS-6.
- `path` contains a NUL byte → refused as an invalid path; audited with reason `path_invalid`.
- `path` resolves into the agent's own workspace via an absolute spelling or via `..` →
  workspace-relative match paths (US-1 AS-6), not absolute.
- `path` is an absolute path into a mount → absolute match paths (never the mount-name form).
- `path` is the Omnipus home itself → admitted; every protected entry under it is withheld
  from the result (US-2 AS-1); session, task, plan and memory files outside the secret set
  are searchable (the same reach `read_file` already has).
- `path` is a volume root (`/`, `C:\`) → admitted; bounded by the unchanged limits.
- A location becomes unreadable mid-walk → unchanged `root_lost` honesty.
- Two concurrent searches already running → the third waits up to 2 s, then reports busy
  (unchanged).
- A custom agent set to `grep: allow` and `read_file: deny` → its search still reads file
  content anywhere `read_file` could have (Security section, accepted and documented).
- The Judge with no `path` → workspace plus mounts, as before.

---

## UI Screens and States

No new screen or component. Two existing surfaces change what they show:

| Screen / Component | Loading | Empty | Error | Partial | Success |
|---|---|---|---|---|---|
| Settings → Security → Audit Log (`src/components/settings/AuditLogViewer.tsx`) | unchanged | unchanged ("Audit events will appear here as agents run") | unchanged | unchanged | a `path.search_roots` row renders with the existing fallback event badge; its name appears in the event filter through the existing live vocabulary (union of base values and loaded event names); expanding it shows `roots` and `path_arg` |
| Agents → Tools & Permissions and Tool Access — Global Policies (`src/components/shared/ToolPolicyEditor.tsx`) | unchanged | unchanged | unchanged | unchanged | for `read_file` and `list_directory` on Ask, the marker reads "Auto: runs" instead of "Auto: runs inside workspace" |

## User Journey

1. The operator leaves `read_file`, `list_directory` and `grep` on their shipped defaults
   (Allow on a fresh install) or sets them to Ask with Auto-approve on.
2. In chat, the user asks an agent to find something in a folder outside the workspace
   (US-1 AS-1). The agent calls `grep` with the absolute path; no card appears (US-3 AS-1).
3. The agent opens a matching file with `read_file`, passing the match path unchanged
   (US-1 AS-6).
4. The operator opens Settings → Security → Audit Log and finds the roots-searched entry
   (US-5 AS-1).
5. Separately, a goal completes; the Judge reviews work that lives in a mount and reads it
   with all three tools (US-4 AS-1).

## Accessibility and Keyboard

No new control. The Audit Log row and the marker text use existing components; their
keyboard behaviour, focus ownership and labels are unchanged (`AuditLogViewer.tsx` already
labels the expand control per entry).

## Design-System Components

No new or changed component; `design-system/catalog.json` untouched. The new audit event uses
the viewer's existing fallback badge style (`BADGE_FALLBACK`); adding a dedicated colour for
it is out of scope.

---

## Security and User Promises

This change touches focus areas `pkg/tools` path resolution (`resolvepath.go`), `pkg/audit`
and the tool-approval surface; security-lead reviews it inside the 8-reviewer gate with the
D9 dedicated check.

### What gets wider, stated plainly

1. **Search reach (D1).** Any agent allowed to call `grep` can search anywhere `read_file`
   can read: the whole machine outside the secret set. This includes `$OMNIPUS_HOME` session
   transcripts, tasks, plans and memory files, which are outside the secret set today.
2. **No prompt under Auto (D2/D3).** With a tool on Ask and Auto-approve on, `read_file`,
   `list_directory` and `grep` run outside the workspace without a prompt. With Ask and
   Auto-approve off, the user is still asked. Deny is never overridden.
3. **Accepted residual risk — personal secret folders (D4).** `~/.ssh`, `~/.aws`, `~/.gnupg`,
   browser profiles and similar folders outside `$OMNIPUS_HOME` are not in the secret set, so
   under Auto an agent can read and search them without a prompt, as it already could with
   `bash`. Out of scope here; tracked in issue #921, which covers every tool.
4. **Accepted risk — Plan Supervisor (D10).** The Plan Supervisor holds `grep: allow` and is
   not read-confined. After this change its `grep` follows the same read rule as every other
   agent and can search `$OMNIPUS_HOME` sessions, tasks, plans and memory outside the secret
   set. The founder chose consistency over a Plan-Supervisor-specific confinement. No such
   confinement is designed here.
5. **Accepted and documented — `grep: allow` with `read_file: deny`.** A custom agent that
   keeps `grep` allowed but denies `read_file` gains open reads through search: `grep` returns
   matching lines (up to the unchanged caps and context lines). To confine an agent's reads,
   deny `grep` too (and `bash`). There is no coupling between the two policies and no third
   policy layer (Hard Constraint #6).

### What stays closed

- The secret set (`master.key`, `credentials.json`, `config.json`, `cli.token`, `entities`,
  `auth.json`, `backups`, other agents' homes under `agents/`, other workspaces under
  `workspaces/`, and their backups) is refused as a `path` and withheld from every walk.
- Skills-registry instruction files (`SKILL.md`, `AGENT.md`, `AGENTS.md` under
  `$OMNIPUS_HOME/skills`) are refused as a `path` and never produce a match (ADR-072 D10.3).
- Agent metadata files never produce a `grep` match.
- Symlinks met during a walk are never followed.
- The Judge's review turns stay confined to the workspace plus mounts; an operator allow-path
  pattern cannot widen them; `send_file` stays workspace-only in them.
- Write tools, `send_file` and `browser_screenshot` keep the workspace-path rule under Auto
  and keep the mid-call re-check.

### security-lead checklist (D9 dedicated check, plus D10)

| # | Check | Where |
|---|---|---|
| SL-1 | read-confined admission is mounts-only; `ResolvePathAllowingPatterns` injects nothing into `AllowedRoots` for a read-confined policy | `resolvepath.go::resolveValidatedPath`, `ResolvePathAllowingPatterns` |
| SL-2 | the read-confined mount read returns the mount-anchored `os.Root` handle, never a host-filesystem handle | `resolvepath.go::newMountRootHandle` use |
| SL-3 | `FSOpSend` stays confined for read-confined turns | same branch |
| SL-4 | widened-`grep` advisory-anchor residual (ancestor swapped between resolve and `os.OpenRoot`) — same class as existing mount roots | `grep.go` new root type |
| SL-5 | **D10 accepted risk**: Plan Supervisor `grep` reach includes `$OMNIPUS_HOME` sessions, tasks, plans and memory outside the secret set — confirm the risk statement matches the shipped behaviour (test S-4.9) | `seed_system.go` PlanSupervisor seed; `verifier_adjudication.go::dispatchTurn` |
| SL-6 | per-visited-file gates complete for every root type (secret set, skills registry, metadata), name and content hits alike | `grep.go` wrapper chain |
| SL-7 | D8: only class `runs` pins skip the re-check; zero-value class still re-checked; RUNS-IF pins unchanged | `auto_approve.go::RecheckAutoPin` |
| SL-8 | `path.search_roots` details carry no pattern and no file content | new emitter |
| SL-9 | `grep: allow` + `read_file: deny` documented in `docs/security.md` | docs |
| SL-10 | #921 residual stated in `docs/security.md` | docs |

### User promises that change (docs, drafted by the implementing lead, audited by docs-verifier)

| File | Current text (verbatim excerpt) | Required correction |
|---|---|---|
| `docs/tools.md` (Auto-approve paragraph) | "File tools run only when every path is inside the workspace or a mounted folder, so `write_file` to `notes/plan.md` runs while `read_file` of `/etc/hosts` asks." | reading, listing and searching run anywhere outside Omnipus's protected files; writing and sending keep the workspace rule; the example must not say `read_file` of `/etc/hosts` asks |
| `docs/security.md` ("File tools run only inside…") | "Anything outside asks, **including reads**. … `read_file` of `/etc/hosts` asks." | split reads from writes: `read_file`, `list_directory`, `grep` run; `write_file`, `edit_file`, `append_file`, the file `send_file` sends and `browser_screenshot`'s destination keep the workspace rule |
| `docs/security.md` ("This is stricter than the shell…") | "while `read_file /etc/hosts` asks. That difference is deliberate." | the read asymmetry with the shell is gone; only writes and sends remain stricter than the shell |
| `docs/security.md` (new text) | — | state: to confine an agent's reads, deny `read_file`, `grep` and `bash`; state the #921 residual (personal secret folders) |
| `docs/security.md` marker table | "Auto: runs inside workspace — Runs without a prompt when its file is inside…" | stays (still true for the remaining RUNS-IF tools); no edit required unless it names `read_file`/`list_directory` |
| `docs/goals.md` ("The Judge, in plain language") | "Its tools are read-only — open files, list folders, read session records" | SHOULD add that during a review the Judge reads only the workspace and its mounted folders |
| `docs/reference/built-in-tools.md` | generated | regenerate only (FR-025); never hand-edit |

---

## Behavioral Contract

> Phase-boundary rule: observable behaviour only.

Primary flows:
- When an agent greps with no `path`, the system searches the workspace and all its mounts.
- When an agent greps with a relative `path` whose first segment is a mount name (and no
  `..`), the system searches that mount or its sub-folder and reports mount-name paths.
- When an agent greps with any other non-empty `path`, the system admits or refuses it
  exactly as it would for `read_file`/`list_directory` with the same path, agent and turn.
- When an admitted location is inside the workspace, match paths are workspace-relative;
  otherwise they are absolute, with forward slashes, and accepted by `read_file` unchanged.
- When a tool on Ask runs with Auto-approve on, `read_file`, `list_directory` and `grep` run
  without a prompt wherever they are admitted.
- When the Judge is in a review turn, all three tools admit the workspace and its mounts and
  refuse everything else.
- When a grep search ran, the system writes exactly one roots-searched audit entry.

Error flows:
- When the `path` is refused, the system returns a refusal naming the path and writes one
  path-access-denied audit entry whose reason matches what `read_file` would record.
- When the `path` does not exist or cannot be opened, the system returns an error naming it,
  never a zero-match success.
- When the tool policy is Deny, the call is refused regardless of Auto-approve.
- When a write tool's approved target moves outside the workspace mid-call, the write is
  refused.

Boundary conditions:
- When the walk meets a protected Omnipus file, a registry skill instruction file or an agent
  metadata file, it returns no match for it by name or content.
- When the walk meets a symlink, it does not follow it.
- When a search reaches any of the unchanged limits, it stops and says so.

## Edge Cases

See "Edge Cases" under User Stories (single list, not duplicated).

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not confine the Plan Supervisor's `grep` (D10) — no Plan-Supervisor-specific
  root list, seed change or read-confined flag.
- The system must not couple `grep`'s reach to `read_file`'s policy, nor add any third policy
  layer (Hard Constraint #6).
- The system must not change the human Library search bar
  (`pkg/gateway/rest_library_files_search.go`), its reach or its tests.
- The system must not change `bash`, `send_file`'s path rule, the write tools' path rule or
  `browser_screenshot`.
- The system must not change the search limits (D7): 2 shared walk slots, 10 s deadline,
  50,000 files, 1,000 matches, 50 per file, 4 MiB per-file content cap, 64,000-character
  output cap, 2 s busy wait.
- The system must not use the mount-name shorthand for an absolute path or for any path
  containing `..`.
- The system must not protect personal secret folders outside `$OMNIPUS_HOME` in this change
  (issue #921).
- The system must not write one audit row per matched file for `grep`.
- The system must not put the search term or any file content into an audit row.
- The system must not follow symlinks during a walk.
- The system must not add a new tool, config key, REST route, WS frame or contract schema.

### Machine-Verifiable Constraints

- **MV-1** `grep`/`read_file`/`list_directory` admission parity: for every DS-1 row the three
  tools return the same admitted/refused outcome for the same agent, turn and path (except the
  rows marked "grep-only" or "grep stricter").
- **MV-2** Refusal audit: event `path.access_denied`, `tool` = `grep`, `reason` ∈
  {`carve_out`, `outside_workspace`, `path_invalid`}, equal to the reason `read_file` records
  for the same path (DS-1 column "reason").
- **MV-3** Roots-searched audit: event `path.search_roots` (matches `^[a-z_.]+$`, registered as
  a valid event name so no "unknown event" warning is logged), one row per call whose search
  ran, `decision` = `allow`, `details.roots` = the absolute realpaths actually walked (in walk
  order), `details.path_arg` = the raw argument or `""`; no other detail keys.
- **MV-4** Limits unchanged: DS-5 values asserted against the constants, not re-derived.
- **MV-5** Auto classification: `read_file`, `list_directory`, `grep` → `runs`; `write_file`,
  `edit_file`, `append_file`, `send_file`, `browser_screenshot` → `runs_if_args` (unchanged);
  the rest of the table unchanged (golden copy).
- **MV-6** `scripts/check-docs-reference.sh` exits 0 after regeneration.
- **MV-7** Read-confined refusal text contains `read-confined`.

## Integration Boundaries

### Agent runtime ↔ file tools

- **Data in / out**: tool call with `path`; structured text result or error.
- **Contract**: `tools.Tool` interface, policy compositor, Auto-approve classifier and pin.
- **On failure**: refusal result + `path.access_denied`; busy → structured busy error.
- **Development**: real — temp directories, real `os.Root`, real policy; no filesystem mocks
  except the existing injected-error seams.

### File tools ↔ audit log

- **Data in / out**: audit entries in; Audit Log REST read by the SPA.
- **Contract**: `contracts/components/schemas/AuditEntry.yaml` (unchanged).
- **On failure**: audit write is best-effort (existing emitter behaviour); a failed audit
  write never fails the tool call.
- **Development**: real audit logger in a temp dir.

### Docs generator ↔ committed reference

- **Data in / out**: tool descriptions and classes in; `docs/reference/built-in-tools.md` out.
- **Contract**: `scripts/check-docs-reference.sh` (exit 0 current, 1 drift, 2 cannot run).
- **Development**: real generator.

---

## Ambiguity Warnings

These are spec-level choices made inside the founder's decisions and the ADRs, recorded so
grill round 1 can challenge them. None is an unanswered founder question.

| # | What could be read two ways | Choice this spec makes | Why |
|---|---|---|---|
| 1 | `grep` withholds metadata and registry-skill instruction files by NAME; `list_directory` of the same folder still lists those names | keep the asymmetry: `grep` stricter on names, `list_directory` unchanged | ADR-081 gates 2–3 require refusing name hits; changing `list_directory` is outside #920 |
| 2 | ADR-081 left the roots-searched event name to backend-lead | this spec fixes it as `path.search_roots` so tests and docs have an oracle | sibling of `path.access_denied` in the same family; backend-lead may propose another name in grill, never at GREEN silently |
| 3 | whether a grep call rejected for an invalid pattern or glob writes a roots-searched row | it does not: the row is written only when the search ran (including zero-hit, truncated and `root_lost` outcomes) | deterministic oracle; nothing was searched |
| 4 | Windows: is `my-mount\src` a mount shorthand | yes — on Windows `\` is a separator for the shorthand test too | the platform's own path semantics; `read_file` already treats `\` as a separator on Windows |
| 5 | a NUL-byte `path` is refused by `grep`'s own pre-check before the single read decision | the pre-check stays (ADR-081) but MUST also write the `path.access_denied` row with reason `path_invalid` | audit parity with `read_file` |
| 6 | a relative `path` that stays inside the workspace | goes through the single read decision too (not only absolute and `..`) | one rule; a relative symlink leaving the workspace must be judged like `read_file` judges it |

---

## BDD Scenarios

### Feature: Read boundary consistency

#### Background

- **Given** an agent "A" in workspace "W" with working folder `<WS>` and one mount named
  `docs-mount` whose host folder is `<MNT>`
- **And** a folder `<EXT>` outside `<WS>`, `<MNT>` and `$OMNIPUS_HOME`, containing
  `ext/notes.txt` with the line "needle outside"
- **And** `<WS>/notes/a.md` and `<MNT>/src/b.md`, each containing "needle"
- **And** all paths in expectations are the realpaths of these folders (so `/tmp` →
  `/private/tmp` on macOS is derived, never hard-coded)
- **And** `read_file`, `list_directory` and `grep` are set to Allow for "A" unless a scenario
  says otherwise

#### US-1 — search reach equals read reach

##### Scenario Outline: S-1.1 grep and read_file give the same answer for every path kind

**Traces to**: User Story 1, Acceptance Scenarios 1, 2, 3, 5, 6
**Category**: Happy Path

- **Given** agent "A" in an ordinary (not read-confined) turn
- **When** "A" calls `grep` with pattern "needle" and `path` `<path>`
- **Then** the outcome is `<grep_outcome>` with match paths written as `<hit_form>`
- **And** `read_file`/`list_directory` on the same `<path>` give `<read_outcome>`

**Examples**: Dataset DS-1 rows 1–21 (each row is one example).

##### Scenario: S-1.2 grep with an absolute path outside everything finds the match

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** agent "A"
- **When** "A" greps "needle" with `path` `<EXT>`
- **Then** the result contains `<EXT>/ext/notes.txt` (absolute, forward slashes) with line 1
- **And** passing that path to `read_file` returns the file's content

##### Scenario: S-1.3 a `..` escape equals its resolved absolute location

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** `<EXT>` reachable from `<WS>` as `../<rel-to-EXT>`
- **When** "A" greps "needle" with that `..` path
- **Then** the result is identical (same matches, same absolute paths) to greping `<EXT>`

##### Scenario Outline: S-1.4 protected locations are refused like read_file refuses them

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Error Path

- **Given** agent "A"
- **When** "A" greps "needle" with `path` `<protected>`
- **Then** the call is refused and no match is returned
- **And** one `path.access_denied` audit entry records tool `grep` and reason `carve_out`

**Examples**:

| protected |
|---|
| `$OMNIPUS_HOME/master.key` |
| `$OMNIPUS_HOME/config.json` |
| `$OMNIPUS_HOME/backups` |
| `$OMNIPUS_HOME/agents/<other-agent>` |
| `$OMNIPUS_HOME/workspaces/<other-workspace>/work` |

##### Scenario: S-1.5 no path searches the workspace and every mount

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Happy Path

- **Given** agent "A"
- **When** "A" greps "needle" with no `path`
- **Then** the result contains exactly `notes/a.md` and `docs-mount/src/b.md`
- **And** it does not contain `<EXT>/ext/notes.txt`

##### Scenario: S-1.6 mount-name shorthand keeps working

**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Alternate Path

- **When** "A" greps "needle" with `path` `docs-mount/src`
- **Then** the result contains `docs-mount/src/b.md`

##### Scenario: S-1.7 absolute path into a mount never uses the mount-name form

**Traces to**: User Story 1, Acceptance Scenarios 5, 6
**Category**: Edge Case

- **When** "A" greps "needle" with `path` `<MNT>/src`
- **Then** the result contains `<MNT>/src/b.md` as an absolute path
- **But** not `docs-mount/src/b.md`

##### Scenario: S-1.8 absolute path into the workspace reports workspace-relative paths

**Traces to**: User Story 1, Acceptance Scenario 6
**Category**: Edge Case

- **When** "A" greps "needle" with `path` `<WS>/notes`
- **Then** the result contains `notes/a.md`, not `<WS>/notes/a.md`

##### Scenario: S-1.9 a missing path is an error, not zero matches

**Traces to**: User Story 1, Acceptance Scenario 7
**Category**: Error Path

- **When** "A" greps "needle" with `path` `<EXT>/does-not-exist`
- **Then** the result is an error naming `<EXT>/does-not-exist`
- **And** no `path.search_roots` entry is written

##### Scenario: S-1.10 a volume-root search stops at the unchanged limits

**Traces to**: User Story 1, Acceptance Scenario 8
**Category**: Edge Case

- **Given** search limits injected low (files visited = 50) so the test is fast
- **When** "A" greps "needle" with `path` `/` (on Windows, the system drive root)
- **Then** the result is marked truncated with reason `max_files` and a narrowing hint
- **And** no protected Omnipus file appears in it

##### Scenario: S-1.11 a NUL byte in `path` is refused and audited

**Traces to**: User Story 1, Acceptance Scenario 3 (edge list)
**Category**: Error Path

- **When** "A" greps "needle" with `path` `notes\x00a`
- **Then** the call is refused with a message naming the NUL byte
- **And** one `path.access_denied` entry records reason `path_invalid`

##### Scenario: S-1.12 a single-file path searches only that file

**Traces to**: User Story 1, Acceptance Scenario 6 (edge list)
**Category**: Alternate Path

- **When** "A" greps "needle" with `path` `<EXT>/ext/notes.txt`
- **Then** the result contains exactly `<EXT>/ext/notes.txt`

#### US-2 — the walk withholds what reading refuses

##### Scenario: S-2.1 protected entries met during a walk are invisible

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Error Path

- **Given** a test `$OMNIPUS_HOME` whose `master.key`, `config.json` and `agents/<other>/x.md`
  contain "needle", and whose `sessions/s1.jsonl` contains "needle"
- **When** "A" greps "needle" with `path` `$OMNIPUS_HOME`
- **Then** the result contains `sessions/s1.jsonl`'s absolute path
- **But** no match names `master.key`, `config.json` or anything under `agents/<other>`

##### Scenario: S-2.2 registry skill instruction files never match; helpers do

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Error Path

- **Given** `$OMNIPUS_HOME/skills/demo/SKILL.md`, `AGENT.md` and `AGENTS.md` and
  `$OMNIPUS_HOME/skills/demo/scripts/needle-helper.py`, all containing "needle"
- **When** "A" greps "needle" with `path` `$OMNIPUS_HOME/skills`
- **Then** the result contains `scripts/needle-helper.py` (absolute) by name and content
- **But** none of `SKILL.md`, `AGENT.md`, `AGENTS.md`, by name or content

##### Scenario: S-2.3 a path naming a registry instruction file is refused like read_file

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Error Path

- **When** "A" greps "needle" with `path` `$OMNIPUS_HOME/skills/demo/SKILL.md`
- **Then** the call is refused
- **And** `read_file` of the same path is refused too

##### Scenario: S-2.4 project-shelf skill instruction files stay searchable

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** a project-shelf skill instruction file inside `<MNT>` (project shelf as ADR-072
  D10.3 defines it) containing "needle"
- **When** "A" greps "needle" with no `path`
- **Then** that file is in the result
- **And** `read_file` of it succeeds

##### Scenario Outline: S-2.5 agent metadata files never match

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Error Path

- **Given** `<WS>/agents/a1/<meta>` whose name or content contains "needle"
- **When** "A" greps "needle" with no `path`
- **Then** no match names `agents/a1/<meta>`
- **And** `read_file` of `agents/a1/<meta>` is refused

**Examples**:

| meta |
|---|
| `SOUL.md` |
| `HEARTBEAT.md` |
| `AGENT.md` |
| `MEMORY.md` |
| `soul.md` (case-insensitive match) |

##### Scenario: S-2.6 symlinks met during a walk are not followed

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Edge Case

- **Given** `<WS>/link-out` → `<EXT>/ext` and `<WS>/link-in` → `<WS>/notes`
- **When** "A" greps "needle" with no `path`
- **Then** no match comes from content behind `link-out` or `link-in`
- **And** a grep for "link" returns the entries `link-out` and `link-in` as name matches

##### Scenario: S-2.7 a symlink given as `path` resolves like read_file

**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** `<WS>/link-out` → `<EXT>/ext`
- **When** "A" greps "needle" with `path` `link-out`
- **Then** the result contains `<EXT>/ext/notes.txt` as an absolute path
- **And** `list_directory` of `link-out` lists `notes.txt`

#### US-3 — Auto-approve

##### Scenario Outline: S-3.1 policy × Auto × location for the three reading tools

**Traces to**: User Story 3, Acceptance Scenarios 1–5
**Category**: Happy Path

- **Given** `<tool>` set to `<policy>` for "A" and Auto-approve `<auto>`
- **When** "A" calls `<tool>` on `<location>` through a real agent turn
- **Then** the outcome is `<outcome>`

**Examples**: Dataset DS-2 (every row).

##### Scenario: S-3.2 Auto-approved read on an Ask policy succeeds through the real loop (D8)

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** `read_file`, `list_directory`, `grep` set to Ask, Auto-approve on
- **And** the call is dispatched by the agent loop, which attaches its Auto pin
- **When** "A" calls `read_file` on `<EXT>/ext/notes.txt`
- **Then** the call returns the file's content — not a "moved" / re-check refusal
- **And** a `tool.auto_approved` audit entry is written for it

##### Scenario: S-3.3 Auto-approved image inspection outside the workspace succeeds

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Alternate Path

- **Given** Ask + Auto-approve on and a PNG at `<EXT>/pic.png`
- **When** "A" calls `read_file` on it
- **Then** the image is inspected (the second authorisation step of image inspection also
  passes)

##### Scenario: S-3.4 a run-class pin is not re-checked; an empty-class pin still is

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Edge Case

- **Given** a pin of class `runs` with no paths
- **When** the re-check runs for a read outside the workspace
- **Then** it passes
- **And** a pin with no class and no paths still fails the re-check

##### Scenario: S-3.5 write-tool pin still catches a swapped target (RUNS-IF regression)

**Traces to**: User Story 3, Acceptance Scenario 7
**Category**: Error Path

- **Given** `write_file` on Ask, Auto-approve on, target `<WS>/out/x.txt` approved by Auto
- **And** `<WS>/out` is replaced by a symlink to `<EXT>` after the decision and before the write
- **When** the write executes
- **Then** it is refused as moved; nothing is written under `<EXT>`

##### Scenario Outline: S-3.6 write and send keep asking outside the workspace

**Traces to**: User Story 3, Acceptance Scenario 8
**Category**: Alternate Path

- **Given** `<tool>` on Ask, Auto-approve on
- **When** "A" calls `<tool>` with a path in `<EXT>`
- **Then** an approval card appears

**Examples**:

| tool |
|---|
| `write_file` |
| `edit_file` |
| `append_file` |
| `send_file` |

##### Scenario: S-3.7 "Never auto-approve" and a per-chat Auto off still ask

**Traces to**: User Story 3, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** `read_file` on Ask and Auto-approve on globally
- **When** "A", marked "Never auto-approve", reads `<EXT>/ext/notes.txt`
- **Then** an approval card appears
- **And** the same happens for an agent without the mark in a chat whose Auto-approve toggle
  is off

#### US-4 — the Judge's review turns

##### Background (US-4)

- **Given** the Judge in a review turn (read-confined) of workspace "W", with the shipped
  Judge seed (`read_file`, `list_directory`, `grep` allowed)

##### Scenario Outline: S-4.1 read-confined matrix

**Traces to**: User Story 4, Acceptance Scenarios 1, 2
**Category**: Happy Path

- **When** the Judge calls `<tool>` with `<path>`
- **Then** the outcome is `<outcome>`

**Examples**: Dataset DS-3 (every row, each tool).

##### Scenario: S-4.2 read_file of a mount file by absolute path now succeeds

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **When** the Judge calls `read_file` on `<MNT>/src/b.md`
- **Then** the content is returned (today: refused)

##### Scenario: S-4.3 outside workspace and mounts is refused as read-confined

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Error Path

- **When** the Judge calls `grep` with `path` `<EXT>`
- **Then** the call is refused with a message containing "read-confined"
- **And** one `path.access_denied` entry records reason `outside_workspace`

##### Scenario: S-4.4 an operator allow-path pattern cannot reopen transcripts

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Error Path

- **Given** an operator AllowReadPaths pattern matching `$OMNIPUS_HOME/sessions/.*`
- **When** the Judge calls `read_file` on `$OMNIPUS_HOME/sessions/s1.jsonl`
- **Then** it is refused as read-confined

##### Scenario: S-4.5 a send from a mount stays refused

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Error Path

- **Given** a read-confined policy (resolution level; the Judge's seed denies `send_file`)
- **When** a send-operation path resolution targets `<MNT>/src/b.md`
- **Then** it is refused as read-confined

##### Scenario: S-4.6 a swapped folder inside a mount cannot redirect a Judge read

**Traces to**: User Story 4, Acceptance Scenario 5
**Category**: Edge Case

- **Given** the Judge resolved `<MNT>/src/b.md` for reading
- **And** `<MNT>/src` is then replaced by a symlink to `<EXT>/ext` holding a `b.md`
- **When** the read is performed
- **Then** it fails; the content of `<EXT>/ext/b.md` is never returned

##### Scenario: S-4.7 a symlink from the workspace into a mount is admitted

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Alternate Path

- **Given** `<WS>/to-mount` → `<MNT>/src`
- **When** the Judge calls `list_directory` on `to-mount`
- **Then** `b.md` is listed

##### Scenario: S-4.8 non-review turns are unaffected

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** agent "A" in an ordinary turn
- **When** "A" calls `read_file` on `<EXT>/ext/notes.txt`
- **Then** it succeeds (read-confinement applies only to the Judge's review turns)

##### Scenario: S-4.9 Plan Supervisor grep follows the general rule (D10 accepted risk)

**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Edge Case

- **Given** the Plan Supervisor with its shipped seed (`grep` allowed, `read_file` denied)
- **When** it greps "needle" with `path` `$OMNIPUS_HOME/sessions`
- **Then** the search runs and returns `s1.jsonl`'s match
- **But** a grep with `path` `$OMNIPUS_HOME/master.key` is refused

#### US-5 — audit

##### Scenario: S-5.1 one roots-searched entry per search

**Traces to**: User Story 5, Acceptance Scenarios 1, 3
**Category**: Happy Path

- **Given** 300 files under `<EXT>` each containing "needle"
- **When** "A" greps "needle" with `path` `<EXT>`
- **Then** exactly one `path.search_roots` entry is written for the call
- **And** its details are `roots` = [`<EXT>` realpath] and `path_arg` = the raw argument
- **And** no `file_op` read entry is written for any matched file

##### Scenario: S-5.2 default search lists workspace and mounts as roots

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Alternate Path

- **When** "A" greps "needle" with no `path`
- **Then** the entry's `roots` are [`<WS>` realpath, `<MNT>` realpath] and `path_arg` is `""`

##### Scenario: S-5.3 refused path writes a denial entry and no roots entry

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Error Path

- **When** "A" greps with `path` `$OMNIPUS_HOME/master.key`
- **Then** one `path.access_denied` entry (tool `grep`, reason `carve_out`) is written
- **And** no `path.search_roots` entry is written

##### Scenario: S-5.4 roots entry carries no term and no content

**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Edge Case

- **When** "A" greps "s3cr3t-term" with `path` `<EXT>`
- **Then** the serialized `path.search_roots` entry does not contain "s3cr3t-term" nor any
  matched line

##### Scenario: S-5.5 the event name is recognised

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Edge Case

- **When** the audit layer validates the event name `path.search_roots`
- **Then** it is recognised as valid and matches `^[a-z_.]+$`

##### Scenario: S-5.6 invalid pattern, busy engine: no roots entry

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Error Path

- **When** "A" greps with `regex: true` and pattern `(`
- **Then** the call returns an invalid-pattern error and no `path.search_roots` entry
- **And** a call refused as busy writes no `path.search_roots` entry

##### Scenario: S-5.7 the Audit Log shows and filters the new entry

**Traces to**: User Story 5, Acceptance Scenarios 1, 4
**Category**: Happy Path

- **Given** the audit log contains a `path.search_roots` entry
- **When** the operator opens Settings → Security → Audit Log
- **Then** the entry renders with its event name, and "path.search_roots" is selectable in the
  event filter
- **And** expanding it shows `roots` and `path_arg`

#### US-6 — truthful text

##### Scenario: S-6.1 descriptions meet the text requirements

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **When** the three descriptions are read from the shipped build
- **Then** each requirement DR-G1..DR-G8 / DR-R1..DR-R3 (FR-024) holds

##### Scenario: S-6.2 generated reference is current

**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Happy Path

- **When** `scripts/check-docs-reference.sh` runs
- **Then** it exits 0
- **And** the rows for `read_file` and `list_directory` end with "Runs"

##### Scenario: S-6.3 Tools & Permissions marker

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Happy Path

- **Given** `read_file` set to Ask
- **When** the operator opens the agent's Tools & Permissions panel
- **Then** the marker next to `read_file` reads "Auto: runs"

##### Scenario: S-6.4 user docs no longer promise asking for reads

**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Error Path

- **When** docs-verifier audits `docs/tools.md` and `docs/security.md`
- **Then** neither says `read_file` of a path outside the workspace asks under Auto
- **And** `docs/security.md` says to deny `read_file`, `grep` and `bash` to confine an agent's
  reads, and names the personal-secret-folder residual (issue #921)

#### US-7 — Windows

##### Scenario Outline: S-7.1 Windows path forms

**Traces to**: User Story 7, Acceptance Scenarios 1–3
**Category**: Edge Case

- **Given** Windows
- **When** "A" greps "needle" with `path` `<path>`
- **Then** the outcome is `<outcome>`
- **And** `read_file`/`list_directory` on `<path>` give the matching admitted/refused outcome

**Examples**: Dataset DS-4 rows W1–W8.

##### Scenario: S-7.2 backslash is a file-name character on Linux and macOS

**Traces to**: User Story 7, Acceptance Scenario 4
**Category**: Edge Case

- **Given** Linux or macOS and a folder literally named `notes\sub` in `<WS>`
- **When** "A" greps "needle" with `path` `notes\sub`
- **Then** that folder is searched and match paths begin `notes\sub/`

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | `pkg/tools` path/scope parsing, hit rendering, per-file gates, `RecheckAutoPin`, class table; `pkg/audit` event registry | logic in isolation, real temp dirs |
| Integration | `GrepTool`/`ReadFileTool`/`ListDirTool` `Execute` with real `ResolveTurnFSPolicy`, real mounts, real audit logger; `ResolvePath` read-confined branch | parity and gates end to end inside the tools layer |
| Loop | `pkg/agent` real loop → pin → tool (MIN-004) | D8 behaviour as the product runs it |
| Frontend | vitest on `AuditLogViewer`, `ToolPolicyEditor` | the two UI surfaces |
| Windows | `_windows_test.go` in `pkg/tools` on `windows-latest` | DS-4 |
| E2E / UAT | real agent session (UAT provider `openrouter` + `z-ai/glm-5.3-flash`) | US-1/US-3/US-5 reachability |

### Test Implementation Order

Test names are placeholders; qa-lead names the real tests in RED. Every oracle comes from
this spec's datasets, never from running the current code.

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | `TestReadBoundary_ParityMatrix` | Integration | S-1.1, S-1.2, S-1.3, S-1.7, S-1.8, S-1.12 | DS-1 table-driven; same agent/turn; compares grep vs read_file vs list_directory admission, reason, hit form |
| 2 | `TestReadBoundary_GrepRefusesProtected` | Integration | S-1.4, S-5.3 | DS-1 protected rows + audit reason |
| 3 | `TestReadBoundary_DefaultAreaAndShorthand` | Integration | S-1.5, S-1.6 | D5 unchanged; shorthand unchanged |
| 4 | `TestReadBoundary_MissingPathIsError` | Integration | S-1.9 | error, no roots row |
| 5 | `TestReadBoundary_VolumeRootBounded` | Integration | S-1.10 | injected limit; truncated `max_files`; no carve-out hit |
| 6 | `TestReadBoundary_NULRefusedAndAudited` | Unit | S-1.11 | `path_invalid` row |
| 7 | `TestReadBoundary_WalkWithholdsCarveOuts` | Integration | S-2.1 | test `$OMNIPUS_HOME`; sessions visible, secret set not |
| 8 | `TestReadBoundary_SkillsRegistryGate` | Integration | S-2.2, S-2.3, S-2.4 | registry withheld by name+content; path refused; project shelf visible |
| 9 | `TestReadBoundary_MetadataGuardInWalk` | Integration | S-2.5 | five examples |
| 10 | `TestReadBoundary_WalkSymlinksNotFollowed` + `TestReadBoundary_SymlinkPathResolvesLikeRead` | Integration | S-2.6, S-2.7 | replaces `TestFileGrep_SymlinkConfinement`'s agent-side role (the engine test stays) |
| 11 | `TestAutoPin_RunsClassSkipsRecheck` | Unit | S-3.4 | class `runs` passes; zero class fails; `runs_if_args` unchanged |
| 12 | `TestAutoApproveClasses_ReadToolsRun` | Unit | S-3.1 (classification) | MV-5 golden copy updated |
| 13 | `TestReadBoundary_AutoAskRunsThroughLoop` | Loop | S-3.2, S-3.3, S-3.5 | MIN-004: real loop pin; read/list/grep inside+outside succeed; secret refused; image re-authorisation; write_file swap refused |
| 14 | `TestReadBoundary_AutoPolicyMatrix` | Loop | S-3.1, S-3.6, S-3.7 | DS-2 |
| 15 | `TestResolvePath_ReadConfinedMountMatrix` | Integration | S-4.1, S-4.2, S-4.3, S-4.7 | DS-3 × three tools |
| 16 | `TestResolvePath_ReadConfinedIgnoresAllowPatterns` | Integration | S-4.4 | JUDGE-FR-060 guard |
| 17 | `TestResolvePath_ReadConfinedSendStaysConfined` | Integration | S-4.5 | FSOpSend |
| 18 | `TestResolvePath_ReadConfinedMountAncestorSwap` | Integration | S-4.6 | resolve, swap, read → error |
| 19 | `TestReadBoundary_UnconfinedTurnUnaffected` | Integration | S-4.8 | |
| 20 | `TestReadBoundary_PlanSupervisorGrepReach` | Integration | S-4.9 | pins the D10 accepted risk |
| 21 | `TestGrepAudit_SearchRootsRow` | Integration | S-5.1, S-5.2, S-5.4, S-5.6 | one row, exact detail keys, no term/content, no row on pattern error or busy |
| 22 | `TestAuditEventNames_SearchRootsRegistered` | Unit | S-5.5 | `IsValidEventName`; pattern (extends `pkg/audit/event_name_contract_test.go` coverage) |
| 23 | `AuditLogViewer` search-roots row test | vitest | S-5.7 | fallback badge, filter option, details |
| 24 | `TestGrepDescription_Requirements` (+ read_file/list_directory) | Unit | S-6.1 | asserts FR-024 required phrases present and forbidden phrases absent |
| 25 | docs reference gate (`scripts/check-docs-reference.sh`) | CI | S-6.2 | exit 0 |
| 26 | `ToolPolicyEditor` marker test | vitest | S-6.3 | `auto_approve: runs` → "Auto: runs" for read_file |
| 27 | docs-verifier audit | Docs | S-6.4 | against FR-026 |
| 28 | `TestReadBoundary_Windows*` | Windows | S-7.1 | DS-4, run on `windows-latest` (FR-031) |
| 29 | `TestReadBoundary_BackslashLiteralUnix` | Unit (unix build tag) | S-7.2 | |
| 30 | UAT lane "read boundary" | UAT | S-1.2, S-3.2, S-5.7 | real session; evidence checked by uat-validator |

### Test Datasets

Expected outcomes come from decisions D1–D10, ADR-081's D4 amendment and ADR-092's
2026-09-26 notes. "Admitted" = the call proceeds; "refused(r)" = refused with audit reason r.

#### Dataset DS-1: Parity matrix (unconfined agent "A", policy Allow)

| # | Path kind | Example `path` | grep outcome | grep match-path form | read_file / list_directory outcome | Traces to | Notes |
|---|---|---|---|---|---|---|---|
| 1 | workspace-relative | `notes` | admitted | `notes/a.md` | admitted | S-1.1 | unchanged |
| 2 | absolute inside workspace | `<WS>/notes` | admitted | `notes/a.md` (relative) | admitted | S-1.8 | location decides |
| 3 | mount-name shorthand | `docs-mount/src` | admitted | `docs-mount/src/b.md` | n/a — grep-only shorthand (read_file treats it as `<WS>/docs-mount/src`, not found) | S-1.6 | unchanged |
| 4 | absolute in a mount | `<MNT>/src` | admitted | `<MNT>/src/b.md` (absolute) | admitted | S-1.7 | never shorthand |
| 5 | absolute outside everything | `<EXT>` | admitted | `<EXT>/ext/notes.txt` | admitted | S-1.2 | D1 |
| 6 | `..` escape outside | `../…/<EXT>` | admitted | absolute, same as row 5 | admitted | S-1.3 | D1 |
| 7 | `..` staying inside | `notes/../notes` | admitted | `notes/a.md` | admitted | S-1.1 | relative, via single decision |
| 8 | `..` landing in a mount | `../…/<MNT>/src` | admitted | absolute | admitted | S-1.1 | never shorthand |
| 9 | secret-set file | `$OMNIPUS_HOME/master.key` | refused(`carve_out`) | — | refused(`carve_out`) | S-1.4 | |
| 10 | secret-set dir | `$OMNIPUS_HOME/backups` | refused(`carve_out`) | — | refused(`carve_out`) | S-1.4 | |
| 11 | another agent's home | `$OMNIPUS_HOME/agents/<other>` | refused(`carve_out`) | — | refused(`carve_out`) | S-1.4 | |
| 12 | another workspace | `$OMNIPUS_HOME/workspaces/<other>/work` | refused(`carve_out`) | — | refused(`carve_out`) | S-1.4 | |
| 13 | Omnipus home itself | `$OMNIPUS_HOME` | admitted; carve-outs withheld | absolute | admitted (list_directory output unchanged by #920) | S-2.1 | D10 reach |
| 14 | registry skill instruction file | `$OMNIPUS_HOME/skills/demo/SKILL.md` | refused | — | read_file refused (points to the Skill tool) | S-2.3 | ADR-072 D10.3 |
| 15 | registry skills folder | `$OMNIPUS_HOME/skills` | admitted; `SKILL.md`/`AGENT.md`/`AGENTS.md` withheld | absolute | list_directory unchanged by #920 | S-2.2 | grep stricter on names (Ambiguity 1) |
| 16 | project-shelf instruction file | project shelf in `<MNT>` | admitted, matches | per location | admitted | S-2.4 | never read-denied |
| 17 | metadata file in a searched root | `agents/a1/SOUL.md` under `<WS>` | withheld (no hit) | — | read_file refused; list_directory unchanged | S-2.5 | grep stricter on names |
| 18 | symlink given as `path`, target outside | `link-out` | admitted, searches target | absolute (target location) | admitted | S-2.7 | location decides |
| 19 | symlink met during walk | `<WS>/link-out` inside default area | not followed; name-match only | `link-out` | n/a | S-2.6 | unchanged |
| 20 | embedded NUL | `notes\x00a` | refused(`path_invalid`) | — | refused(`path_invalid`) | S-1.11 | |
| 21 | nonexistent outside | `<EXT>/does-not-exist` | error naming path, no roots row | — | error (not found) | S-1.9 | never "0 matches" |
| 22 | single file | `<EXT>/ext/notes.txt` | admitted, that file only | absolute | admitted | S-1.12 | |
| 23 | volume root | `/` | admitted; bounded (`max_files` with injected limit) | absolute | admitted | S-1.10 | D7 |

#### Dataset DS-2: Auto-approve matrix (tools `read_file`, `list_directory`, `grep`, each row applies to all three)

| # | Policy | Auto-approve | Location | Expected outcome | Traces to |
|---|---|---|---|---|---|
| 1 | allow | off | inside `<WS>` | runs, no card | S-3.1 |
| 2 | allow | off | `<EXT>` | runs, no card | S-3.1 |
| 3 | allow | on | `<EXT>` | runs, no card | S-3.1 |
| 4 | ask | off | inside `<WS>` | approval card | S-3.1 |
| 5 | ask | off | `<EXT>` | approval card | S-3.1 |
| 6 | ask | on | inside `<WS>` | runs, no card; `tool.auto_approved` entry | S-3.1, S-3.2 |
| 7 | ask | on | `<EXT>` | runs, no card; `tool.auto_approved` entry (was: card for read_file/list_directory) | S-3.1, S-3.2 |
| 8 | ask | on | `$OMNIPUS_HOME/master.key` | refused (`carve_out`), no card | S-3.1 |
| 9 | deny | off | inside `<WS>` | refused, no card | S-3.1 |
| 10 | deny | on | `<EXT>` | refused, no card | S-3.1 |
| 11 | ask | on, agent "Never auto-approve" | `<EXT>` | approval card | S-3.7 |
| 12 | ask | on globally, chat toggle off | `<EXT>` | approval card | S-3.7 |
| 13 | ask | on | `<EXT>/pic.png` (read_file only) | image inspected | S-3.3 |
| R1 | `write_file` ask | on | `<WS>/out/x.txt`, swapped to `<EXT>` mid-call | refused as moved; nothing written | S-3.5 |
| R2 | `write_file`/`edit_file`/`append_file`/`send_file` ask | on | `<EXT>` | approval card | S-3.6 |
| R3 | `send_file` ask | on | inside `<WS>` | runs, no card | S-3.6 (control) |
| R4 | pin class `runs`, no paths | — | `<EXT>` | re-check passes | S-3.4 |
| R5 | pin class empty, no paths | — | `<EXT>` | re-check fails (fail-closed) | S-3.4 |

#### Dataset DS-3: Judge review turn (read-confined), each row for `grep`, `read_file`, `list_directory` unless noted

| # | Path | Expected | Traces to | Notes |
|---|---|---|---|---|
| 1 | no `path` (grep) / `.` (list) | admitted: workspace + mounts | S-4.1 | unchanged |
| 2 | `notes/a.md` / `notes` | admitted | S-4.1 | |
| 3 | `docs-mount/src` (grep) | admitted | S-4.1 | shorthand |
| 4 | `<MNT>/src/b.md` / `<MNT>/src` | admitted | S-4.2 | NEW for read_file/list_directory |
| 5 | `<EXT>/ext/notes.txt` / `<EXT>` | refused(`outside_workspace`), text contains "read-confined" | S-4.3 | |
| 6 | `..` escape to `<EXT>` | refused(`outside_workspace`) | S-4.1 | |
| 7 | `..` landing in `<MNT>` | admitted | S-4.1 | |
| 8 | `<WS>/link-out` → `<EXT>/ext` | refused(`outside_workspace`) | S-4.1 | |
| 9 | `<WS>/to-mount` → `<MNT>/src` | admitted | S-4.7 | |
| 10 | `$OMNIPUS_HOME/master.key` | refused(`carve_out`) | S-4.1 | |
| 11 | `$OMNIPUS_HOME/sessions/s1.jsonl` with an AllowReadPaths pattern covering it (read_file) | refused (read-confined) | S-4.4 | no regex injection |
| 12 | send-op resolution of `<MNT>/src/b.md` | refused (read-confined) | S-4.5 | FSOpSend |
| 13 | mount ancestor swapped after resolve (read_file) | read fails | S-4.6 | anchored handle |
| 14 | Plan Supervisor, grep `$OMNIPUS_HOME/sessions` | admitted (accepted risk D10) | S-4.9 | not read-confined |

#### Dataset DS-4: Windows (runs on `windows-latest`)

| # | `path` | Expected grep outcome | Parity | Traces to |
|---|---|---|---|---|
| W1 | `C:\…\<EXT>` (backslashes) | admitted as absolute; matches written `C:/…/ext/notes.txt` | read_file admitted | S-7.1 |
| W2 | `C:/…/<EXT>` (forward slashes) | same as W1 | same | S-7.1 |
| W3 | `\\nonexistent-host.invalid\share\x` | error naming the path; never "must be relative"; never mount shorthand | read_file errors too | S-7.1 |
| W4 | `notes\sub` | workspace folder `notes/sub`; matches `notes/sub/…` | read_file admitted | S-7.1 |
| W5 | `docs-mount\src` | mount shorthand; matches `docs-mount/src/b.md` | n/a (grep-only) | S-7.1 |
| W6 | `..\..\<rel-to-EXT>` | same as W1 | same | S-7.1 |
| W7 | `C:ext` (drive-relative) | same admitted/refused outcome as read_file | parity oracle | S-7.1 |
| W8 | `C:\` (volume root) with injected low limit | admitted; truncated `max_files` | read_file/list admitted | S-7.1 |

#### Dataset DS-5: Unchanged limits (D7)

| # | Limit | Value | Traces to |
|---|---|---|---|
| 1 | concurrent walk slots (shared with Library bar) | 2 | S-1.10 |
| 2 | busy wait before refusing | 2 s | S-5.6 |
| 3 | deadline | 10 s | S-1.10 |
| 4 | files visited | 50,000 | S-1.10 |
| 5 | total matches / per file | 1,000 / 50 | S-1.10 |
| 6 | per-file content cap | 4 MiB | S-1.10 |
| 7 | tool output cap | 64,000 characters | S-1.10 |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| grep confined to own workspace for any `path` | `pkg/tools/grep_ownworkspace_test.go::TestGrepTool_OwnWorkspaceOnly` | Yes — rewrite to DS-1 rows 9–12 (other workspace still unreachable) | superseded rule; keep the cross-workspace refusal half |
| `..` / absolute refused | `pkg/tools/grep_test.go::TestGrepTool_ArgValidation` | Yes — absolute/`..` cases now admitted | NUL case stays |
| scope normalisation | `pkg/tools/grep_scope_normalize_test.go` | No — must stay green | relative spellings unchanged |
| engine symlink confinement | `pkg/filegrep/integration_test.go::TestFileGrep_SymlinkConfinement` | No — must stay green | engine unchanged |
| read-confined refuses outside WorkDir | `pkg/tools/resolvepath_readconfined_adr084_test.go::TestResolvePath_ReadConfinedRefusesOutsideWorkdir`, `…RefusesListAndSendToo`, `…DoesNotReopenCarveOuts` | Yes — mount cases flip to admitted for read/list only; send and non-mount cases unchanged | re-read each assertion; no silent weakening |
| read-confined only for system agents | `pkg/fspolicy/readconfined_adr084_test.go` | No | |
| Auto classification golden copy | `pkg/gateway/auto_approve_classification_test.go` | Yes — tally update; check 4 still passes after classifier deletion | |
| existing AutoPin literals | `pkg/tools/auto_approve_test.go`, `pkg/tools/browser/screenshot_autoapprove_test.go` | No — must stay green unchanged | zero-value class fail-closed |
| human Library search | `pkg/gateway` `rest_library_files_search` tests | No — must stay green unchanged | FR-028 |
| grep policy roster | `TestGrep_PolicyAllTiersAndDriftBackfill`, `TestGrepTool_ExecuteAndPolicy` | No | |
| docs reference | `cmd/docsref/main_test.go`, `scripts/check-docs-reference.sh` | regenerate | |

---

## Functional Requirements

- **FR-001**: `grep` MUST decide every non-empty `path` that is not a mount-name shorthand
  (FR-002) with the same single read decision `read_file`/`list_directory` use — relative,
  absolute and `..` paths alike — giving the same admitted/refused outcome for the same agent,
  turn and path (D1; ADR-081 D4 amendment). `grep.go::validateGrepScope`'s absolute-path and
  `..` refusals are removed; the NUL pre-check stays.
- **FR-002**: The mount-name shorthand MUST apply only to a relative `path` with no `..`
  segment whose first segment exactly equals a mount name; an absolute path or a path with
  `..` MUST never be read as a mount name. On Windows `\` MUST count as a separator for this
  test.
- **FR-003**: With no `path` (or `""`), `grep` MUST search the workspace and every mount on it,
  unchanged (D5).
- **FR-004**: Match paths MUST be workspace-relative when the searched location resolves inside
  the workspace; mount-name form for the shorthand; otherwise absolute with forward slashes on
  every platform, and each MUST be accepted by `read_file` unchanged.
- **FR-005**: The secret set, other agents' homes and other workspaces MUST be refused as a
  `path` and withheld (name and content) from every walk, for every root type.
- **FR-006**: Skills-registry instruction files (`SKILL.md`, `AGENT.md`, `AGENTS.md`) MUST be
  refused as a `path` and never produce a name or content match during a walk; project-shelf
  instruction files MUST remain searchable (ADR-072 D10.3; ADR-081 gate 2 as corrected).
- **FR-007**: Agent metadata files (per `metadataFileMatch`: `SOUL.md`, `HEARTBEAT.md`,
  `AGENT.md`, legacy `MEMORY.md` under `agents/<id>/`, case-insensitive) MUST never produce a
  `grep` name or content match (ADR-081 gate 3).
- **FR-008**: Symlinks met during a walk MUST NOT be followed; a symlink named by `path` MUST be
  resolved and judged like `read_file` judges it.
- **FR-009**: No `/`-prefix absolute-path test may remain in `grep`; Windows drive, forward-slash
  drive and UNC forms MUST be treated as absolute; a backslash MUST be a file-name character on
  Linux/macOS.
- **FR-010**: A `path` that does not exist or cannot be opened MUST return an error naming it,
  never a zero-match success.
- **FR-011**: The D7 limits (DS-5) MUST apply unchanged to every widened call.
- **FR-012**: In a read-confined turn, `grep`, `read_file` and `list_directory` MUST admit the
  workspace and its mounts and refuse everything else with a message containing
  "read-confined" (D6, D10).
- **FR-013**: In a read-confined turn, operator allow-path regex grants MUST NOT widen what is
  admitted (ADR-081 D6 correction 1).
- **FR-014**: In a read-confined turn, a mount read MUST go through a handle anchored at the
  mount, so a folder swapped after resolution cannot redirect it outside the mount (ADR-081 D6
  correction 2).
- **FR-015**: In a read-confined turn, the send operation MUST stay confined to the workspace
  (ADR-081 D6 correction 3).
- **FR-016**: The Plan Supervisor MUST NOT be read-confined; its `grep` follows FR-001 (D10).
  Code comments that call the Plan Supervisor read-confined
  (`resolvepath.go` near `WithReadConfined` and in the read-confined branch) and the
  PlanSupervisor seed comment citing the superseded FR-020 confinement
  (`seed_system.go::systemAgentSeed`) MUST be corrected to match.
- **FR-017**: `read_file` and `list_directory` MUST be classified "runs" under Auto-approve,
  like `grep`; under Ask with Auto-approve on they MUST run inside and outside the workspace;
  the secret set MUST still be refused (D2, D3; ADR-092 D9 amendment).
- **FR-018**: A pin of class "runs" MUST skip the mid-call re-check; a pin with an empty class
  MUST still be re-checked; RUNS-IF pins MUST re-check exactly as before (D8; ADR-092
  correction note). The now-unreachable `ReadFileTool.AutoApproveVerdict` and
  `ListDirTool.AutoApproveVerdict` and their classifier assertions MUST be deleted.
- **FR-019**: Deny MUST never be overridden; Ask with Auto-approve off, an agent marked "Never
  auto-approve" and a chat with Auto-approve off MUST still ask.
- **FR-020**: A refused `grep` `path` MUST write one `path.access_denied` entry with tool
  `grep` and the same reason `read_file` records for that path.
- **FR-021**: Every `grep` call whose search ran MUST write exactly one `path.search_roots`
  entry (decision `allow`, details `roots` and `path_arg` only, no pattern, no content);
  a call refused at resolution, rejected for an invalid pattern/glob, or refused as busy MUST
  NOT write one. The event name MUST be registered in `pkg/audit/audit.go::validEventNames`.
- **FR-022**: `grep` MUST NOT write per-file read entries; `read_file`/`list_directory` audit
  behaviour MUST be unchanged.
- **FR-023**: The Audit Log screen MUST render and filter `path.search_roots` entries with the
  existing components.
- **FR-024**: Tool descriptions (written by prometheus-prompt-engineer) MUST meet:
  - `grep` — DR-G1 no claim that search is limited to the workspace and mounts when `path` is
    given ("never anywhere else" and the "never reachable" sentence are removed); DR-G2 states
    the default area is the workspace plus its mounted folders; DR-G3 states `path` may be
    workspace-relative, a mount name (optionally with a sub-path), or an absolute path,
    reaching what `read_file` can read; DR-G4 states Omnipus's protected files and other
    agents' and workspaces' files are never reachable; DR-G5 states the match-path rule
    (workspace-relative inside the workspace, absolute elsewhere, usable with `read_file`);
    DR-G6 states symlinks met while searching are not followed; DR-G7 keeps every limit
    rendered from the constants (no hard-coded numbers); DR-G8 does not mention approval,
    Auto-approve or policy mechanics.
  - `read_file`, `list_directory` — DR-R1 no statement or implication that reads are limited
    to the workspace or that reads outside it ask; DR-R2 existing accurate content kept;
    DR-R3 no approval mechanics.
  - All three: plain text, no emoji.
- **FR-025**: `docs/reference/built-in-tools.md` MUST be regenerated with `cmd/docsref`
  (labels "Runs" for `read_file`/`list_directory`; new descriptions; header text still
  correct for the remaining RUNS-IF tools) and `scripts/check-docs-reference.sh` MUST exit 0.
- **FR-026**: `docs/tools.md` and `docs/security.md` MUST be corrected per the Security
  section's table (drafted by the implementing lead, audited by docs-verifier), including
  "deny `grep` too" and the issue #921 residual; `docs/goals.md` SHOULD state the Judge's
  review area.
- **FR-027**: The Tools & Permissions marker for `read_file` and `list_directory` MUST read
  "Auto: runs" (from the class table; no contract change).
- **FR-028**: `pkg/gateway/rest_library_files_search.go` MUST be unchanged and its tests MUST
  pass unchanged.
- **FR-029**: No contract schema, REST route, WS frame, config key or tool MAY be added; no
  third policy layer.
- **FR-030**: `grep`'s reach MUST NOT depend on `read_file`'s policy (a `grep: allow`,
  `read_file: deny` agent gets FR-001's reach), and this MUST be documented (FR-026).
- **FR-031**: The Windows cases (DS-4) MUST execute on the `windows-latest` runner: a step in
  `.github/workflows/cross-platform.yml`'s Windows job MUST run the named tests
  (`-run '^TestReadBoundary_Windows'` over `./pkg/tools/`), so they are not skipped by the
  Linux-only test gates.

## Success Criteria

- **SC-001**: DS-1 parity: 23/23 rows produce the expected outcome; zero rows where grep and
  read_file disagree outside the rows marked grep-only or grep stricter.
- **SC-002**: DS-2: 18/18 rows pass through the real agent loop (not a hand-built pin).
- **SC-003**: DS-3: 14/14 rows pass for every applicable tool.
- **SC-004**: DS-4: 8/8 rows pass on `windows-latest` (the CI log shows the named tests ran, with
  `--- PASS` lines, not zero tests).
- **SC-005**: Exactly one `path.search_roots` entry per searched call in S-5.1 (300 matched
  files → 1 entry).
- **SC-006**: `scripts/check-docs-reference.sh` exit 0; `make verify-contracts` shows no drift
  (no contract change).
- **SC-007**: Full CI green, including the unchanged Library search tests and the regression
  table's updated pins; the 8-reviewer gate clean, with security-lead's SL-1..SL-10 recorded.
- **SC-008**: UAT: in a real session, an agent greps an absolute folder outside its workspace
  under Ask + Auto-approve on, gets matches with no card, and the Audit Log shows the
  roots-searched entry — confirmed by uat-validator.

---

## Reachability

- **Tool registration**: none new. The three tools are already registered and policy-covered:
  global ceiling `pkg/config/defaults.go` (`"read_file": "allow"`, `"list_directory":
  "allow"`, `"grep": "allow"`); ADR-090 role policies `pkg/coreagent/role_policies_adr090.go`
  (`commonWork` includes all three; the explicit grant list includes all three; `grep` in the
  structural list); system agents `pkg/coreagent/seed_system.go::systemAgentSeed` (Judge:
  all three allowed; Plan Supervisor: `grep` allowed only). Nothing in this change alters any
  policy value.
- **User-visible surfaces**:
  - agents' behaviour in chat (wider `grep`, no card for reads under Auto);
  - Settings → Security → Audit Log (`src/components/settings/AuditLogViewer.tsx`) — the new
    `path.search_roots` entries and `grep` `path.access_denied` entries;
  - Agents → Tools & Permissions and Tool Access — Global Policies
    (`src/components/shared/ToolPolicyEditor.tsx`) — "Auto: runs" marker;
  - the three tool descriptions agents read;
  - user docs `docs/tools.md`, `docs/security.md` (and `docs/goals.md`), and the generated
    `docs/reference/built-in-tools.md`.
- **Test plan execution**: RED — qa-lead writes tests 1–29 failing on `7efcc5d`-based code
  (CI on a tests-only commit is the red evidence). GREEN — backend-lead (`grep.go`,
  `resolvepath.go`, `auto_approve.go`, `filesystem.go` dead code, audit emitter and
  registration, `cross-platform.yml` step, docsref regeneration) and
  prometheus-prompt-engineer (descriptions) in parallel; frontend needs no code change
  unless test 23/26 exposes a gap. CHECK — qa-lead (other instance) audits. UAT lane (test 30)
  runs in a real session with `openrouter` + `z-ai/glm-5.3-flash`, checked by uat-validator.
  Then the 8-reviewer gate with security-lead's D9 check.

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) | Code site |
|---|---|---|---|---|
| FR-001 | US-1 | S-1.1, S-1.2, S-1.3 | 1 `TestReadBoundary_ParityMatrix` | `grep.go::validateGrepScope`, `grepRoots`; `resolvepath.go::ResolvePath` |
| FR-002 | US-1, US-7 | S-1.6, S-1.7, S-7.1 (W5) | 3, 28 | `grep.go::splitGrepScopeMount` |
| FR-003 | US-1 | S-1.5 | 3 `TestReadBoundary_DefaultAreaAndShorthand` | `grep.go::grepRoots` (no-scope branch) |
| FR-004 | US-1 | S-1.2, S-1.7, S-1.8, S-1.12 | 1 | `grep.go::resolveScopedRoot` (namePrefix), `renderGrepResult` |
| FR-005 | US-1, US-2 | S-1.4, S-2.1 | 2, 7 | `grep.go::guardCarveOuts`/`carveOutFS`; `fspolicy.IsCarveOut` |
| FR-006 | US-2 | S-2.2, S-2.3, S-2.4 | 8 `TestReadBoundary_SkillsRegistryGate` | `resolvepath.go::isSkillInstructionFileLeaf`, `classifySkillsGateCandidate`; new grep wrapper |
| FR-007 | US-2 | S-2.5 | 9 `TestReadBoundary_MetadataGuardInWalk` | `metadata_guard.go::metadataFileMatch`; new grep wrapper |
| FR-008 | US-2 | S-2.6, S-2.7 | 10 | `pkg/filegrep` walk (unchanged); `ResolvePath` for `path` |
| FR-009 | US-7 | S-7.1, S-7.2 | 28, 29 | `grep.go::validateGrepScope` (removed test), `ResolvePath` |
| FR-010 | US-1 | S-1.9 | 4 | `grep.go::grepScopeStatError` / resolution error path |
| FR-011 | US-1 | S-1.10 | 5 | `filegrep` limits (unchanged) |
| FR-012 | US-4 | S-4.1, S-4.2, S-4.3, S-4.7 | 15 | `resolvepath.go::resolveValidatedPath` read-confined branch |
| FR-013 | US-4 | S-4.4 | 16 | `resolvepath.go::ResolvePathAllowingPatterns` |
| FR-014 | US-4 | S-4.6 | 18 | `resolvepath.go::matchedAllowedRoot`, `newMountRootHandle` |
| FR-015 | US-4 | S-4.5 | 17 | same branch, `FSOpSend` arm |
| FR-016 | US-4 | S-4.8, S-4.9 | 19, 20 | `verifier_adjudication.go::dispatchTurn` (unchanged); comments in `resolvepath.go`, `seed_system.go` |
| FR-017 | US-3 | S-3.1, S-3.2, S-3.3 | 12, 13, 14 | `auto_approve.go::autoApproveClasses` |
| FR-018 | US-3 | S-3.4, S-3.5 | 11, 13 | `auto_approve.go::AutoPin`, `AutoPinForVerdict`, `RecheckAutoPin`; `filesystem.go` dead code |
| FR-019 | US-3 | S-3.1 (DS-2 rows 4, 5, 9–12), S-3.6, S-3.7 | 14 | policy compositor, loop (unchanged) |
| FR-020 | US-5 | S-1.4, S-1.11, S-4.3, S-5.3 | 2, 6, 15, 21 | `path_audit.go::emitPathAccessDenied` wired into `grep.go` |
| FR-021 | US-5 | S-5.1, S-5.2, S-5.4, S-5.5, S-5.6, S-1.9 | 21, 22, 4 | new emitter in `grep.go`/`path_audit.go`; `audit.go::validEventNames` |
| FR-022 | US-5 | S-5.1 | 21 | `grep.go` (no `emitFileReadAudit`) |
| FR-023 | US-5 | S-5.7 | 23 | `AuditLogViewer.tsx` (existing) |
| FR-024 | US-6 | S-6.1 | 24 | `GrepTool.Description`, `ReadFileTool.Description`, `ListDirTool.Description` |
| FR-025 | US-6 | S-6.2 | 25 | `cmd/docsref/main.go::toolRows`, `autoApproveLabel`, header |
| FR-026 | US-6 | S-6.4 | 27 | `docs/tools.md`, `docs/security.md`, `docs/goals.md` |
| FR-027 | US-6 | S-6.3 | 26 | `rest_tool_registry.go` (unchanged) → `ToolPolicyEditor.tsx` |
| FR-028 | — (non-behaviour) | regression table | existing Library search tests | `pkg/gateway/rest_library_files_search.go` (unchanged) |
| FR-029 | — (non-behaviour) | — | `make verify-contracts` (SC-006) | contracts (unchanged) |
| FR-030 | US-1 | S-4.9 (seed with read_file denied) | 20, 27 | policy compositor (unchanged); docs |
| FR-031 | US-7 | S-7.1 | 28 | `.github/workflows/cross-platform.yml` Windows job |

**Completeness check**: every FR-001..FR-031 has at least one test (FR-028/FR-029 by the
unchanged-suite and contract gates); every scenario S-1.1..S-7.2 appears in at least one row
above (S-1.11 via FR-020; S-1.12 via FR-004; S-2.x via FR-005..FR-008; S-3.x via
FR-017..FR-019; S-4.x via FR-012..FR-016; S-5.x via FR-020..FR-023; S-6.x via FR-024..FR-027;
S-7.x via FR-002/FR-009/FR-031).

---

## Assumptions

- Only the Judge's review turn sets read-confinement in production code (one
  `tools.WithReadConfined(callCtx, true)` call, in `verifier_adjudication.go::dispatchTurn`),
  verified by Grep at `7efcc5d`. If another read-confined caller appears before GREEN, D6
  applies to it too and security-lead re-checks SL-1..SL-3.
- `list_directory` does not apply the metadata guard today (`guardMetadataPath` is called with
  `"read"` only from `read_file`); this spec does not change that (Ambiguity 1).
- The existing Audit Log viewer builds its event filter from the loaded entries, so no SPA code
  change is needed for FR-023 (`AuditLogViewer.tsx`, "Live filter vocabulary" comment); test 23
  confirms it.
- Sections removed: none. "UI Screens", "Accessibility" and "Design-System Components" are
  kept with the explicit statement that nothing new is added.

## Clarifications

### 2026-09-26

- Q: Does D6 (read-confined agents) apply to the Plan Supervisor? → A: No. Only the Judge's
  review turns are read-confined in code; the Plan Supervisor stays unconfined and its `grep`
  follows D1 — accepted risk (interview D10).
- Q: New spec, or amend `unified-search-and-grep-spec.md`? → A: new spec; the old spec is
  FINAL and self-contained; dated pointers were added at each superseded clause (squad-lead
  dispatch, founder-delegated).
- Q: What about `grep: allow` with `read_file: deny`? → A: that agent gains open reads through
  search; documented; to confine reads, deny `grep` too; no third policy layer (squad-lead
  dispatch).
- Q: How are match paths written for an absolute `path`? → A: the resolved location decides:
  inside the workspace workspace-relative, anywhere else absolute; `..` and absolute paths
  never use the mount-name shorthand (squad-lead dispatch).
- Q: Does the new audit event need a contract change? → A: No; register the name in
  `validEventNames`; it matches `AuditEntry.yaml`'s pattern (squad-lead dispatch, verified).
- Q: Personal secret folders (`~/.ssh` etc.)? → A: out of scope, issue #921 (interview D4).
