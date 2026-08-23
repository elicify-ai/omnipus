# ADR-063 — One file-access engine, and workspace mounts

- **Status:** Proposed (2026-08-12)
- **Implements:** [file-access requirements, 2026-08-12](../specs/file-access-requirements-2026-08-12.md)
- **Builds on:** [ADR-062](ADR-062-filesystem-read-exec-model-inversion.md) (reads/exec open; the secret set unreachable)
- **Corrected by:** [ADR-068](ADR-068-bash-text-guard-third-rule-layer.md) (2026-08-23) — §1.1's two-layer model is incomplete; there is a third, and one VERIFIED claim there is false in shipped code
- **Supersedes in part:** ADR-046 P1's `FSScope` model; the `repository` field on Workspace

---

## 1. Context

> **Correction (2026-08-23, [ADR-068](ADR-068-bash-text-guard-third-rule-layer.md)):**
> §1.1 below counts two rule layers. There are **three**. The `bash` tool runs
> its own in-process text guard (`ExecTool.guardCommand`, `pkg/tools/shell.go`)
> before any child is spawned, and that guard still enforces the pre-ADR-062
> confined model for reads. The bullet "`bash cat ~/notes.txt` succeeds" and the
> table row granting outside-`WorkDir` reads to the kernel layer are **false in
> shipped code** under the default `RestrictToWorkspace: true` — verified at
> commit `e269e52c` by calling `guardCommand` directly. The command is rejected
> in-process and never reaches the kernel. Read §1.1's disagreement analysis as
> incomplete, not merely imprecise: it describes the kernel's disposition toward
> commands the kernel never sees. ADR-068 §1.1 carries the evidence.

### 1.1 There are two file-access rule sets and they disagree

Omnipus decides "may this path be touched" in two independent places:

| | Governs | Type |
|---|---|---|
| **App layer** | the in-process tools — `read_file`, `write_file`, `edit`, `send_file`, `web_serve` | `fspolicy.FSPolicy` → `tools.ResolvePath` |
| **Kernel layer** | child processes — `bash`, the browser | `sandbox.SandboxPolicy` → Landlock / Seatbelt |

They are not two enforcement points for one rule. They are two different rules,
and they disagree in **both** directions (VERIFIED, 2026-08-12):

| Path | App layer | Kernel layer |
|---|---|---|
| `agents/`, `workspaces/` | denied (carve-out) | granted |
| `config.json`, `cli.token` | **allowed** | denied (ADR-062) |
| anything outside `WorkDir`, read | denied when confined | ~~**allowed** (ADR-062)~~ — **corrected: denied by the bash text guard before the kernel is reached (ADR-068 §1.1)** |

The practical consequences are all of the form "the answer depends on which tool
asked", which no operator can predict:

- ~~`bash cat ~/notes.txt` succeeds; `read_file ~/notes.txt` is denied.~~
  **Corrected (ADR-068):** `bash cat ~/notes.txt` is *also* denied under the
  shipped default, by the bash tool's own text guard. Both are denied — the
  asymmetry this bullet claims does not exist on the read axis.
- An agent running unrestricted can read the gateway bearer token through
  `read_file`, because `cli.token` is not an app-layer carve-out — while the
  same read from `bash` is denied by the kernel.

ADR-062 did not create this. It made it visible and, by opening reads on one
side only, it widened the gap.

### 1.2 The competition has one rule layer, not two

Claude Code's permission rules "apply to every tool: Bash, Read, Edit, WebFetch,
MCP", while OS sandboxing "applies only to Bash commands and their child
processes". One authored ruleset over every tool; the kernel is a second wall on
the subset it can reach. Codex is arranged the same way. Neither ships anything
resembling our two-rulesets split.

### 1.3 Agents cannot work in the operator's own folders

An agent works in `$OMNIPUS_HOME/workspaces/<id>/work/`. The most common real
task — "work in this repo I already have" — is impossible without copying the
repo into that directory, which defeats the point: the operator's git history,
tooling and editor all point at the original.

The `Workspace.repository` field looks like it addresses this. It does not: it
stores a URL, nothing reads it (VERIFIED — the only non-test consumers are the
sysagent tool that sets it and the one that echoes it back). It is a control
that appears functional and does nothing, the defect class this project has
repeatedly had to correct.

### 1.4 What is already in place, and is better than expected

Three findings materially reduce the size of this change:

1. **`ResolvePath` already takes an `op FSOp`** (`FSOpRead`, `FSOpWrite`,
   `FSOpList`, `FSOpExec`, `FSOpServe`) and **every call site already passes the
   correct value** — `FSOpRead` from `read_file`, `FSOpWrite` from `write_file`
   and `edit`, and so on. The parameter is then discarded: `_ = op`. So a
   read/write distinction does not need to be threaded through the tools; it
   needs the chokepoint to stop ignoring an argument that is already right.
2. **`FSPolicy.AllowedRoots` already exists**, documented as "always nil in P1.
   Reserved for P2's per-path allow seam", consumed by nothing. That is the
   mount seam, pre-shaped.
3. **`RestrictCurrentThread` rebuilds the kernel ruleset per child spawn** on
   Linux (ADR-062). A per-turn kernel policy is therefore already achievable on
   that platform without new machinery.

---

## 2. Decision

### D1 — One authored ruleset; the two layers become renderings of it

A single per-turn value describes file access. Both layers are projections:

```
                    authored once (per turn)
                    ┌──────────────────────┐
                    │  work dir            │
                    │  mounts (write)      │
                    │  secret set (never)  │
                    │  reads open?         │
                    └──────────┬───────────┘
                   ┌───────────┴───────────┐
        app-layer rendering        kernel rendering
      (ResolvePath, every tool)   (Landlock / Seatbelt,
                                    child processes)
```

**The app layer enforces every rule on every platform.** The kernel enforces
what it can, as a second wall. Where the kernel cannot help — Windows, pre-5.13
Linux — the rule still holds; only the second wall is missing, and boot says so.

This is deliberately the ADR-062 pattern ("one principle, two renderings")
applied one level up. ADR-062 unified the *secret set* across two backends; this
unifies the *whole ruleset* across the app and kernel layers.

**It is also not a new direction: this is ADR-046's P3, accepted and never
built.** `pkg/fspolicy`'s package doc already names EffectiveFSPolicy as "the
one function of record consumed by both the app-layer path resolver (ResolvePath,
P1/P2) and the future per-child Landlock ruleset builder (P3) — so the two
enforcement backends can never drift apart". The divergence documented in §1.1 is
what happened in the gap where P3 was specified but absent. This ADR therefore
decides far less than its length suggests: it completes an accepted design, adds
mounts on top, and corrects the read/write split ADR-062 exposed.

**Consequence that constrains everything below:** `pkg/fspolicy` is deliberately
a stdlib-only leaf, because `pkg/tools` already imports `pkg/sandbox` and P3
wires `pkg/sandbox → pkg/fspolicy`. The secret set must therefore live in the
leaf and be consumed by the kernel package, not the other way around.

### D2 — Access decisions become operation-aware

`ResolvePath` stops discarding `op`:

| Operation | Decision |
|---|---|
| read, list | allowed anywhere **except the secret set** |
| write | allowed only under the work dir or a mount |
| exec | governed by the kernel model (ADR-062) |
| **serve** | **allowed only under the work dir or a mount — NOT open** |

**`serve` is deliberately not a read, and the first draft of this ADR got that
wrong by omitting it from the table.** `web_serve` publishes files over HTTP at
a preview URL. If serving inherited the open-read rule, an agent could serve any
readable file on the machine — the operator's documents, another project's
source — to anyone holding the preview token. Reading a file into the agent's
own context and republishing it on a network listener are different acts with
different blast radii, and the operation vocabulary already distinguishes them
(`FSOpServe`). Publishing stays confined to what the operator granted.

This is what makes `bash cat X` and `read_file X` agree. It is also the single
change that retires the `FSScopeConfined` / `FSScopeUnrestricted` distinction
for reads: reads are open under both.

> **The `restrict` setting does not disappear — it becomes a WRITE setting.**
> `RestrictToWorkspace` continues to govern where an agent may write, which is
> what operators actually mean by it. Reads stop being part of that decision
> because, post-ADR-062, confining them was already fiction for any agent with
> shell access.

### D3 — The secret set is the one thing neither layer may reach

`sandbox.SecretEntriesRelative` (ADR-062 §4.0) becomes the authority for BOTH
layers, replacing `fspolicy.buildCarveOuts`'s separate list. The merged set is
the union, because each list protects something the other misses:

| Entry | Was protected by | Why it stays |
|---|---|---|
| `master.key`, `credentials.json` | both | root of trust |
| `config.json`, `cli.token` | kernel only | sandbox self-disable; live bearer token |
| `entities/` | both | per-agent tool policy |
| `agents/`, `workspaces/` | app only | cross-agent isolation |
| `config.json.bak-*` etc. | kernel only | a copy of a secret is a secret |

