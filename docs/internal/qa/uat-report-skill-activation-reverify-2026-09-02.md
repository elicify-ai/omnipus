# UAT re-verification — Skill activation (ADR-072), two findings from Batch 3

Date executed: 2026-09-02. Branch: `release/v0.1.1`. Build commit: `88c91a931bbd9cae358150761c5aa723a2f9f922` ("fix(agent,skills): wire ADR-072 mounted-project skills end-to-end (R1-R4)"), one commit after `898d3aca` ("fix(agent): remove pre-ADR-072 read_file back door and stale cap claim from the skills prompt"). Both fixes are present in the binary under test.

Scope: **not** a full UAT round. Targeted re-verification of two findings from `docs/internal/qa/uat-report-skill-activation-batch3-groupsEtoJ-2026-09-02.md`, which tested an older commit (`a828c133`) that predates both fixes above:
1. The CRITICAL cross-cutting defect ("mounted project skills are non-functional").
2. S72 (live model fails to reach for `Skill` when two granted skills both plausibly match).

## Environment

- Isolated `$OMNIPUS_HOME=/tmp/omnipus-reverify-20260902-022047` (deleted at end of run), isolated port `18902` (confirmed free before use).
- Binary built fresh from this exact commit via the documented SPA-embed pipeline (`npm run build` → sync `dist/spa/` into `pkg/gateway/spa/` → `CGO_ENABLED=0 go build -tags goolm,stdjson`).
- Provider: OpenRouter, model `z-ai/glm-5-turbo` throughout — same model the original batch used, for a fair comparison.
- Onboarded via `POST /api/v1/onboarding/complete` (admin `reverifyadmin`), REST-authenticated with the returned bearer token.
- All scenarios driven as real WebSocket chat turns against `/api/v1/chat/ws` via a purpose-built Python driver (`websocket-client`), including `tool_approval_required` handling for `ask`-policy tools (approved via `POST /api/v1/tool-approvals/{id}`) — genuine LLM tool selection throughout, no mocked/scripted provider.
- Fixture: a real mounted project (`acme-service`) with a real `.claude/skills/deploy-helper/SKILL.md`, mounted into a disposable workspace, exactly matching the batch plan's fixture shape. A second, isolated fixture (two registry skills with identical trigger-worded descriptions) was built for S72.
- Real production install (`omnipus-f`, port 5000, `~/.omnipus`) confirmed untouched throughout and after — see Cleanup.

## Question 1 — is the mounted-project-skills defect actually fixed?

**Verdict: PARTIALLY FIXED — a real, narrower regression remains and needs a further fix.**

### What is fixed and durable

- **`edit_skill` locates and edits a mounted project skill correctly, and this survives a hot config reload.** Live call: `edit_skill(name="deploy-helper", content="...")` against the real mount → `{"action":"edited","mount":"acme-service","name":"deploy-helper","path":"/private/tmp/.../acme-service/.claude/skills/deploy-helper/SKILL.md","shelf":"project","success":true}`. Confirmed the write actually landed on disk (re-read the file). This is the R3 fix (`pkg/sysagent/tools/skill_authoring.go`'s `resolveProjectShelf`), and by design it does **not** depend on the same wiring the `Skill` tool's load path uses — it independently calls `workspace.LoadMounts` + skill discovery fresh on every call, with no dependency on `ContextBuilder`/`wireProjectShelfResolvers`. This was tested and confirmed working **both before and after** a hot config reload (see below) — durable.
- **The original batch's `read_file`-succeeds-on-mount-skill sub-finding is confirmed NOT a defect.** Read `docs/internal/architecture/ADR-072-skill-activation-and-loading.md`'s D10.3 section: r6 explicitly states Part B's project-skill read gate "is removed (D4.1 already lets any agent in the workspace load any of that mount's skills, so it protected nothing)." Live-confirmed: `read_file` on the mounted `deploy-helper/SKILL.md` succeeds and returns full content verbatim. This matches the ADR exactly. The original batch's framing of this as part of "the defect" was a plan/understanding-staleness issue (the batch tested against the pre-r6 mental model), not a code defect — no fix needed here, and none was attempted.

### What is NOT fixed — a new, narrower regression

The `Skill` tool's **load path** for mounted project skills works correctly only until the **first hot config reload** of the gateway process, after which it silently breaks again — for every agent, workspace-wide — and stays broken for the rest of that process's life.

