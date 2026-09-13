# ADR-082 — Opt-in worktree isolation for native coding delegates (v2)

- **Status:** Proposed (2026-09-13); **v2** after Codex + Claude Code CLI **reject** of v1 (see §7).
- **Date:** 2026-09-13
- **Deciders:** Daniel Piatkowski (operator)
- **Branch:** `feat/lsp-and-native-worktrees` (off `release/v0.1.1`)
- **Amends [[ADR-032]] decision 1:** CoreTeam cwd remains `workspaces/<id>/work/` **except** this native-only isolate path. Isolate re-roots **that child** to a git worktree of an explicit **user** `project_root`. The parent contributes **cwd topology** for that run (narrows “inherit nothing” for working directory only). When this ADR is Accepted, ADR-032 **must** gain a dated amendment stating that exception — not implied here.
- **Composes with [[ADR-053]] D10:** evidence isolation (Judge snapshots on the **marked** `work/` git) and this coding isolation (user git) are **two worlds**. They are never composed on one turn (D7). Isolated coding evidence for the Judge is the user-git range `base_rev..output_rev` (D5), not gitevidence.
- **Does not use** `work/` evidence git (`pkg/gitevidence`, marker `.git/omnipus-gitevidence.marker`) as a coding repo.

## 1. Problem

Two **native** writers (General Purpose, future Software Implementer) delegated at once share `workspaces/<id>/work/` (live ADR-032). They overwrite files and can corrupt a **user** git index.

ADR-032 removed live `git worktree` isolation because copies hid real files from external CLIs. `pkg/agent/runner/worktree.go` (`PrepareWorkspace`) still exists: any git, **detached** HEAD, **empty temp dir** on failure. It is **not** called from dispatch. It must not be revived as-is.

“If `.git` then isolate” is wrong: almost every `work/` is git because Omnipus auto-inits **evidence** git for the Judge. That is not a project.

“Always isolate `delegate`” is wrong: Researcher, Planner, mail, sequential GP do not need a second checkout.

v1 of this ADR used walk-up discovery and `isolate: true`. Reviewers rejected it: walk-up from `work/` hits evidence git and **never** sees `work/my-app/`; a boolean can be forgotten and does not name the repo; dirty force-delete destroys work; GitGuard/`os.Root` were unspecified.

## 2. Decisions

### D1 — Default `delegate` unchanged; isolate only via `project_root`

Live rules stand: CoreTeam members run in `workspaces/<id>/work/` (`workspace_reroot.go`). No workspace membership → **refuse**. Isolate does **not** drop that check.

`delegate` gains optional string **`project_root`**.  
- **Omitted** → today’s cwd (`work/`).  
- **Set** → isolate this child (D2–D8). Empty/invalid → **reject**.  
No boolean `isolate`. No per-agent “always isolate writers” in v1.

A model can omit `project_root` and two writers still collide. Accepted. We do not infer isolation from `.git`.

### D2 — `project_root` is an explicit user git top-level

Caller passes the directory they cloned (e.g. `work/my-app` or a mount). Engine **does not walk** from `work/` to find a repo.

Deterministic checks (all must pass):

1. `git rev-parse --show-toplevel` and `--git-common-dir` from `project_root` (handles `.git` **file** / linked worktree).
2. **Canonical identity:** cleaned `project_root` **must equal** `show-toplevel`. A subdirectory is **reject** (no silent walk-up).
3. Authorize **both** the toplevel checkout **and** the resolved common dir (and `worktrees/` dir if distinct). Each must lie inside the child’s workspace or an approved mount. A linked worktree whose objects live outside those bounds → **reject**.
4. Common dir contains `omnipus-gitevidence.marker` → **reject** (ours).
5. Incomplete evidence init: `.git` exists, **no** marker, **and** `rev-parse HEAD` fails → **reject** (ambiguous). Unmarked **and** HEAD exists → treat as **user** git (step 3–4 still apply).
6. `ErrNestedRepo` is **not** the detector.

Fail closed: no evidence worktree, no empty temp dir, no `PrepareWorkspace`.

### D3 — Writer children only; reject, do not skip

If `project_root` is set and the target’s **effective** policy (ceiling × per-agent, strictest-wins, ADR-077) does not allow **any** of `write_file`, `edit_file`, `append_file`, `bash` (including **ask** — ask is still a write path) → **reject** the call (“omit project_root”). Researcher/Planner stay in `work/` by **not passing** `project_root`. Silent skip is forbidden.

### D4 — Native only

External-CLI (`subagent_3p`) stays in real `work/` with the existing same-path **lock**. Native + 3p in parallel can still collide; **not closed** by this ADR. Stated, not hidden.

### D5 — Lifecycle: keep the work

**Start:** user repo at `project_root` must be **clean** (no uncommitted/untracked the child needs). Pin `base_rev` = HEAD. `git worktree add -b isolate/<run-id> <dir> <base-rev>`. Dir under `$OMNIPUS_HOME/runner-runs/run-*` (reaper can list). Record on the child session: `{project_root, branch, base_rev, dir}`.

