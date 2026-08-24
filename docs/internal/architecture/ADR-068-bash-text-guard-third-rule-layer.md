# ADR-068 — The bash text guard is a third rule layer, and it never implemented ADR-062

- **Status:** Accepted (2026-08-23) — the founder ratified §2.1 **option (A)**: align the bash text guard with ADR-062. Reads outside the working directory are allowed; writes outside it continue to require a mount.
- **Date:** 2026-08-23
- **Deciders:** founder (decides §2.1), lead (mechanism)
- **Corrects:** [ADR-063](ADR-063-unified-file-access-engine-and-mounts.md) §1.1 (a claim marked VERIFIED that is false in shipped code); [file-access requirements](../specs/file-access-requirements-2026-08-12.md) R-1 and R-4
- **Builds on:** [ADR-062](ADR-062-filesystem-read-exec-model-inversion.md) (reads/exec open, writes confined)
- **Evidence level:** claims marked **[VERIFIED]** were executed against the shipped code at commit `e269e52c` on macOS (Darwin 25.5.0) via throwaway Go tests calling the production functions directly. Claims marked **[INFERRED]** are reasoned from code reading, not run.
- **Origin:** UAT defect reports 002 and 003 (2026-08-22). Both reports misattribute their own cause; see §5.

---

## 1. Decision

The bash tool's in-process text guard (`ExecTool.guardCommand`,
`pkg/tools/shell.go`) is a **third file-access rule layer**. ADR-063 and its
spec both model the system as having exactly two. Every conclusion either
document draws from "there are two layers" needs re-reading against three.

That third layer **still enforces the pre-ADR-062 confined model for reads**.
ADR-062 opened reads; the bash guard never changed. It has been the actual
behaviour of every `bash` command since ADR-062 was accepted.

We align the bash text guard with ADR-062: **reads outside the working
directory are allowed; writes outside it continue to require a mount.**
The guard stops treating both the same.

### 1.1 Why this is not a tuning change

ADR-063 §1.1 states, marked **VERIFIED (2026-08-12)**:

> `bash cat ~/notes.txt` succeeds; `read_file ~/notes.txt` is denied.

and tabulates "anything outside `WorkDir`, read" as **allowed (ADR-062)** for
the kernel layer.

**[VERIFIED]** This is false in shipped code. Calling the production
`guardCommand` directly with the shipped default (`RestrictToWorkspace: true`,
`pkg/config/defaults.go:32`):

| Command | `restrictToWorkspace: true` (shipped default) | `false` |
|---|---|---|
| `cat /Users/<user>/notes.txt` | **BLOCKED** — "path outside working dir" | allowed |
| `ls /Users/<user>/Desktop/` | **BLOCKED** — "path outside working dir" | allowed |
| `cat /etc/hosts` | **BLOCKED** — "path outside working dir" | allowed |

The command never reaches the kernel layer. It is rejected in-process, by text
inspection, before any child is spawned. ADR-063's two-layer table describes the
kernel's disposition toward a command the kernel never sees.

This inverts the gap the spec set out to close. Spec R-4 says:

> `bash cat X` and `read_file X` must give the same answer. Today they do not:
> reads are open for shell commands and confined to the work directory for the
> file tools.

**[VERIFIED]** The direction is backwards. Reads are confined for shell commands
too — by a different mechanism, in a different package, that R-1's "two
implementations of one idea" never counted. R-4's goal (one answer regardless of
which tool asks) is correct and unchanged; its stated diagnosis is not.

## 2. What the third layer actually does

`guardCommand` runs three checks in order:

1. **Deny patterns** (`applyDenyPatterns`) — regex over the command text.
   Unconditional: runs regardless of `restrictToWorkspace`, and FR-B4 forbids
   disabling it.
2. **Substitution guard** (`substitutionGuard`) — structural, also unconditional.
3. **Path containment** (`checkPathSegment`) — gated on `restrictToWorkspace`,
   which returns early at `shell.go:907` when false.

Only step 3 is gated. Steps 1 and 2 apply even with every sandbox setting off.

`checkPathSegment` does **not distinguish read from write**. Any absolute path
outside the working directory is rejected for any operation, with one escape
hatch: a workspace mount covering that path.

### 2.1 The decision that needs ratification

Aligning the guard with ADR-062 widens what agents may read via `bash` — from
"the working directory plus mounts" to "anything the OS user can read, minus the
secret set". That is precisely what ADR-062 decided and what §7 of that ADR
accepted as residual risk. It has simply never been true on the bash path.

Two coherent options:

- **(A) Align — recommended.** Reads open, writes still need a mount. The
  product stops holding two contradictory positions, and spec R-4 becomes true
  rather than aspirational.
- **(B) Keep bash strict, and amend ADR-062** to say reads-open does not apply
  to the shell path. Defensible, but it makes R-4 permanently unreachable and
  keeps the answer dependent on which tool asked.

