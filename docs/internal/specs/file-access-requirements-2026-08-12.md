# Requirements — file and folder access (to-be)

- **Status:** Interview output, operator-confirmed 2026-08-12. Pre-ADR.
- **Supersedes in intent:** the `repository` field on Workspace (to be removed).
- **Depends on:** [ADR-062](../architecture/ADR-062-filesystem-read-exec-model-inversion.md) (reads/exec open, secret set unreachable).

This records what was agreed in the requirements interview, in the operator's own
terms. It is deliberately NOT a design: no file layout, no API shapes, no
mechanism beyond what a requirement forces. An ADR follows.

---

## 1. The problem, in one paragraph

Agents work in a workspace directory Omnipus owns. That is wrong for the most
common real task: the operator already HAS a git repo or a documents folder on
their machine and wants an agent to work in it, in place, without copying
anything. Separately, Omnipus enforces file access in two different places that
disagree with each other, so the same question gets two answers depending on
which tool asks it.

## 2. The two rule layers must become one

> **Correction (2026-08-23, [ADR-068](../architecture/ADR-068-bash-text-guard-third-rule-layer.md)):**
> This section counts two layers. There are **three** — the `bash` tool carries
> its own in-process text guard (`ExecTool.guardCommand`) that runs before any
> child is spawned and is neither `pkg/fspolicy` nor `pkg/sandbox`. R-4's
> diagnosis below is also **backwards**: reads are confined for shell commands
> too, by that third guard. R-1's goal ("one ruleset, written once") and R-4's
> goal ("a rule must not depend on which tool asks") are unchanged and correct;
> the count of what must be unified is one higher than written here.

**R-1. One ruleset, written once.** There is today an application layer
(`pkg/fspolicy`, governing the in-process file tools) and a kernel layer
(`pkg/sandbox`, governing child processes). They currently disagree in BOTH
directions — the app layer denies `agents/` and `workspaces/` that the kernel
grants; the kernel denies `config.json` and `cli.token` that the app layer does
not. This is not a tuning problem, it is two implementations of one idea.

**R-2. The app layer enforces every rule, on every platform.** Behaviour is
then identical everywhere, including Windows and pre-5.13 Linux where no kernel
sandbox exists.

**R-3. The kernel enforces whatever it can, as a second wall.** Where it cannot,
the rule still holds (R-2) but the second wall is absent, and Omnipus says so at
startup rather than implying protection it does not have.

**R-4. A rule must not depend on which tool asks.** `bash cat X` and
`read_file X` must give the same answer. Today they do not: ~~reads are open for
shell commands and confined to the work directory for the file tools~~
**[corrected 2026-08-23, ADR-068 §1.1 — VERIFIED at `e269e52c`: reads are
confined for BOTH. `bash cat X` on an outside path is rejected in-process by
`ExecTool.guardCommand` under the shipped `RestrictToWorkspace: true` default
and never reaches the kernel. The requirement stands; only its stated diagnosis
was wrong.]**. The
operator identified this as a bug, and it is — it is also what made an earlier
version of these requirements argue for a feature (read-only mounts) that only
made sense while the inconsistency existed.

> **Confirmed against the competition.** Claude Code has ONE permission layer
> that "appl[ies] to every tool: Bash, Read, Edit, WebFetch, MCP", plus OS
> sandboxing that "applies only to Bash commands and their child processes".
> That is R-1 through R-3 exactly. Our two-rulesets arrangement has no
> counterpart in either Claude Code or Codex.

## 3. Where agents work

**R-5. By default an agent works in its workspace.** Unchanged, and it stays the
default for every workspace that does not mount anything.

**R-6. Agents do not write at the top level of `$OMNIPUS_HOME`.** Already the
intent — agents default to `RestrictToWorkspace: true`, and the app layer
already carves out `agents/`, `workspaces/`, `entities/`, `master.key` and
`credentials.json`. Stated here because the kernel layer was broader than this
intent, and the ADR-062 work narrowed it toward it.

## 4. Mounts

**R-7. A workspace may mount local folders.** The operator points a workspace at
an existing folder on disk and the agent works in it directly. No copying.

**R-8. A mount appears as a NAMED ITEM INSIDE the work area.** e.g.
`work/my-repo` → `/Users/you/projects/foo`. Not a replacement for the work area.
This allows several — a code repo and a documents folder on one workspace — and
keeps a stable root for Omnipus's own per-workspace files, so nothing of ours
lands inside the operator's repo.

**R-9. A mount is a WRITE grant, and nothing else.** Under R-4 reads are open to
every tool, so a "read-only mount" would be indistinguishable from not mounting
at all. Mounts are therefore always writable and carry no read-only option. This
is a direct consequence of R-4; if R-4 were ever reversed, R-9 must be revisited.

