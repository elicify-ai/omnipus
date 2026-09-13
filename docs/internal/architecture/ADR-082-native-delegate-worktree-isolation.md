# ADR-082 — Opt-in worktree isolation for native coding delegates

- **Status:** Proposed (2026-09-13)
- **Date:** 2026-09-13
- **Deciders:** Daniel Piatkowski (operator)
- **Branch:** `feat/lsp-and-native-worktrees` (off `release/v0.1.1`)
- **Amends [[ADR-032]] decision 1 (narrow exception):** CoreTeam cwd remains `workspaces/<id>/work/` **unless** this ADR’s isolate path is taken. Isolate is native-only, opt-in, and re-roots **that child** to a worktree of an explicit **user** `project_root` under the workspace/mounts. Parent now contributes filesystem topology for that run (narrowing “inherit nothing” for cwd only). A dated amendment must be added on ADR-032 when this is accepted — not implied.
- **Does not use:** the workspace `work/` **evidence git** (`pkg/gitevidence`, ADR-053 §3d) as a coding repo.

## 1. Problem

Jim (or any parent) can `delegate` two **native** writers (General Purpose, a future Software Implementer) at once. Both run in the **same** `workspaces/<id>/work/` directory (ADR-032). They overwrite each other’s files and can corrupt a **user** git index.

ADR-032 **removed** per-run `git worktree` isolation from the live dispatch path because the isolated copy hid the real files from external CLIs. `pkg/agent/runner/worktree.go` (`PrepareWorkspace` / `Teardown`) and `reaper.go` still exist but are **not** called from `spawnSubTurn` / `runExternalCLISubTurn`.

A naïve “if the folder is git, isolate” gate is **wrong**: almost every `work/` dir is git because Omnipus auto-inits a **hidden evidence repo** (marker `.git/omnipus-gitevidence.marker`) so the Judge can snapshot write-sets. That is not a GitHub project. Hanging coding worktrees on it would fight evidence commits and the Judge’s own isolated checkout.

A naïve “always isolate on `delegate`” is also **wrong**: Researcher, Planner, mail, and one sequential GP job do not need a second checkout.

## 2. Decisions

### D1 — Isolation is opt-in, not the default `delegate`

Default `delegate` is **unchanged from live code**, not from ADR-032’s original “private `agents/<id>/`” sentence: CoreTeam members already **must** run in `workspaces/<id>/work/` (`workspace_reroot.go`); agents with no workspace membership are **refused**. Isolation does **not** weaken that membership check.

When `isolate: true` **and** D2 accepts a `project_root`, the engine re-roots **that child only** to a worktree of `project_root`. That is a **narrow exception** to “cwd is `work/`”: the child still cannot escape the workspace/mounts; it is a subdirectory (or authorized mount) **inside** them.

The flag is **call-scoped** (this `delegate` invocation). Omitting it = today’s cwd. A model can still forget the flag (two writers collide) — accepted; we do not infer isolate from `.git`. Unsupported target (D3): **reject** the isolate request (do not silently skip).

### D2 — Explicit project git; never the evidence git

The caller **must** pass `project_root` (absolute or workspace-relative directory that is the **user** git top-level, e.g. `work/my-app`). The engine does **not** guess by walking from `work/`.

Classification (deterministic):

1. Resolve `project_root` with `git rev-parse --show-toplevel` and `--git-common-dir` (so a `.git` **file** / linked worktree is classified correctly).
2. If that common dir contains `omnipus-gitevidence.marker` → **refuse** (that is ours).
3. If `project_root` is outside the child’s authorized workspace/mounts → **refuse**.
4. If git metadata exists but the marker is not yet written (init race) → **refuse** (ambiguous).
5. Nested clone `work/my-app/`: `project_root` **must** be `my-app`, never `work/`.

If `isolate: true` and any of the above fail: **fail closed** — no worktree, no fallback to evidence git, no empty temp dir (that was ADR-032’s “CLI saw no files” bug). Do **not** reuse `PrepareWorkspace`’s “any git + detach + empty-dir fallback” behaviour; new helper only.