**Ruled 2026-08-23: (A).** The founder accepted alignment. The choice was a
security-posture judgement, not an implementation detail, which is why it was
escalated rather than decided in code.

### 2.2 Mounts are load-bearing for the wrong thing

**[VERIFIED — code read]** `AllowedMountRoots` (`pkg/workspace/mount.go:388`)
documents its own contract:

> A mount grants WRITE and nothing else — reads are already open under ADR-062
> regardless of this list.

But because `checkPathSegment` blocks reads too, a mount is currently the *only*
way to read an outside path from `bash`. Operators are creating write grants to
obtain read access — exactly what defect report 002 describes, and why mounting
Desktop appeared to be the fix.

Under (A) this resolves itself: the mount goes back to granting only writes.

### 2.3 A latent fragility in mount matching

**[INFERRED — code read, not executed]** `Mount.HostPath` is realpath-resolved
(symlink-free) at create time (`pkg/workspace/mount.go:37`). `checkPathSegment`
resolves its candidate with `filepath.Abs` — lexical, symlinks not followed —
and `matchedAllowedRoot` → `isWithinWorkspace` compares by string prefix. The
guard's own comment at `shell.go:936` concedes it "matches lexically where the
kernel enforces on realpaths".

When the two forms disagree, an approved mount silently stops matching and the
write is refused with "no mount covers it" despite the mount existing. Under (A)
this stops affecting reads, but still affects writes. Resolve the candidate
before matching.

## 3. The heredoc deny pattern has never fired

**[VERIFIED]** `applyDenyPatterns` lowercases the command
(`lowerASCII`, `shell_guard.go:32`) before matching. `defaultDenyPatterns`
contains:

```go
regexp.MustCompile(`<<\s*EOF`)
```

Uppercase `EOF` cannot match a lowercased string. The pattern is unreachable and
always has been. It is the only uppercase-literal pattern in the list, and no
test in `pkg/tools` covers heredocs at all.

**Decision: delete it.** Making it fire would newly block heredocs that work
today — a behaviour regression introduced under the banner of a fix. It has
protected nothing for its entire existence; removing it changes no behaviour and
stops the next reader trusting a guard that is not there.

**[VERIFIED]** Against the same production function, none of the shell-write
forms defect 003 reports as blocked are blocked by the pattern layer:

| Command | Deny-pattern verdict |
|---|---|
| `cat > Desktop/test.md << EOF …` | allowed |
| `printf "hello" > mounted-folder/file.md` | allowed |
| `python3 -c "open('p','w').write('x')"` | allowed |
| `echo "…" \| base64 -d > Desktop/test.md` | allowed |
| `git push origin main` | blocked |

## 4. Mount discovery is genuinely missing

`request_mount` exists (`pkg/tools/request_mount.go`) with no counterpart for
asking what is currently mounted. An agent can request access and cannot
enumerate what it has. Defect 003's Issue 1 is valid as written.

Add `list_mounts`, returning per mount: alias, host path, permission level,
status, approval timestamp. The data already exists — `LoadMounts` and
`MountStatus` both read it — so this is a tool surface over existing state, not
a new subsystem.

> **Correction (2026-08-23, at implementation).** "The data already exists" is
> true for alias, host path and status; it is **false for approval timestamp**.
> `Mount` is `{name, host_path}`, the store record adds only `workspace_id`, and
> `CreateMount` records no time — so the field was omitted rather than
> fabricated from a symlink mtime (which describes the symlink, not the
> approval, and does not survive a workspace restore). Surfacing it properly
> needs a deliberate `Mount.ApprovedAt` schema addition with a back-compat story
> for existing stores; that is a separate decision, not something a read-only
> tool should imply exists. A test pins the payload as carrying no
> `approved_at`, so adding one has to be deliberate.
>
> Permission level is likewise reported as the constant `write`, not a stored
> field: there is no per-mount permission column, and "a mount grants WRITE and
> nothing else" is a system-wide invariant `AllowedMountRoots` states in its own
> contract. Status has two values (`ok`/`broken`), not the three this section
> implies — `MountStatus` models no `revoked` state.

## 5. Both defect reports misattribute their cause

Recorded because it will otherwise be rediscovered:

- **Report 003** attributes blocked writes to the deny-pattern layer
  ("the safety guard appears to pattern-match on shell syntax"). §3 shows that
  layer allowed every one of those commands. The actual cause was the path
  containment check — i.e. defect 002.
- **Report 003's** "base64 bypasses the guard" concern is void. Plain redirects
  were never pattern-blocked, so base64 evaded nothing.
- **Report 002** attributes the block to the sandbox setting being ignored. The
  sandbox toggle (`sandbox.mode`) and the guard's control
  (`RestrictToWorkspace`) are genuinely different settings; neither is ignoring
  the other. See §6.