**`agents/` and `workspaces/` keep their existing own-tree exception, which is
narrower than it sounds and must be carried across exactly.** The first draft of
this ADR described it as "an agent's own directory is not a carve-out of
itself", which is too broad. As implemented it is scoped PER-ROOT: a path is
exempt from carve-out root R only when `WorkDir` is a proper descendant of that
same R. The consequence that matters:

- **Agent-home-rooted turn** (`WorkDir == <home>/agents/<self>`): the agent's own
  home is reachable, as it must be.
- **Re-rooted workspace turn** (`WorkDir == <home>/workspaces/<id>/work`):
  `agents/<self>` falls outside `WorkDir` and is therefore **as unreachable as
  any other agent's home** — by design, matching today's re-root behaviour.

A kernel rendering that applied a blanket "own home is always reachable"
exception would therefore be MORE permissive than the app layer during every
workspace turn — silently reintroducing the cross-layer divergence this ADR
exists to remove. Because the authored policy is per-turn (D1), the rendering
can and must reproduce the per-root, per-shape rule rather than approximate it.

### D4 — A mount is a named write-grant on a real local folder

A workspace carries a list of mounts: `{name, hostPath}`. Each renders as:

| Layer | Rendering |
|---|---|
| Filesystem | a symlink `work/<name>` → `hostPath` |
| App layer | an entry in `FSPolicy.AllowedRoots` (write permitted) |
| Kernel | a `PathRule{hostPath, Read|Write|Execute}` |
| Evidence git | excluded, so the operator's repo is never committed as ours |

**A mount grants WRITE and nothing else.** Reads are already open (D2), so a
"read-only mount" would be indistinguishable from no mount at all. This is a
consequence of D2, not an independent choice: if D2 is ever reversed, this must
be revisited.

**The symlink is the user-visible mechanism, not the enforcement.** Enforcement
is by realpath: every decision resolves symlinks first and then asks whether the
result is under the work dir or a mount. This is why **writes stop at the mount
boundary** (requirement R-10) with no extra code — a link inside the operator's
repo pointing elsewhere resolves outside every granted root, so the write is
denied while the read still succeeds.

### D5 — Mounts take effect immediately, which costs macOS a per-spawn profile

Adding a mount must not require a restart. Linux already rebuilds its ruleset
per spawn. macOS renders its Seatbelt profile once at boot and must move to
per-spawn rendering.

**MEASURED, not estimated: 2.5 ms per render** for a realistic production policy
(142 KB profile, 1000 dev-server connect ports, 200 iterations on this host). An
earlier draft of this ADR claimed "sub-millisecond" without measuring; the real
figure is several times that, which is why the decision below exists at all.

2.5 ms on every child spawn is not free — it is roughly 5–10% on top of a
fork+exec — so **the rendered profile is cached and invalidated when the policy
changes** (a mount added or removed, a workspace re-root, a config reload). The
cache key is the authored policy, so a stale profile is not representable: if the
policy differs, the key differs. Rendering naively per spawn is rejected as a
tax paid on every command to support a change that happens a few times a day.

`sandbox-exec` already spawns per child, so no new process is introduced. The
real cost is the loss of a boot-time invariant — the profile is currently
computed once, in one place, and this makes it a per-spawn lookup that must not
fail. It is treated as such: a render (or cache-fill) failure aborts the spawn,
matching the Linux contract, rather than falling back to an unconfined child.

### D6 — `$OMNIPUS_HOME` is the single refused mount target

Risky mounts warn and proceed (the operator owns the machine). Mounting
`$OMNIPUS_HOME`, or any path containing it, is refused: it would make
`config.json` and `master.key` writable and let an agent switch off its own
sandbox — the protection this ADR and ADR-062 exist to provide, handed back
through the front door.

### D7 — `Workspace.repository` is deleted, not repurposed

Removed from the wire and from storage, with no back-compat, matching the
ADR-035/ADR-037 precedent. Git linkage becomes a convenience on top of mounting:
paste a URL → clone to a location the operator picks → mount it. Cloning into
Omnipus's own storage is explicitly not the behaviour, since that is the copying
mounts exist to avoid.

---

## 3. Alternatives considered and rejected

**Make the kernel authoritative and delete the app layer.** Attractive — one
enforcement point, at the strongest layer. Rejected: the kernel does not see
in-process tools at all, so `read_file` would become entirely ungoverned on
every platform, and completely ungoverned on Windows where there is no kernel
layer at all.

