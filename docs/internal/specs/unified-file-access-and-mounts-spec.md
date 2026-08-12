# Spec — unified file-access engine and workspace mounts

- **Implements:** [ADR-061](../architecture/ADR-061-unified-file-access-engine-and-mounts.md)
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

**Non-negotiable gate, inherited from ADR-060 and restated because it is the
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

  | `FSOp` | Class | Decision |
  |---|---|---|
  | `FSOpRead`, `FSOpList` | read into agent context | allowed anywhere except the secret set |
  | `FSOpWrite` | mutate | work dir or a mount only |
  | `FSOpExec` | run | per the ADR-060 model |
  | `FSOpServe` | **publish** | work dir or a mount only |
  | `FSOpSend` *(new)* | **publish** | work dir or a mount only |

- **FR-2.3** **There is a PUBLISH class, and it does not follow open reads.**
  Reading a file into the agent's own context and emitting it to somewhere
  other people can see it are different acts with different blast radii. The
  first is the agent doing its job; the second is disclosure.

- **FR-2.3a** `web_serve` (`FSOpServe`) publishes on an HTTP listener reachable
  by anyone holding the preview token. **VERIFIED it is already confined**
  today: `web_serve` calls `ResolveTurnFSPolicy(ctx, agentHome, true)` with
  `restrict` HARDCODED true, ignoring the agent's own setting. So this
  requirement PRESERVES existing behaviour rather than adding a restriction —
  and the existing code already made this exact judgement, independently.

- **FR-2.3b** **`send_file` must move from the read class to the publish class,
  and this is a real gap the first draft of this spec missed.** It resolves with
  `FSOpRead` and then emits the file "to the user on the current chat channel
  via the MediaStore pipeline" — Telegram, Slack, Discord, possibly a group.
  Unlike `web_serve` it uses the agent's own `restrict` setting, so under open
  reads it would become able to send ANY readable file on the machine to a
  third-party chat service. That is a wider and easier disclosure channel than
  the one FR-2.3a protects.

  A new `FSOpSend` is introduced rather than reusing `FSOpServe`, so audit
  records and any future ask-flow can tell "published on a listener" from "sent
  to a chat".

- **FR-2.3c** A test MUST assert, for BOTH publish ops, that a path outside the
  work dir and outside every mount is refused **while `read_file` on the same
  path succeeds**. Asserting the refusal alone would pass against a build where
  reads never opened at all.

- **FR-2.3d** `workspace_read` (`pkg/gateway/rest_workspace.go`, the third
  `FSOpRead` site) is an OPERATOR-facing REST read, not an agent tool. It stays
  in the read class: the operator reading their own files through their own
  authenticated API is not agent disclosure. Called out because it is the one
  remaining `FSOpRead` site and a reader would otherwise have to re-derive
  whether it was missed.
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
  fspolicy definition (the same alias treatment ADR-060 gave the older
  `SecretFilesRelative`), so no call site churns and there is still exactly one
  list. `fspolicy.buildCarveOuts` is folded into it, not kept in sync.
- **FR-3.2** The merged set is the UNION of both lists: `master.key`,
  `credentials.json`, `config.json`, `cli.token`, `entities/`, `agents/`,
  `workspaces/`, plus the ADR-060 backup prefixes.
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
- **FR-7.5** `$OMNIPUS_HOME`, or any path containing it, is REFUSED. Checked on
  the realpath-resolved value, so a symlink to it is refused too. A test MUST
  cover the symlink form; the direct form is the one an attacker would not use.
- **FR-7.6** Refusal in FR-7.5 also applies to a mount whose target CONTAINS
  `$OMNIPUS_HOME` (e.g. the home directory). Warning is not sufficient there,
  because the result is the same: `master.key` becomes writable.

  > This is a deliberate narrowing of FR-7.4 and the operator's "warn, never
  > refuse" answer. It is reported to them as an open item in §7 rather than
  > decided silently: mounting `$HOME` is a legitimate-if-broad thing to want,
  > and it currently collides with FR-7.5.

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

### S-3b — Sending is not reading either
```gherkin
Given the agent can READ /Users/me/private/report.pdf
 When it calls send_file on that path
 Then the call is refused
```
*The wider of the two publish channels: send_file reaches a third-party chat
service, and unlike web_serve it honours the agent's own restrict setting, so
open reads would have silently unconfined it.*

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
| 6 | `web_serve` outside work dir | denied | FR-2.3a (already true today) |
| 6a | `send_file` outside work dir | denied | FR-2.3b — the gap the grill found |
| 6b | `send_file` outside work dir, `read_file` same path | denied / allowed | FR-2.3c — proves reads really opened |
| 6c | `workspace_read` (operator REST) outside work dir | allowed | FR-2.3d — operator, not agent |
| 7 | mount name `../escape` | rejected | FR-5.2 |
| 8 | mount name colliding with existing `work/` entry | rejected | FR-5.2 |
| 9 | two mounts, same name | rejected | FR-5.2 |
| 10 | two workspaces, same target | allowed | FR-8.4 |
| 11 | mount target `$OMNIPUS_HOME` | refused | FR-7.5 |
| 12 | mount target symlinked to `$OMNIPUS_HOME` | refused | FR-7.5, the form that matters |
| 13 | mount target `$HOME` (contains `$OMNIPUS_HOME`) | **open — see §7** | FR-7.6 conflict |
| 14 | mount target `/` | refused or warned per §7 | FR-7.4 vs FR-7.6 |
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
4. FR-2 operation-aware decisions, **the publish class first** (`FSOpServe`,
   `FSOpSend`) — those are where a mistake discloses files rather than merely
   denying them, and `send_file` in particular regresses from confined to open
   the moment reads open, if nothing is done.
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
| `send_file` stays confined instead of following open reads | none vs today | — (without FR-2.3b it would have silently widened) |
| `bash` can no longer reach `agents/`, `workspaces/` | narrower | cross-agent snooping via shell stops working |
| `read_file` can no longer reach `config.json`, `cli.token` | narrower | closes a live token read |
| `RestrictToWorkspace` governs writes only | narrower in meaning | needs a release note |
| Mounts exist | wider, opt-in | nothing changes until one is created |

---

## 7. Open — needs an operator decision before FR-7 is built

**O-1. FR-7.6 collides with the operator's "warn, never refuse" answer.** They
decided risky mounts warn rather than refuse, with `$OMNIPUS_HOME` as the single
exception. But mounting `$HOME` — a legitimate if broad thing to want — CONTAINS
`$OMNIPUS_HOME`, so warning is not enough: `master.key` becomes writable and the
sandbox can be switched off, which is exactly what the single exception exists
to prevent.

Three ways out, none obviously right:

1. Refuse any mount containing `$OMNIPUS_HOME` (so `$HOME` cannot be mounted).
   Simple, and takes away something reasonable.
2. Allow it, but keep the secret set denied INSIDE the mount — the mount grants
   write to `$HOME` minus `$OMNIPUS_HOME`. Most precise; it makes the secret set
   a hole punched in a grant, which is exactly what the macOS deny already does
   and what Linux sibling-granting already computes. Costs the mount rendering a
   subtraction step.
3. Move `$OMNIPUS_HOME` out of `$HOME` so the case cannot arise. Rejected on
   sight — it breaks every existing install for a UI edge case.

Option 2 looks right and is the one I would recommend, because the machinery
already exists on both platforms. It is NOT assumed here: FR-7.6 is written as
the conservative refusal until this is answered.

**O-2.** Does an agent's mount REQUEST (FR-7.2) surface as a chat approval, a
notification, or a Library prompt? Affects no engine behaviour; blocks the UI
work only.
