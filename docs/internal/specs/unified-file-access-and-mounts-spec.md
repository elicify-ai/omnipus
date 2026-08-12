# Spec — unified file-access engine and workspace mounts

- **Implements:** [ADR-063](../architecture/ADR-063-unified-file-access-engine-and-mounts.md)
- **Requirements:** [file-access requirements 2026-08-12](file-access-requirements-2026-08-12.md)
- **Status:** Draft (pre-implementation)
- **Date:** 2026-08-12

---

## 0. Scope and the safety gate

Merges the app-layer and kernel-layer file rules into one authored ruleset with
two renderings, makes access decisions operation-aware, and adds workspace
mounts.

**Out of scope:** Windows kernel enforcement; egress; remote/networked mounts;
the Library UI (depends on this, ships after).

> **The gate was violated on its first day, and this is the record of that.**
> FR-3.1/FR-3.2 (the merged secret set, commit `6e7aece0`) landed BEFORE any
> baseline existed, because the baseline work and the foundation work were
> dispatched in parallel — which is precisely what "nothing else starts until
> this is green" forbids. It was caught by the agent writing the baseline, who
> could not record "today's behaviour" because today had already moved.
>
> **Retroactively verified rather than argued away.** The baseline tests were
> run against the pre-FR-3 commit in a separate worktree. Every delta between
> the two commits involves EXACTLY two paths — `config.json` and `cli.token`,
> both moving from reachable to denied. Nothing else changed: the whole
> confined/unrestricted matrix, the own-tree exception in both shapes, and the
> app-vs-kernel divergence table are identical either side. Those two paths are
> exactly what §6 lists as approved, so the violation produced no undetected
> regression.
>
> The lesson is about sequencing, not about this change: a gate that can be
> skipped by running work in parallel is not a gate. Later waves must not start
> until the baseline for what they touch is green.

**Non-negotiable gate, inherited from ADR-062 and restated because it is the
only thing standing between this change and a silent regression:** before the
unified engine becomes the decision path, the CURRENT behaviour of both layers
must be captured as tests that pass against today's code. The unified engine
then has to reproduce every one of them except the ones this spec explicitly
changes (§1 FR-2, FR-3). A behaviour that changes without a line in §6 is a bug,
not an improvement.

---

## 1. Functional requirements — the engine

### FR-1 — One authored policy

- **FR-1.1** A single per-turn value is the authority for both layers. It
  carries: work dir, mounts, the secret set, the read/exec model, and the
  carve-out roots with their per-root own-tree exception.
- **FR-1.2** `fspolicy.FSPolicy` is extended to be that value rather than a new
  parallel type. It is already per-turn, already realpath-resolved, already
  validated, and already the input to the mandatory chokepoint.

  > **This is not a new architecture — it is ADR-046's P3, specified and never
  > built.** `pkg/fspolicy`'s own package doc already states it: EffectiveFSPolicy
  > is "the one function of record consumed by both the app-layer path resolver
  > (ResolvePath, P1/P2) and the future per-child Landlock ruleset builder (P3)
  > — so the two enforcement backends can never drift apart". The drift this
  > spec exists to fix is precisely what happened while P3 stayed unbuilt.

- **FR-1.3** `sandbox.SandboxPolicy` is DERIVED from it by a single exported
  function. No other code may construct a kernel policy for a turn.
- **FR-1.3a** **The dependency direction is `pkg/sandbox` → `pkg/fspolicy`, and
  it is fixed, not a preference.** `pkg/tools` already imports `pkg/sandbox`, and
  `pkg/fspolicy` is deliberately a stdlib-only leaf for that reason. Any
  requirement that would make `fspolicy` import `sandbox` creates a cycle and
  must be rewritten, not worked around. See FR-3.1, which the first draft of this
  spec got backwards.
- **FR-1.4** A test MUST assert the derivation is total: for every field of the
  authored policy that has a kernel expression, changing it changes the derived
  `SandboxPolicy`. This is the guard against a field being added to one and
  silently ignored by the other — the exact failure that produced today's
  divergence.

### FR-2 — Operation-aware decisions