Implementing either report as written would produce no behaviour change and a
defect marked "not fixed".

## 6. `RestrictToWorkspace` has no operator-facing control

**[VERIFIED — code read]** `pkg/config/config.go:1542`:

```go
RestrictToWorkspace bool `json:"-" env:"OMNIPUS_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE"`
```

`json:"-"` means it is never serialized, and `validateRemovedKeys` **rejects**
any v1 config JSON carrying the key. The field is reachable only through an
environment variable, described in-code as "an intentional ops escape hatch".

The consequence is the operator experience defect 002 reports: the security page
shows a sandbox switch, the switch is off, and commands are still blocked by a
different setting with no switch at all.

**Decision:** give it a visible control, labelled distinctly from the kernel
sandbox, and make the block message name which rule fired and where to change
it. An unexplained denial is what drives operators to disable the whole boundary
— the failure mode ADR-062 §2 already identified.

## 7. Consequences

- Spec R-4 becomes achievable on the read axis for the first time.
- ADR-063 §1.1's two-layer model must be read as three throughout; its
  disagreement table is incomplete, not merely imprecise.
- Under (A), `bash` reads widen to the ADR-062 posture. The secret-set carve-out
  is unaffected: `IsCarveOut` and `DeniedPathsFor` take no `AllowedRoots`
  parameter, so no mount and no read-open can reopen a secret
  (`pkg/fspolicy/mount_secret_independence_test.go` asserts this structurally).
- Deleting the heredoc pattern changes no runtime behaviour.
- Operators lose the accidental confinement they may have been relying on
  without knowing it. This is the substance of the §2.1 decision.

## 8. What this ADR does not cover

UAT defect 004 (a worker agent is not pre-selected in the chat agent picker) is
**not** an architectural decision and gets no ADR. It is two independently
correct behaviours colliding: `useChatAgents` filters workers out of the pickable
list (`src/hooks/useChatAgents.ts:84`), and `AgentPicker`'s auto-select effect
takes `chatAgents[0]` when nothing is selected
(`src/components/chat/composer/AgentPicker.tsx:129`). A worker session therefore
selects the first non-worker agent. The fix — bind the session's worker
explicitly and disable the trigger — is a bug fix against existing intent, not a
change of intent.

## 9. Two limits the read exemption carries (found in review, 2026-08-23)

An independent review of the implementation found three HIGH issues, all
reproduced against the real `guardCommand` before fixing. Two of them narrow
what §1's decision actually means, so they belong in the decision record and not
only in a commit message.

### 9.1 The allow-list must name commands EXACTLY

`shellCommandHead` normalises for the DENY path — it lowercases and strips a
directory prefix, so `/bin/rm` is judged as `rm`. Correct there; a bypass when
reused for an allow list. `./cat`, `bin/cat` and `CAT` all normalised onto the
allowlisted head `cat`, and an agent may freely write an executable named `cat`
into its own working directory — which would then be classified as a **proven
read** while being an arbitrary program with the account's full write access.

Fixed by `shellCommandHeadDetailed`, which reports whether normalisation changed
the token; a changed token is refused. The cost is that absolute spellings
(`/bin/cat f`) are no longer provable reads. That is the doctrine working as
intended: prove a read, or call it a write.

The general lesson: **a normaliser built for a deny list inverts into a bypass
when reused for an allow list.** Anything else in this codebase that reuses
deny-path helpers for a grant decision deserves the same look.

### 9.2 Opening reads does NOT open the secret set

The §1 exemption as first written granted every outside-`WorkDir` read,
including the per-turn secret roots `agents/` and `workspaces/`. The
`secretGuardPatterns` text backstop covers only `SecretEntriesAlways`
(`config.json`, `master.key`, `cli.token`, `entities`) and deliberately not the
per-turn roots — those had been out of reach only as a side effect of blocking
every outside path. Another agent's `SOUL.md` and another workspace's work tree
became readable from `bash` wherever the kernel layer is absent or off, which
`pkg/sandbox/derive_from_fspolicy.go` records as a previously-fixed bug.

Fixed by consulting **`fspolicy.IsCarveOut`** against the turn's resolved
policy before granting any read exemption. When the policy cannot be resolved
the exemption is withheld entirely, so an error path can never widen access.

The first attempt used `fspolicy.DeniedPathsFor` and was wrong twice over — both
caught on a second review pass, and worth recording because each is a general
trap:

1. **Root granularity is not path granularity.** `DeniedPathsFor` re-admits the
   whole per-turn root containing the work dir, so during a workspace turn every
   OTHER workspace was re-admitted along with the caller's own.
   `pkg/sandbox/derive_from_fspolicy.go:160` already warns against precisely
   this substitution, in writing, having been bitten by it before.