### D3 — Only writer children

Even with the flag, skip isolation when the **target** cannot write the tree (no `write_file` / `edit_file` / `bash` allow). Researcher and Planner stay in the real `work/`.

### D4 — Native only in this ADR

This ADR covers **native** Omnipus-loop children. External-CLI (`subagent_3p`) stays in the real workspace with the existing same-path **lock** (ADR-032). Re-introducing worktrees on the CLI path is a separate ADR; it would re-open the “CLI saw no files” bug unless the worktree is a checkout **of the user project** with the right files, not an empty scratch dir.

### D5 — Lifecycle (do not destroy the child’s work)

**Start:** require the user repo working tree to be **clean** (no uncommitted/untracked that the child needs — otherwise HEAD-only checkout repeats ADR-032’s missing-files bug). `git worktree add -b <run-branch> <dir> <base-rev>` from **pinned HEAD**. Record `{project_root, branch, base_rev}` on the child session. Reuse `RunsRoot` / `run-` prefix for the directory so the reaper can see orphans. Child FS tools re-root to that dir.

**End (success):** the child (or engine) **commits** on `<run-branch>` before remove (empty commit allowed only if no changes). Return to the parent: repo, branch, `base_rev`, `output_rev`. Then `git worktree remove` (non-force). Do **not** auto-merge.

**End (failure / cancel):** do **not** `--force` remove a **dirty** worktree. Keep the checkout (or commit to `wip/<run-id>` first). Crash reaper: **do not** `remove --force` dirty coding worktrees; log and leave them. (Today’s `Teardown` force-delete is **forbidden** for this path.)

**Follow-up / warm resume:** same `run-id` reuses the same worktree dir and branch if it still exists.

**Evidence / Judge:** isolated edits live on the **user** git branch, not in `gitevidence`. Plan evidence stays on `work/`’s marked repo and will **not** automatically include `my-app/` worktree files unless the task write-set is under `work/` **and** a later, explicit capture copies/merges. v1: Judge evidence for isolated coding tasks is **the user-git diff `base_rev..output_rev`**, not gitevidence. Do not claim evidence HEAD is “untouched” as a substitute for this rule.

**Setup failure** (git missing, add fails, dirty source, ambiguous git): fail closed — no empty temp dir (`PrepareWorkspace` fallback is prohibited).

### D6 — API: `project_root`, not a boolean

`delegate` gains optional `project_root` (string). Presence **means** isolate (no separate `isolate: true`). Omit = today’s cwd. `project_root` empty/invalid = **reject**. Per-agent default “always isolate writers” is **out of v1** (easy to isolate sequential jobs by accident).

### D7 — Apply isolation on the existing work-dir override seam

Do **not** set cwd in `spawnSubTurn` and then let `runTurn` / `resolveTurnWorkDirOrRefuse` snap back to `work/`. Use the same override seam as ADR-053 Play-from-commit (`WithResumeWorkDirOverride` or a sibling `WithIsolateWorkDir`). **Precedence:** isolate override **after** CoreTeam `work/` resolution, **before** the turn starts; if a resume-from-commit override is also set, **reject** (do not compose two isolation worlds).

### D8 — GitGuard, `os.Root`, and bash: allow-list the user gitdir

A linked worktree’s `.git` is a **file** pointing at `<user-repo>/.git/worktrees/<name>`. `os.Root` on `runner-runs/run-*` cannot see that gitdir. GitGuard today treats **any cwd under `work/`** as the evidence repo (`enclosingProtectedWorkdir`) — a child still under `work/my-app` cannot `git commit` on the user clone.

For an isolated native child the engine must:

1. Re-root app-level FS to the worktree dir.
2. Allow git operations against the **user** repo’s common dir (not `work/.git` / not the evidence marker).
3. State honestly: Landlock still grants `$OMNIPUS_HOME` today, so **bash can still write the sibling `work/`**. v1 file-tool isolation is real; **bash clobber of `work/` is an accepted hole** unless a later sandbox ADR shrinks the kernel grant. Do not claim “without clobbering” for bash.