- **FR-2.1** `ResolvePath` stops discarding `op`. The `_ = op` line is deleted.
- **FR-2.2** Decisions by operation:

  | `FSOp` | Decision |
  |---|---|
  | `FSOpRead`, `FSOpList`, `FSOpSend` | allowed anywhere except the secret set |
  | `FSOpWrite` | work dir or a mount only |
  | `FSOpExec` | per the ADR-062 model |
  | `FSOpServe` | work dir or a mount only (unchanged from today — see FR-2.3b) |

- **FR-2.3** **There is no path-based "publish" restriction. OPERATOR DECISION,
  2026-08-12, overruling an earlier draft of this spec.**

  That draft introduced a publish class — `send_file` and `web_serve` confined to
  the work dir even though reads are open — on the reasoning that reading a file
  into context and disclosing it to other people are different acts. The
  operator rejected it: *"agents that have the send tools can send and that must
  not be guarded; if we want to prevent that we set the tool permissions
  accordingly."*

  **They are right, and the argument defeats the mechanism, not just the
  policy.** Under open reads, path-confining a publish operation is bypassable
  in one extra step:

  - `send_file` confined → `read_file` the path, paste the contents into the
    chat message.
  - `web_serve` confined → `read_file` the path, `write_file` a copy inside the
    work dir, serve the copy.

  So the restriction would not have stopped a determined agent; it would only
  have made ordinary use awkward while reading as protection in the spec. That
  is the "mechanism, not property" defect this project keeps having to correct,
  and it was in my own draft.

  **The real gate is tool policy**, which already exists, is explicit per agent,
  and is hard-validated with no defaults (Constraint #6). An operator who does
  not want an agent disclosing files denies it `send_file`. That is one gate
  that actually holds, rather than two that each half-hold.

- **FR-2.3a** `FSOpSend` is still introduced as a distinct operation, but purely
  for AUDIT: a disclosure to a chat channel should be distinguishable from an
  ordinary read in the audit log and in any future ask-flow. It carries no
  additional path restriction.

- **FR-2.3b** `web_serve` (`FSOpServe`) stays confined to the work dir and
  mounts, and this is PRESERVATION, not a boundary claim. **VERIFIED:** it
  already hardcodes `ResolveTurnFSPolicy(ctx, agentHome, true)`, ignoring the
  agent's own restrict setting. Opening it would be a deliberate widening of
  shipped behaviour, which is out of scope here. This spec explicitly does NOT
  claim it prevents disclosure — the copy-then-serve bypass above applies.

- **FR-2.3c** `workspace_read` (`pkg/gateway/rest_workspace.go`) is an
  OPERATOR-facing REST read, not an agent tool, and stays in the read class.
  Called out because it is the third `FSOpRead` site and a reader would
  otherwise have to re-derive whether it was missed.

- **FR-2.4** Every existing `ResolvePath` call site already passes a correct
  `op`. A test MUST assert that no call site passes a zero-value `FSOp`, since
  an empty op after FR-2.1 would take the default branch rather than fail
  loudly.
- **FR-2.5** `RestrictToWorkspace` becomes a WRITE-scope setting. Its wire name,
  config key and UI label are unchanged; its meaning narrows. **This must be
  stated in the release note** — an operator reading "restrict to workspace"
  will otherwise assume it still confines reads.

### FR-3 — The merged secret set

- **FR-3.1** **The secret set moves INTO `pkg/fspolicy` (the leaf), and
  `pkg/sandbox` consumes it from there.** The first draft of this spec said the
  opposite — that `sandbox.SecretEntriesRelative` becomes the single definition
  — which is unbuildable: `fspolicy` would have to import `sandbox` for it,
  while FR-1.3a requires `sandbox` to import `fspolicy`. That is an import
  cycle, and Go would have refused it at the first compile.

  The set is data about the `$OMNIPUS_HOME` layout, so the leaf is where it
  belongs regardless. `sandbox.SecretEntriesRelative` becomes an alias for the
  fspolicy definition (the same alias treatment ADR-062 gave the older
  `SecretFilesRelative`), so no call site churns and there is still exactly one
  list. `fspolicy.buildCarveOuts` is folded into it, not kept in sync.
- **FR-3.2** The merged set is the UNION of both lists: `master.key`,
  `credentials.json`, `config.json`, `cli.token`, `entities/`, `agents/`,
  `workspaces/`, plus the ADR-062 backup prefixes.
- **FR-3.3** The per-root own-tree exception is carried across EXACTLY, not
  approximated. A path is exempt from carve-out root R only when the work dir is
  a proper descendant of that same R.
- **FR-3.4** A test MUST assert both shapes: in an agent-home-rooted turn the
  agent's own home is reachable; in a re-rooted workspace turn it is NOT. The
  second is the one a looser implementation gets wrong, and getting it wrong
  makes the kernel MORE permissive than the app layer on every workspace turn.
- **FR-3.5** `agents/` and `workspaces/` gaining kernel enforcement is a
  NARROWING of what child processes may do. Listed in §6 as an intended change.

### FR-4 — Kernel rendering

- **FR-4.0** **The per-child policy must first be PLUMBED to the child spawn on
  macOS — it does not exist today, and FR-4.1 is meaningless without it.**
  VERIFIED: `applyPlatformHardening(cmd, _ Limits)` passes an EMPTY policy "so
  ApplyToCmd reuses the profile Apply already [installed]" — i.e. every child is
  wrapped in one process-global profile captured at boot. There is no seam that
  takes a per-turn policy. Adding one is a prerequisite of this whole section,
  and the first draft of this spec silently assumed it was already there.
  Linux needs no equivalent work: `RestrictCurrentThread` already rebuilds from
  a saved policy per spawn, so it needs the saved policy swapped for the
  per-turn one, not a new seam.
- **FR-4.1** The rendered macOS profile is cached, keyed by the authored policy.
  A stale profile must not be representable: different policy → different key.
- **FR-4.2** A render or cache-fill failure ABORTS the spawn. Never a fallback
  to an unconfined child. (Matches the Linux `RestrictCurrentThread` contract.)
- **FR-4.3** **MEASURED:** 2.5 ms per render, 142 KB profile, 1000 connect
  ports, 200 iterations. FR-4.1 exists because of this number, not despite it.
- **FR-4.4** A test MUST assert the cache invalidates when a mount is added or
  removed. A cache that survives a policy change is worse than no cache: it
  enforces yesterday's grants.

---

## 2. Functional requirements — mounts

### FR-5 — Shape and storage

- **FR-5.1** A workspace carries `mounts: [{name, host_path}]`. Contract-first:
  schema → `scripts/gen-contracts.sh` → generated diff in the same commit.
- **FR-5.2** `name` is a single path segment, non-empty, no separators, no `..`,
  unique within the workspace, and not colliding with an existing entry in
  `work/`.
- **FR-5.3** `host_path` is absolute and realpath-resolved at creation. The
  resolved form is stored; the raw form is not.
- **FR-5.4** Materialised as a symlink `work/<name>` → `host_path`.
- **FR-5.5** Excluded from the workspace evidence git, or it will commit the
  operator's repository contents as its own. A test MUST assert an evidence
  commit after a mount exists contains no file from inside the mount.

### FR-6 — Grants

- **FR-6.1** A mount grants WRITE. Reads need no mount (FR-2.2).
- **FR-6.2** There is no read-only mount. Under FR-2.2 it would be
  indistinguishable from no mount. If FR-2.2 ever changes, this must be revisited
  — noted here because a future reader will otherwise see an obvious missing
  feature.
- **FR-6.3** Mounts apply to every agent on the workspace.
- **FR-6.4** Writes stop at the mount boundary. A symlink inside a mount that
  resolves outside every granted root is NOT writable; reading through it still
  succeeds. This falls out of realpath resolution and MUST be tested rather than
  assumed, including: symlink to a sibling directory, symlink to `$HOME`,
  symlink chains, and a relative `../..` escape.
- **FR-6.5** A hardlink inside a mount pointing outside it is a KNOWN,
  DOCUMENTED GAP — a hardlink has no separate path to resolve. Asserted as a gap
  so it is not mistaken for coverage.

### FR-7 — Creation, approval, refusal

- **FR-7.1** The operator creates mounts.
- **FR-7.2** An agent may request one; approval turns it into an ordinary mount.
- **FR-7.3** An approval is permanent until revoked. Not per-turn, not
  per-session.
- **FR-7.4** Risky targets (home directory, filesystem root, system directories)
  WARN with a message naming what the grant covers, then proceed.
- **FR-7.5** A mount whose target IS `$OMNIPUS_HOME`, or lies INSIDE it, is
  REFUSED. Checked on the realpath-resolved target, so a symlink to it is
  refused too. A test MUST cover the symlink form; the direct form is not the
  one anyone would reach for.

- **FR-7.6** A mount whose target CONTAINS `$OMNIPUS_HOME` — the home directory,
  the filesystem root — is **WARNED AND ALLOWED**. *(Operator decision,
  2026-08-12: "warn and allow applies to all but the omnipus directory".)*

  An earlier draft of this spec refused these too, on the grounds that mounting
  `$HOME` makes `master.key` writable. **That reasoning was wrong, because it
  treated the mount as the only thing standing between an agent and the secret
  set.** It is not. The secret set is denied (macOS) or never granted (Linux)
  independently of any mount, by FR-3 — a mount grants write to a tree, and the
  secret set is subtracted from every grant regardless of where the grant came
  from. So mounting `$HOME` yields exactly "write to `$HOME` minus the secret
  set", which is what the operator asked for and what the existing machinery
  already computes on both platforms.

  Refusing would have taken away something legitimate to protect against a
  danger the design already handles.

- **FR-7.7** A test MUST prove FR-7.6 rather than assume it: with `$HOME`
  mounted, a child MUST still be unable to read `cli.token` or write
  `config.json` or `master.key`. This is the assertion that turns the reasoning
  above into a fact — and if it ever fails, FR-7.6 must revert to refusal.

### FR-8 — Lifecycle

- **FR-8.1** Mounts take effect immediately. No restart.
- **FR-8.2** A missing target does not block boot or the workspace. The mount is
  marked broken; an agent using it gets a reason, not a permission error.
- **FR-8.3** A broken mount is NEVER silently recreated as an empty directory.
  A test MUST assert this: an agent writing into a recreated empty directory
  while believing it is the project is the worst outcome available here.
- **FR-8.4** Two workspaces MAY mount the same folder. No locking, no
  coordination. (Operator decision — the situation is the same as an operator
  editing files while an agent works.)
- **FR-8.5** Mounts are machine-specific. On another machine, or a restored
  backup, a mount whose target does not resolve shows as broken (FR-8.2). A
  mount is NEVER silently re-bound to a same-named path that happens to exist —
  that is how an agent ends up writing into a different folder than intended.
- **FR-8.6** Archiving or deleting a workspace NEVER touches the operator's
  mounted folder. Only the symlink and the mount record are removed. A test MUST
  assert the target still exists with its contents after workspace deletion.

### FR-9 — Removal of `repository`

- **FR-9.1** `Workspace.repository` is deleted from the wire, storage and the
  sysagent tool. No back-compat (ADR-035/037 precedent).
- **FR-9.2** `PUT`/`POST` with a `repository` field 400s via raw-body sniff
  rather than silently dropping it, mirroring the `sandbox_profile` and
  `delegation_policy` precedents.
- **FR-9.3** Git linkage becomes: URL → clone to an operator-chosen location →
  mount it. Cloning into Omnipus storage is explicitly NOT the behaviour.

---

## 3. BDD scenarios

### S-1 — The two layers finally agree
```gherkin
Given an agent with a workspace work dir
 When it reads ~/notes.txt via read_file
  And it reads ~/notes.txt via bash cat
 Then both succeed
```
*Fails today: the first is denied, the second allowed.*

### S-2 — Writes stay confined while reads are open
```gherkin
Given no mount covers ~/notes.txt
 When the agent writes to ~/notes.txt
 Then the write is denied
  And reading the same path still succeeds
```

### S-3 — Serving is not reading
```gherkin
Given the agent can READ /Users/me/private/report.pdf
 When it calls web_serve on that path
 Then the call is refused
```
*The one place open reads must not propagate — serving publishes on a listener.*

### S-3b — Sending is governed by tool policy, not by path
```gherkin
Given the agent is granted the send_file tool
 When it sends /Users/me/report.pdf
 Then the send succeeds

Given the agent is DENIED the send_file tool
 When it attempts to send any file
 Then the tool is unavailable
```
*The path is not the gate. Confining it would have been bypassable by reading
the file and pasting its contents into the message.*

### S-4 — A mount makes exactly one folder writable
```gherkin
Given the workspace mounts /Users/me/projects/foo as "foo"
 When the agent writes work/foo/src/main.go
 Then the write succeeds
  And the file on disk at /Users/me/projects/foo/src/main.go is changed
```

### S-5 — Writes stop at the mount boundary
```gherkin
Given /Users/me/projects/foo/link -> /Users/me/Documents
 When the agent writes work/foo/link/notes.txt
 Then the write is denied
  And reading work/foo/link/notes.txt still succeeds
```

### S-6 — The own-tree exception, both shapes
```gherkin
Given an agent-home-rooted turn (WorkDir = <home>/agents/self)
 When the agent writes its own agents/self/x
 Then the write succeeds

Given a re-rooted workspace turn (WorkDir = <home>/workspaces/w/work)
 When the agent writes <home>/agents/self/x
 Then the write is denied
```
*The second is the one a blanket own-tree exception gets wrong — and getting it
wrong makes the kernel MORE permissive than the app layer.*

### S-7 — $OMNIPUS_HOME cannot be mounted, even indirectly
```gherkin
Given a symlink /tmp/sneaky -> $OMNIPUS_HOME
 When the operator mounts /tmp/sneaky
 Then the mount is refused
```

### S-7b — but mounting a folder that CONTAINS it is fine, and still safe
```gherkin
Given the operator mounts $HOME, accepting the warning
 When an agent writes $HOME/notes.txt
 Then the write succeeds
 When the same agent reads $OMNIPUS_HOME/cli.token
 Then the read is denied
 When the same agent writes $OMNIPUS_HOME/config.json
 Then the write is denied
```
*The secret set is subtracted from every grant regardless of its source, so a
broad mount cannot re-expose it. Refusing the mount would have taken away
something legitimate to guard against a danger already handled.*

### S-8 — A broken mount is inert, not invented
```gherkin
Given a mount whose target has been deleted
 When the gateway starts
 Then it starts normally
  And the mount is reported broken
  And no directory is created at the target path
```

### S-9 — Deleting a workspace does not delete the operator's work
```gherkin
Given a workspace mounting /Users/me/projects/foo
 When the workspace is deleted
 Then /Users/me/projects/foo still exists with all its files
```

---

## 4. Test dataset

| # | Case | Expected | Why |
|---|---|---|---|
| 1 | `read_file` outside work dir | allowed | FR-2.2, the headline change |
| 2 | `write_file` outside work dir and mounts | denied | writes unchanged |
| 3 | `read_file` on `master.key` | denied | secret set, both layers |
| 4 | `read_file` on `cli.token` | denied | app layer gains this |
| 5 | `bash cat` on another agent's dir | denied | kernel gains this (FR-3.5) |
| 6 | `web_serve` outside work dir | denied | FR-2.3b — preserves today's behaviour |
| 6a | `send_file` outside work dir | **allowed** | FR-2.3 — tool policy is the gate, not the path |
| 6b | agent denied `send_file` by tool policy | tool unavailable | FR-2.3 — the gate that actually holds |
| 6c | `workspace_read` (operator REST) outside work dir | allowed | FR-2.3c — operator, not agent |
| 7 | mount name `../escape` | rejected | FR-5.2 |
| 8 | mount name colliding with existing `work/` entry | rejected | FR-5.2 |
| 9 | two mounts, same name | rejected | FR-5.2 |
| 10 | two workspaces, same target | allowed | FR-8.4 |
| 11 | mount target `$OMNIPUS_HOME` | refused | FR-7.5 |
| 11a | mount target inside `$OMNIPUS_HOME` (`entities/`) | refused | FR-7.5 |
| 11b | `$HOME` mounted, then read `cli.token` | denied | FR-7.7 — the proof FR-7.6 rests on |
| 11c | `$HOME` mounted, then write `master.key` | denied | FR-7.7 |
| 12 | mount target symlinked to `$OMNIPUS_HOME` | refused | FR-7.5, the form that matters |
| 13 | mount target `$HOME` (contains `$OMNIPUS_HOME`) | warned, allowed | FR-7.6 |
| 14 | mount target `/` | warned, allowed | FR-7.6 |
| 15 | write through symlink escaping the mount | denied | FR-6.4 |
| 16 | read through that same symlink | allowed | FR-6.4 |
| 17 | write through a hardlink escaping the mount | **allowed — documented gap** | FR-6.5 |
| 18 | mount added mid-session, then a child spawns | child sees it | FR-8.1 |
| 19 | mount removed, then a child spawns | child cannot write it | FR-4.4 |
| 20 | mount target deleted while running | writes fail with a reason | FR-8.2 |
| 21 | evidence commit with a mount present | contains no mount file | FR-5.5 |
| 22 | workspace deleted | target intact | FR-8.6 |
| 23 | restore on another machine | mounts broken, gateway starts | FR-8.5 |
| 24 | same-named path exists on the other machine | still broken, NOT re-bound | FR-8.5 |
| 25 | `PUT /workspaces/{id}` with `repository` | 400 | FR-9.2 |
| 26 | zero-value `FSOp` reaching ResolvePath | rejected | FR-2.4 |

---

## 5. Implementation order

1. **Capture today's behaviour** for both layers as tests that pass NOW. The
   gate. Nothing else starts until this is green.
1b. **FR-4.0 plumbing** — a per-turn policy must reach the macOS child spawn
   before any of the kernel-rendering work has anywhere to land.
2. FR-1 authored policy + derivation, with FR-1.4's totality test.
3. FR-3 merged secret set, including the per-root exception (FR-3.3/3.4).
4. FR-2 operation-aware decisions. `FSOpWrite` first — it is the only class
   where a mistake grants real damage, since reads and sends are open by
   decision and `serve` merely keeps what it already had.
5. FR-4 kernel rendering + cache.
6. FR-5/6 mounts: storage, contract, symlink, grants, boundary.
7. FR-7 creation/approval/refusal.
8. FR-8 lifecycle.
9. FR-9 `repository` removal.
10. Library UI — separate change, depends on all of the above.

---

## 6. Intended behaviour changes (the §0 gate's allow-list)

Anything not on this list must not change.

| Change | Direction | Who notices |
|---|---|---|
| `read_file`/`list` work outside the work dir | wider | agents stop failing on ordinary reads |
| `web_serve` unchanged (stays confined) | none | — |
| `send_file` follows open reads | wider | agents can send any readable file; governed by tool policy |
| `bash` can no longer reach `agents/`, `workspaces/` | narrower | cross-agent snooping via shell stops working |
| `read_file` can no longer reach `config.json`, `cli.token` | narrower | closes a live token read |
| `RestrictToWorkspace` governs writes only | narrower in meaning | needs a release note |
| Mounts exist | wider, opt-in | nothing changes until one is created |

---

## 7. Open — needs an operator decision before FR-7 is built

**O-1 — RESOLVED 2026-08-12 by the operator: "warn and allow applies to all but
the omnipus directory".** Only a mount that IS or is INSIDE `$OMNIPUS_HOME` is
refused (FR-7.5). Everything else, including `$HOME` and `/`, warns and
proceeds (FR-7.6), because the secret set is subtracted from every grant
independently of mounts — so mounting `$HOME` cannot expose it. FR-7.7 asserts
that rather than trusting it.

**O-2 — RESOLVED 2026-08-13 by the operator: "ask in chat, with the same
mechanism as tool access — the modal we already use."** A mount request is an
ordinary tool call (`request_mount`, `pkg/tools/request_mount.go`), so it rides
the existing tool-approval modal: focus on Deny, Escape denies, nothing
auto-approves, request expires. No second consent path.

One change was required to reuse it safely. **"Always Allow" is withheld for
this tool.** Approval grants are keyed on (session, agent, TOOL NAME) — the
arguments are not part of the key — so the shortcut would mean "this agent may
mount ANY folder for the rest of the session, without asking": a blanket grant
over the whole disk from one click, with no path shown. It is also
unnecessary, since approving once creates a mount that persists until revoked.
Implemented as a named set in `ToolApprovalModal.tsx` and mutation-verified;
Approve and Deny are untouched.

**O-3 — RESOLVED 2026-08-13, same session: the Library browses a SET of roots.**
Discovered while building the UI: the Library could not open a mount at all. It
browses through an `os.Root` at `work/`, which refuses at the syscall level to
follow anything resolving outside it — and a mount IS a symlink pointing
outside. A mounted folder appeared in the listing and failed on click. Resolved
by opening one `os.Root` per mount (`pkg/library/root.go`), so browsing inside a
mount is contained to that folder exactly as `work/` is contained to itself.
Nothing is relaxed; the containment learns a workspace legitimately has more
than one root, which is what the engine already models via
`fspolicy.FSPolicy.AllowedRoots`.