**Live reproduction, in order, same process:**

1. Fresh gateway boot (config/workspace/mount already persisted on disk from a prior boot in the same `$OMNIPUS_HOME`, so onboarding did not run in this process's lifetime) → `Skill(name="deploy-helper")` for agent `jim` → **succeeds**, returns the skill body.
2. Deliberately triggered a real hot config reload (external edit to `config.json` — a field the app itself never registered as a self-write — picked up by the gateway's 2-second config-file poller within ~4s; confirmed via `handleConfigReload`'s service-restart log lines).
3. Same call, same agent, same mount, new session → `Skill(name="deploy-helper")` → **`{"error":"skill_not_found","message":"No skill named \"deploy-helper\" is installed.",...}`**. Gateway log: `"skill resolution denied or not found"` at `pkg/agent/context.go:1673`.
4. In the **very first live run of this re-verification** (full realistic flow: onboard → create workspace → mount project → chat), the identical failure reproduced *without* any deliberate reload trick — onboarding's own provider-registration flow was enough to trigger it. `Skill(name="deploy-helper")` failed for both `ava` and `jim` on the very first real attempt.
5. Isolated the underlying data layer directly (a small throwaway Go program calling the exact same `workspace.LoadMounts` + `skills.MergeProjectSkills` functions the resolver uses): the mount **is** discovered correctly, `deploy-helper` **is** in the returned shelf. The bug is not in the discovery/merge logic — it is in the wiring that connects that logic to the `Skill` tool at runtime.

**Root cause, confirmed by reading source (not hypothesis):**

`wireProjectShelfResolvers` (`pkg/agent/loop_env.go:347`, the R1 fix itself) installs the per-workspace project-shelf resolver on each agent's `ContextBuilder`. It is called from exactly one place: `wireEnvProviders`, which is itself called **only once**, from `NewAgentLoop` at process boot (`pkg/agent/loop.go`).

Two other code paths rebuild agents' `ContextBuilder`s after boot, and **neither** re-installs the resolver:

- **`ReloadProviderAndConfig`** (`pkg/agent/loop.go:4839`, the full hot-reload path triggered by the config-file watcher) — its wiring block (lines ~4943–4950) explicitly re-wires `wireDelegationInjectors` and `wireWorkingDirInjectors` on the freshly-built registry ("so the updated ... graph ... is reflected on every agent's next turn without a static-prompt cache bust"), but never calls `wireProjectShelfResolvers`.
- **`UpsertAgentFast`** (`pkg/agent/registry.go:674`, the fast single-agent create/update path) — same pattern: re-wires `wireDelegationInjectors`/`wireWorkingDirInjectors` (lines ~763–764) on the one freshly-built agent instance, never `wireProjectShelfResolvers`.

Both paths build brand-new `ContextBuilder`s whose `projectShelfResolver` field is left `nil`, so `effectiveProjectShelf` (`pkg/agent/context.go:284`) silently falls back to the single, non-per-workspace `cb.projectShelf` field — which nothing in this codebase ever populates (`WithProjectShelf` has no production caller; only `WithProjectShelfResolver` is called, and only at boot) — so it is always empty. Every mounted project skill becomes permanently unreachable via `Skill(name=...)` for any agent whose `ContextBuilder` was rebuilt by either path, until the whole gateway process restarts.

This explains why the R1 fix's own integration tests pass cleanly: they construct one fresh `AgentLoop` and test immediately, never triggering a reload — exactly the one code path (`wireEnvProviders`) that *is* correct. A live, longer-running gateway is a different story: **onboarding itself is enough to trigger the loss**, and any agent create/update touches it too. This is a live regression a real installation will hit routinely, not an edge case.

**Confirmed the break is scoped correctly (not a red herring):** `edit_skill` — tested in the identical post-reload broken state — still worked, because it doesn't route through `ContextBuilder` at all (see above). Only the `Skill` tool's load/search path is affected.

### Recommendation

This needs a further, small fix: add a `wireProjectShelfResolvers(al, registry)` call alongside the existing `wireDelegationInjectors`/`wireWorkingDirInjectors` calls in both `ReloadProviderAndConfig` (`pkg/agent/loop.go`, ~line 4950) and `UpsertAgentFast` (`pkg/agent/registry.go`, ~line 764). Both call sites already have the exact pattern to copy from their neighboring injectors. A regression test that boots an `AgentLoop`, mounts a project skill, forces one `ReloadProviderAndConfig` (or `UpsertAgentFast`), and then re-asserts `Skill(name=...)` still resolves would have caught this — the existing `project_shelf_wiring_test.go` apparently never exercises a second wiring pass.

## Question 2 — does the model still fail to reach for `Skill` when two skills both match?

**Verdict: FIXED (strong evidence, though the earlier failure's specific cause can't be retroactively proven).**

Same scenario shape as S72: two granted registry skills (`deploy-helper-reverify`, `deploy-checklist-reverify`), both with the identical trigger-worded description "Use when the user asks about deployment," materially different bodies (one is a deploy command, the other a pre-deploy checklist). Both genuinely granted to a fresh disposable agent. **N=5** independent fresh conversations, each sent "I need help with a deployment."

| Trial | Outcome | Evidence |
|---|---|---|
| 1 | picked-both-in-sequence | `Skill(deploy-helper-reverify)` → its own marker; `Skill(deploy-checklist-reverify)` → its own marker |
| 2 | picked-A (`deploy-helper-reverify`) | one `Skill` call, correct marker returned |
| 3 | picked-both-in-sequence | both `Skill` calls, correct markers, order checklist→helper |
| 4 | picked-both-in-sequence | both `Skill` calls, correct markers, order helper→checklist |
| 5 | picked-both-in-sequence | one empty-arg `Skill` call (self-corrected, no error propagated to the user), then both `Skill` calls, correct markers |

**0 of 5 trials were picked-neither** — the FAIL CONDITION (`picked-neither` in ≥3 of 5) does not fire; this is the opposite of the original batch's 5/5 picked-neither. In every one of the 5 trials the model's very first tool call was `ToolSearch(names:["Skill"])` immediately followed by one or more genuine `Skill(name=...)` calls — the `read_file`-before-`Skill` pattern the original batch flagged as a likely cause (traced to the stale prompt text `898d3aca` removed) did **not** appear in any of the 5 trials. Every loaded body matched the slug the model named — no cross-contamination between the two skills' content, and no silent auto-loading (both loads were always preceded by an explicit `Skill` tool call visible in the transcript).

One trial (a separate, immediately-preceding attempt) returned a provider "I'm at capacity right now" message with zero tool calls — this was excluded and re-run rather than counted, consistent with the plan's own rule that a non-committal/non-diagnostic outcome isn't a real trial.

This is consistent with — though does not conclusively prove — the batch's own hypothesis that the stale pre-ADR-072 prompt text (fixed in `898d3aca`) was the dominant cause of the `read_file`-first reflex and the resulting 0/5 hit rate: the fix removed the exact sentence that told models to bypass `Skill`, and post-fix, the reflex is gone and the tool is reached for reliably. Since the original failing prompt state no longer exists to re-test side-by-side, this can't be proven as *the* cause with certainty, but the result answers the actual open question this re-verification was asked to answer: the current live hit rate is not broken, and shows no sign of a deeper problem.

## Cleanup confirmation

- Mount removed, both disposable workspaces (`reverify-acme-workspace`, `reverify-s72-workspace`) deleted, both disposable agents (`Reverify Custom`, `Reverify S72 Tester`) deleted, both disposable registry skills (`deploy-helper-reverify`, `deploy-checklist-reverify`) deleted. Follow-up `GET /api/v1/agents` shows exactly the 10 default core/system agents; `GET /api/v1/workspaces` shows exactly the one auto-created default workspace (`My Workspace`); `GET /api/v1/skills` shows no `reverify` entries remaining.
- Mount fixture's only change from baseline is the single intentional `edit_skill` mutation made during testing (confirmed by re-reading the file) — no unattributed change.
- Both gateway processes stopped; `$OMNIPUS_HOME`, the mount fixture directory, both built binaries, and all driver/scratch files deleted.
- Real production install confirmed untouched throughout and after: `lsof -i :5000` shows the pre-existing `omnipus-f` process still listening on its own port; `~/.omnipus` was never touched. The repo working tree is clean (`git status --short` shows only pre-existing untracked sibling UAT reports from other agents in this session, not created by this run).