**R-10. Writes stop at the mount boundary.** A symlink inside a mounted folder
pointing elsewhere on disk does NOT extend write access to the target. Reading
through it still works (reads are open). Rationale: the operator granted one
folder, and the UI shows one folder; a link inside a third-party repo must not
silently become a second grant.

**R-11. Mounts belong to the workspace, not to an agent.** Every agent on the
workspace gets the mount. A workspace is the unit of shared work and already has
a team.

**R-12. Mounts take effect immediately.** No restart. Requiring one is the kind
of friction that makes operators disable the sandbox. **Cost, accepted:** macOS
currently renders its Seatbelt profile once at boot, so this requires rendering
per child spawn. Linux already rebuilds per spawn, so this brings the two into
line rather than adding a platform quirk.

**R-13. A missing mount does not block anything.** Unplugged drive, renamed
folder, workspace restored on another machine: Omnipus starts normally, the
mount shows as broken, and an agent that uses it gets a clear reason instead of
a permission error. It is NEVER silently recreated as an empty directory — an
agent would then write into a new empty folder believing it was the project.

## 5. Who may mount

**R-14. The operator creates mounts.** Mounting grants access to part of the
disk; it is a human decision.

**R-15. An agent may REQUEST a mount, which the operator approves.** For the
case where an agent discovers mid-task that it needs a folder.

**R-16. An approval is permanent until revoked.** An approved request becomes an
ordinary mount, visible and deletable in the UI. Deliberately not per-turn or
per-session: repeated prompts train people to approve without reading.

**R-17. Risky targets warn, they do not refuse.** The operator owns the machine.
Mounting a home folder or a broad tree produces a clear warning about what it
grants, and then proceeds.

**R-18. Exactly one exception to R-17: `$OMNIPUS_HOME` is refused.** Mounting
Omnipus's own directory would make `config.json`, `master.key` and the
credential vault writable, letting an agent disable its own sandbox, read the
gateway token, or destroy the vault. This is not overruling the operator about
their files; it is refusing to hand out the key to the thing doing the
protecting. Operator-confirmed as the single carve-out from R-17.

## 6. Git

**R-19. The workspace keeps its own Omnipus evidence git; a mounted repo leads
inside the mount.** Omnipus does not touch the operator's git history. Changes
inside a mount are recorded by the operator's own repository.
**Verified feasible:** the nested-repo check inspects the work directory and its
ancestors, not its children, so a mount at `work/my-repo` does not trip it and
the evidence repo still initialises at `work/`. One mechanism is required — the
mount must be excluded from the evidence git, or it will try to commit the
operator's repo contents as its own.

**R-20. No extra guard on destructive operations inside a mount.** The agent
works in the repo as the operator would; git history is the safety net. The real
exposure — uncommitted and untracked files, which git cannot restore — is stated
plainly when the mount is created rather than papered over with a confirmation
dialog.

**R-21. Remove the `repository` field from Workspace.** It stores a URL that
nothing reads: a control that looks functional and does nothing. Replaced by
R-22.

**R-22. Git linkage is a convenience on top of mounting.** Paste a URL → Omnipus
offers to clone it to a location the operator chooses → mounts that folder. One
flow covers both "folder I already have" and "repo I do not have yet". Cloning
into Omnipus storage is explicitly NOT the behaviour: that is the copying this
whole feature exists to avoid.

## 7. Surface

**R-23. Mounting and git linkage live in the Library (the file browser).** The
Library is already a workspace-scoped explorer with a virtual root, so a mount
appearing as a named child of the workspace IS the R-8 model rather than an
illustration of it. It also puts the grant next to the files it affects.

**R-24. A mounted folder must be visually distinct from workspace-owned
content.** The Library has a Transfer (copy/move) dialog; once mounts exist,
that dialog can move files on the operator's real disk. A drag that used to
shuffle workspace files can now edit a repo.

> **Sequencing consequence, not a requirement:** `pkg/library` resolves through
> `workspace.SafeWorkDir`, which by construction cannot address anything outside
> the workspace. The Library work therefore depends on the unified engine (§2)
> and cannot be built in parallel with it.

## 8. Explicitly out of scope

- Windows kernel enforcement — its own ADR, already deferred.
- Network/egress control — its own ADR.
- Remote or networked mounts (S3, SSH, network shares). Local folders only.

## 9. Open, not yet asked

- Can two workspaces mount the same folder at once, and does anything need to
  coordinate writes?
- What happens to a mount when its workspace is archived or deleted — is the
  operator's folder ever touched? (Presumed never; needs confirming.)
- Does a mount survive an Omnipus data-directory move, and is a mount portable
  between machines at all?