### D9 — Do not call `PrepareWorkspace`

Reuse only `RunsRoot` / `run-` naming / reaper **scan**. New prepare/teardown. Quarantine `PrepareWorkspace` (detach + empty-dir fallback) — never on this path.

### D10 — Not a new chat tool in v1

No `EnterWorktree` / `ExitWorktree`.

## 3. Consequences

**Positive:** two native Implementers can edit the same user project without clobbering. Evidence git and Judge stay untouched if D2 is held. Default `delegate` (PA, research, planning) stays simple.

**Negative:** disk and git metadata for isolated runs; operator must have `git` on PATH (already true for coding). Fail-closed when isolate is set without a user repo — Jim must not spray `isolate: true` on every delegate (orchestrate skill / Software pack).

**Accepted:** isolation is **requested**, not inferred from `.git`. Agent behaviour can still set the flag wrongly; the engine **deterministically** refuses to worktree the evidence repo.

## 4. Alternatives not taken

| Option | Why not |
|---|---|
| Always isolate every native delegate | Hits non-coding work; treats evidence git as a project |
| Infer “is git” | Almost every `work/` is git (evidence layer) |
| Infer “two writers already running” | Easy to get wrong; races |
| Restore worktrees for external CLI in this ADR | Repeats ADR-032’s empty-copy failure unless scoped to user git; out of scope |
| Isolation as a tool the child must call | Model-dependent; two children that forget still collide |

## 5. Out of scope

- `lsp` tool (same branch, separate change).
- Merging grep (ADR-081 on the search line — number reserved there; this file is **082** so the two lines do not collide).
- Changing `gitevidence` auto-init.
- Google/Microsoft calendar.
- Making General Purpose inherit parent tools (still ADR-032: other seats keep their own belt; **self-delegate** remains same identity, same belt, still no isolate unless flagged).

## 6. Verification (when implemented)

- Isolate + user git at `work/my-app/` → child cwd is a worktree of `my-app`, not `work/`.
- Isolate + **only** evidence git → error, no worktree on the marked repo.
- Isolate omitted → byte-for-byte ADR-032 cwd.
- Isolate + Researcher (no write/bash) → no worktree.
- Two isolated native writers → two worktree dirs; teardown removes both; evidence HEAD unchanged by worktree add/remove.
- Reaper still collects `runner-runs/run-*` orphans.

Local: `CGO_ENABLED=0 go test -tags goolm,stdjson -run 'IsolateProjectRoot|IsolateEvidenceRefuse' -p 1 ./pkg/agent/...` — **do not** treat existing `pkg/agent/runner/worktree.go` tests as covering this ADR.

Also assert: no `worktrees/` entries under **evidence** `work/.git`; child cwd is a worktree of the **unmarked** repo; dirty isolate is not `--force` removed; `project_root` missing → error; isolate + resume-override → error.

## 7. External reviews (2026-09-13)

**Codex CLI** (`codex exec`, gpt-6-astra): **reject** first draft. BLOCK: (1) branch retain ≠ uncommitted edits / force-delete; (2) walk-up discovery not deterministic; (3) Judge/gitevidence vs isolated cwd unspecified; (4) D1 cwd text obsolete vs CoreTeam reroot. WARN: leftover `PrepareWorkspace`, flag vs policy, HEAD-only missing files, follow_up lifetime.

**Claude Code CLI** (`claude -p`): **reject** first draft. BLOCK: walk-up cannot see `work/my-app`; claiming not to repeal ADR-032 is false; `PrepareWorkspace` footgun; GitGuard / `os.Root` / bash `$OMNIPUS_HOME`; skip vs fail-closed; boolean API; second isolation vs ADR-053 D10.

This file’s D1–D10 after that pass adopt: explicit `project_root`, fail-closed, no evidence worktree, commit-before-remove, no force-delete dirty, override seam, GitGuard allow-list, quarantine `PrepareWorkspace`, ADR-032 amendment required, bash clobber hole stated. **Status stays Proposed** until those tests exist and ADR-032 is amended.