**Success:** engine or child **commits** on `isolate/<run-id>` (empty commit only if no diff). **Publish** `{project_root, branch, base_rev, output_rev}` on the child session **before** `git worktree remove` (non-force). Parent result includes that tuple. No auto-merge. If commit or publish fails, **do not** remove the worktree.

**Judge / machine-check:** for a task that ran isolated, the oracle is **not** `work/`. Verification **must** inspect `output_rev` of the **user** repo (read-only `git worktree add` or `git show` / tree checkout of that rev, lifetime = adjudication). A test **must** fail if checks still look at original `work/` while isolated output is correct. gitevidence on `work/` is **not** used for these tasks.

**Failure / cancel:** no `--force` on a **dirty** tree. Keep checkout or commit `wip/<run-id>` first. Crash reaper: **must not** force-delete dirty coding worktrees (today’s `Teardown` force-delete is **forbidden** on this path). Log and leave.

**Follow-up:** same `run-id` reattaches the same dir/branch if present.

**Judge:** isolated coding evidence is **user-git** `base_rev..output_rev`. gitevidence on `work/` does **not** see `runner-runs/` files. Do not snapshot isolate trees into the marked repo.

**Setup failure** (no git, add fails, dirty source): **reject** the delegate. No empty dir.

### D6 — Work-dir override seam (do not fight `runTurn`)

Do not set cwd in `spawnSubTurn` then let `resolveTurnWorkDirOrRefuse` snap back to `work/`. Set isolate cwd via a sibling of ADR-053 `WithResumeWorkDirOverride` (`WithIsolateWorkDir`). Order: CoreTeam `work/` resolved → isolate override applied → turn starts. If resume-from-commit override is also set → **reject** (two isolation worlds).

### D7 — GitGuard and gitdir allow-list

Linked worktree gitdir lives under `<user-repo>/.git/worktrees/<name>`, **outside** the worktree dir. For an isolated child:

1. App-level FS root = worktree dir.
2. Git mutating verbs allowed only against the **user** common dir (not `work/.git`, not the evidence marker).
3. GitGuard `enclosingProtectedWorkdir` must **not** treat cwd under `work/my-app` as evidence git once isolate cwd is the worktree (which is **not** under `work/` if dir is `runner-runs/`). Evidence protection stays on the **marked** repo only.

**Accepted hole:** Landlock still grants `$OMNIPUS_HOME`, so **bash can still write `work/`**. v1 isolates **file tools**. Bash clobber of `work/` needs a later sandbox ADR. Do not claim full clobber-proof isolation.

### D8 — Do not call `PrepareWorkspace`

Reuse `RunsRoot` / `run-` / reaper **scan** only. New prepare/teardown. Quarantine `PrepareWorkspace` (detach + empty fallback).

### D9 — No `EnterWorktree` tool in v1

Isolation is engine behaviour on `project_root`, not a second tool.

## 3. Consequences

**Positive:** two native writers can each have a committed branch on the **user** repo; parent gets merge handles. Default `delegate` unchanged. Evidence git is not a worktree source.

**Negative:** `git` on PATH required; disk; leftover `isolate/*` branches until someone merges/deletes; model must pass `project_root`.

**Accepted holes:** omit `project_root` → collision; bash can still write `work/`; native+3p collision; Judge does not see isolate files via gitevidence.

## 4. Alternatives not taken

| Option | Why not |
|---|---|
| Walk-up discovery / `isolate: true` | v1; rejected in review |
| Always isolate | Non-coding work; evidence git |
| Infer “is git” | Evidence layer |
| Restore CLI worktrees here | ADR-032 empty-copy; separate ADR |
| Child-called `enter_worktree` | Model-dependent |
| Per-agent always-isolate | Sequential GP jobs pay isolation by default |

## 5. Out of scope

`lsp`; grep merge; gitevidence auto-init change; calendar; parent tool inheritance; shrinking Landlock to close the bash hole; isolating `subagent_3p`.

## 6. Verification (when implemented)

Must **not** use existing `pkg/agent/runner/worktree.go` tests as coverage.

- `project_root=work/my-app` → cwd is worktree of **unmarked** repo; **zero** `worktrees/` under evidence `work/.git`.
- `project_root=work` (evidence) → error.
- `project_root` omitted → cwd `work/` as today.
- `project_root` + Researcher → **error** (not silent `work/`).
- Dirty user repo at start → error.
- Success → parent result includes `output_rev`; worktree gone; branch remains.
- Dirty failure → worktree **still on disk**; no `--force`.
- Isolate + resume override → error.
- `$OMNIPUS_HOME` inside a user repo → `project_root` that resolves to that repo **outside** workspace/mounts → error.

`CGO_ENABLED=0 go test -tags goolm,stdjson -run 'IsolateProjectRoot|IsolateEvidenceRefuse|IsolateDirty' -p 1 ./pkg/agent/...`

## 7. Review history

**v1 (same day):** Codex **reject**; Claude Code CLI **reject**. Walk-up, boolean flag, force-delete, unspecified GitGuard, false “does not repeal ADR-032.”

**v2 (this file):** adopts their rewrite bar: `project_root`, fail-closed, commit-before-remove, no force-delete dirty, override seam, GitGuard/gitdir, quarantine `PrepareWorkspace`, explicit ADR-032 amendment, bash hole named, Judge evidence = user-git range.