2. **A deny list matched lexically is inert against an alias.** The roots were
   built from `config.OmnipusHomeDir()` (`/var/…`) and compared by string prefix
   against candidates in resolved form (`/private/var/…`), so on macOS the
   exclusion matched nothing at all. This is the SAME lexical-vs-realpath bug
   §2.3 fixed on the allow side for mount roots — fixed there, then reintroduced
   on the deny side a few lines away.

   Its verification passed anyway, because the probe used the unresolved form
   for both root and candidate. **A test that constructs both sides from the
   same unresolved value cannot see a resolution bug.**

`IsCarveOut` answers per path with no directory listing, applies the own-tree
exception per root, and resolves by filesystem identity rather than string
prefix — so it is immune to both traps. `KernelDeniedPathsFor` is also correct
but enumerates the sibling set from disk on every call (thousands of entries per
bash command), which is the wrong shape for a per-command text guard.

It is also the primitive the app-layer file tools already call, so `bash cat X`
and `read_file X` now agree about the secret set — spec **R-4** ("a rule must
not depend on which tool asks"), which §1.1 found had never been true on the
bash path.

### 9.3 A rule must not depend on how the path is SPELLED (round 3)

A third review pass found the read exemption still reachable for a secret-set
file — through a different spelling of it.

`absolutePathPattern` treats `~` and a token-start `$VAR` as **boundary**
characters and captures only the suffix. So
`$OMNIPUS_HOME/agents/other/SOUL.md` reached the guard as the candidate
`/agents/other/SOUL.md` — a path naming no real file. It is outside the work
dir, no mount covers it, the head is `cat` so `readOnly` is true, and
`IsCarveOut` cannot match a carve-out root against a path that never contained
`$OMNIPUS_HOME`. The guard allowed it; the shell then expanded the variable and
read the real file. Measured, with the literal spelling of the same file
correctly refused:

| Command | Before |
|---|---|
| `cat <home>/agents/victim/SOUL.md` | BLOCKED |
| `cat $HOME/.omnipus/agents/victim/SOUL.md` | **ALLOWED** |
| `cat ~/.omnipus/agents/victim/SOUL.md` | **ALLOWED** |

The write half was escapable the same way, and this half predates ADR-068: a
fabricated suffix can be made to land **inside** the work dir
(`printf x > $HOME<abs-cwd>/pwned`), so `checkPathSegment` returned "" before
reaching the mount check at all, while the real target was outside every mount.
`~/dev/null` likewise reached the `safePaths` exemption while bash wrote a real
file at `$HOME/dev/null`.

**Fixed by resolving the prefix at the scan site**, not by refusing expansions.
`~`, `$HOME` and `$OMNIPUS_HOME` are expanded before judgement, so the real path
faces the same checks its literal spelling does; any other `$VAR` is refused,
because its value is unknowable to this process. The first attempt refused every
expansion-derived candidate and broke `cat ~/notes.txt` — an ordinary read this
ADR exists to permit, and one an existing test already pinned.

**The general rule this makes explicit:** a path guard that reasons about
command TEXT must judge the path the shell will actually open, not the substring
its own regex happened to capture. Where it cannot know that path, it must
refuse — never fall through to judging a fragment.

### 9.4 Still open — the carve-out set omits session transcripts

Round 3 also measured `cat <home>/sessions/<id>/<date>.jsonl` as **allowed** by
its plain literal absolute path — no expansion trick needed.
`SecretEntriesAlways`/`PerTurn` cover `agents` and `workspaces` but not
`sessions/`, `memory/`, `tasks/`, `uploads/`, `media/` or `logs/`.

A cross-agent session transcript contains the other agent's persona, the user's
messages and every tool result in that turn — a superset of what
`agents/<id>/SOUL.md` discloses. Denying `agents/` while `sessions/` stays open
is the "deny that reads as correct and protects nothing" shape
`SecretEntriesAlways`'s own doc comment warns about.

This is an **ADR-063 scope decision about the carve-out set**, not a bug in this
guard, so it is recorded here rather than changed unilaterally — but the ADR-068
read exemption is what makes it reachable from `bash` for the first time, so it
should be decided before this ships broadly.

### 9.5 §1 restated, precisely

Reads outside the working directory are allowed **when the command head is an
exact, unqualified allow-listed name, and the path is not in the turn's secret
set**. Everything else — including anything the classifier cannot prove — is a
write and needs a mount.

## 11. Implementation order

1. Delete the dead `<<\s*EOF` pattern; add a test asserting heredocs are
   permitted, so the removal is pinned.
2. Split read from write in `checkPathSegment` (gated on the §2.1 ruling).
3. Resolve the candidate path before mount matching (§2.3).
4. Add `list_mounts`.
5. Surface `RestrictToWorkspace` and improve the denial message (§6).
6. Correct ADR-063 §1.1 and spec R-1/R-4 to describe three layers.