**Make the app layer authoritative and drop kernel enforcement.** Rejected for
the opposite reason: `bash` and the browser are child processes that never call
our resolver. The app layer cannot see them.

**Keep two rule sets and add a test asserting they agree.** Rejected. A
consistency test only fails when someone edits both lists inconsistently; it
cannot catch a rule that exists in one vocabulary and has no expression in the
other (`FSScope` has no kernel counterpart; `ReadsOpen` has no app counterpart).
It would also have passed happily for the entire period the two lists have been
diverging.

**Bind-mount the operator's folder instead of symlinking.** Rejected: requires
root on Linux, has no clean macOS equivalent, and would put a privileged
operation on the mount path. The realpath-based decision makes the symlink
sufficient.

**Copy the operator's folder into the workspace and sync back.** Rejected: it is
the behaviour the requirement exists to eliminate, and two-way sync of a git
working tree is its own product.

---

## 4. Consequences

### 4.1 Accepted

- **Reads become open for the in-process tools too.** An agent can read files
  anywhere outside the secret set. This is already true via `bash` post-ADR-062;
  D2 stops pretending otherwise for the file tools. The operator explicitly
  chose this posture, and it matches Claude Code and Codex.
- **`FSScope` loses its meaning for reads.** The type stays for writes; the
  reserved `FSScopeAsk`/`FSScopeAllow` values remain unimplemented.
- **A mount is a real grant on the operator's disk.** An agent can delete and
  overwrite files in the operator's repo. Git history is the safety net; the
  uncommitted-and-untracked gap is stated at mount creation rather than papered
  over with a confirmation dialog.
- **The Library's Transfer dialog gains reach.** Once mounts exist, a copy/move
  in the file browser can move files on the real disk. Mounted folders must be
  visually distinct there — a UI requirement created by this ADR, not optional
  polish. **Delivered 2026-08-13**: mounts carry a `mount` object on
  `LibraryEntry`, are drawn with their own icon/colour and their real path in
  the row, and a transfer whose destination is inside a mount names the
  absolute host path before it is confirmed.
- **The Library had to learn about more than one root (D8, added 2026-08-13).**
  Building the UI surfaced a defect that made all of it unreachable: the Library
  browses via an `os.Root` at `work/`, which refuses at the syscall level to
  follow anything resolving outside it. A mount IS a symlink pointing outside,
  so a mounted folder appeared in the listing and failed on click with "path
  escapes the workspace work tree" — visible and unopenable. Resolved by opening
  one `os.Root` per mount rather than by relaxing the single root: browsing
  inside a mount is contained to that folder exactly as `work/` is contained to
  itself. This is the same shape the enforcement engine already uses
  (`fspolicy.FSPolicy.AllowedRoots`), which is the point of this ADR.
- **Two verbs had to be separated (D9, added 2026-08-13).** Multi-root creates a
  data-loss path that did not previously exist: a mount's own entry resolves to
  its root at `"."`, so an unguarded delete calls `RemoveAll(".")` and
  recursively empties the operator's real folder. DELETE and UNMOUNT are
  therefore distinct operations with distinct wording — the row menu offers
  "Unmount · files stay" and does not offer Delete at all — and the engine
  refuses a delete, rename, or copy aimed at a mount's own entry regardless of
  what any client sends.

### 4.2 Risks

| Risk | Mitigation |
|---|---|
| One ruleset means one place to get wrong, for every tool at once | The `confined`-equivalence gate from ADR-062 is reused: the pre-change behaviour must be reproducible and asserted before the new path is switched on |
| macOS per-spawn rendering makes every child spawn depend on a render | Render failure aborts the spawn (never falls back to unconfined); the renderer is already covered by the ADR-062 test suite |
| Symlink-based mounts confuse tools that compare declared vs resolved paths | Enforcement is realpath-only; the symlink is presentation. Existing `ResolvePath` already resolves before deciding |
| Deleting `repository` breaks a client reading it | No back-compat by project convention; it is removed from the contract in the same commit as the generated artifacts |

### 4.3 Not addressed here

Windows kernel enforcement (its own ADR); egress control (its own ADR); remote
or networked mounts (local folders only).

---

## 5. Open questions for the spec round

1. Can two workspaces mount the same folder concurrently, and does anything need
   to coordinate writes?
2. When a workspace is archived or deleted, is the operator's mounted folder
   ever touched? (Presumed never — needs stating normatively.)
3. Is a mount portable between machines, or is a workspace with mounts
   machine-bound?
4. Does the per-turn authored policy get computed once per turn and reused for
   every spawn within it, or recomputed per spawn? (Affects whether a mount
   added mid-turn is visible to a child spawned later in that same turn.)

---

## 6. Accepted residuals — the enforcement boundary is not total

Added after the second review pass. Every item here was DEMONSTRATED against real
sandboxed children, not inferred, and each previously existed only as a Go
comment. That is why this section exists at all: a reviewer greps the decision
records, finds nothing, and marks the area closed. **An accepted security
residual that lives in no decision record is not an accepted residual — it is an
undocumented hole with a note beside it.**

### R-1. The kernel permits LISTING the directories above a turn's own work dir

The app layer refuses; the kernel allows. Measured to be exactly the ancestors of
the work dir and nothing else — every path below agrees in both directions, so a
child still cannot read or write another agent's files.

Not closed because doing so on macOS means emitting an allow AFTER the deny
block, and "nothing follows the deny block" is the single invariant that stops a
stray filtered allow re-opening every secret
(`TestSeatbelt_DenyPrecedenceIsMeasuredNotAssumed`). Trading that to hide a list
of directory NAMES is a bad exchange. Linux does not have this residual: its
grant-based walk never grants the ancestor node.

Pinned by `TestKernelDeniedPaths_MatchIsCarveOutPathForPath`, which fails if the
divergence ever reaches a non-ancestor path or flips direction.

### R-2. Path identity cannot answer for a file that does not exist

`os.SameFile` needs an inode. For a not-yet-created secret the comparison falls
back to strings — case-folded on the deny side, byte-exact on the grant side
(asymmetric on purpose: over-matching only ever denies, while a true answer on
the grant side RE-ADMITS).

Two demonstrated consequences:

- On a normalization-insensitive volume with a non-ASCII `$OMNIPUS_HOME`
  component, an NFC/NFD variant of an ABSENT secret is not matched. Requires a
  mount whose root prefix-matches; without one, write-confinement refuses it
  anyway.
- Backup-prefix coverage was discovered by LISTING at policy-build time, so a
  `config.json.bak-*` that does not exist yet was not matched — plain ASCII, no
  Unicode needed. This one is a defect rather than a residual and is being fixed
  by matching the prefix instead of enumerating.

Closing R-2 fully would mean carrying a Unicode normalization table into
`pkg/fspolicy`, which must stay a stdlib-only leaf to avoid the
`pkg/tools` ↔ `pkg/sandbox` import cycle — and normalization alone would still
miss locale case rules.

### R-3. MCP servers are not a sandbox concern — operator decision

**Operator decision, 2026-08-12: "if someone gives the agent the tool to add an
MCP, or adds an MCP on their own, it is their decision — let's not
overcomplicate it."**

So: MCP server processes are NOT confined, and `add_mcp_server`'s seeded policy
is left as shipped. Granting an agent a tool that runs programs is the
operator's call, and the seed is data they can edit on their own installation.
This matches Claude Code, whose sandboxing doc states a command able to edit its
config could "add a hook or MCP server that Claude Code runs outside the
sandbox".

Two earlier positions are superseded and recorded so they are not re-litigated:
first that this was purely a tool-policy question (too narrow — it ignored that
the two spawn paths differ), then that MCP processes should be confined like
`bash` (over-corrected — it diverges from the competition, risks breaking
legitimate servers, and addresses a threat the operator has explicitly accepted).

One factual correction stands regardless of the policy: three doc comments in
`pkg/mcp/manager.go` assert that the child "inherits the gateway's Landlock
domain" UNCONDITIONALLY. That is true on Linux and false on macOS, where
`restrictCurrentThreadIfNeeded` is a no-op and `applyPlatformHardening` is never
reached. The comments must say so. The confinement is a decision; the comment
describing behaviour that does not occur is simply wrong, and is the exact
defect class this section closes with.

### What this section is really recording

Ten agents worked this branch in parallel and produced ten local truths. The
recurring signature across both review passes was not a coding error — it was a
doc comment written from a verification that was true inside its own file and
false one layer out. A function describing a rendering that had no caller; a
store describing an exploit it did not close; a manager describing Linux
inheritance on macOS.

Prose that runs ahead of enforcement is exactly what a reviewer greps and marks
closed. When in doubt, the rule this branch earned is: **before believing a
comment, check that something calls the thing it describes.**
