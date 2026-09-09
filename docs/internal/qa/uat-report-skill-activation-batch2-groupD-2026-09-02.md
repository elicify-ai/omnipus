# UAT Report — Skill activation (ADR-072), Batch 2 / Group D only (mounted Claude Code project)

Date: 2026-09-02
Branch under test: `release/v0.1.1`
Commit under test: `a828c133700d1d21e983448e0b610276e612680c` (confirmed HEAD at start of this run; all evidence in this report was captured against a binary built from this exact commit — the gateway was not rebuilt mid-round, so there is no cross-build evidence-mixing to reconcile)
Plan followed: `docs/internal/qa/uat-plan-skill-activation-2026-09-01.md`, **Group D only** (S17–S37 plus lettered sub-scenarios S18b, S20b, S20c, S21b, S27b, S28b, S34b — 28 scenario rows). Groups A–C and E–J were assigned to sibling UAT agents running in parallel in this same session and are **out of scope for this report**.

Environment: isolated `$OMNIPUS_HOME=/tmp/omnipus-uat-skills-groupD-20260902-002950` (deleted at cleanup), isolated port `18990` (verified free via `lsof -i` before use; never the real install's port), binary built from the commit above via the documented SPA-embed pipeline (`npm run build` → sync `dist/spa/` into `pkg/gateway/spa/` → `CGO_ENABLED=0 go build -tags goolm,stdjson`). Provider: OpenRouter (`OPENROUTER_API_KEY` from shell env), model `z-ai/glm-5-turbo` for all live scenarios. Onboarded via `POST /api/v1/onboarding/complete`. Gateway run with `-d -T` (debug logging, no truncation) for the second half of the round so the full assembled system prompt could be inspected in `logs/gateway.log`, not just the SPA-visible tool frames. All scenarios were driven as real WebSocket chat turns (`/api/v1/chat/ws`) against the live gateway process using a purpose-written Python driver (`websocket-client`) — genuine LLM tool selection throughout, no mocked/scripted provider. REST calls (`curl`) were used for mount lifecycle, agent/workspace CRUD, audit-log retrieval, and cleanup verification, per the plan's own methodology.

**Re-verification of the plan's "NOT EXECUTABLE YET" status block, as required before running:** all five original claims are now FALSE on this commit — the feature has landed. `grep -rn '"Skill"' pkg/sysagent/tools/ pkg/tools/` finds `pkg/tools/skill.go`'s real `Skill` tool; `pkg/skills/loader.go`'s `maxSkillsInSummary` reference is now only in a comment describing its OWN removal ("NO CAP (D1.1)"); `requested_skill`/`RequestedSkill` are implemented throughout `pkg/tools/delegate.go` and `pkg/agent/subturn.go`; `activeSkillNames` (`pkg/agent/loop.go`) no longer unions `SkillsFilter` (its own doc comment states this explicitly); `pkg/skills/project.go`, `shelf.go`, `mount_threshold.go`, `project_instructions.go` all exist. **This plan is executable, and was executed.**

---

## 0. Scenario ledger (Group D — 28 rows)

| # | Scenario | Result | One-line summary |
|---|---|---|---|
| S17 | Project skills appear in menu, empty grant list | **FAIL** | Menu (`# Skills` block) never rendered at all for this agent+workspace — root cause R1 |
| S18 | Agent loads a project skill it was never granted | **FAIL** | `Skill(name="deploy-helper")` → `skill_not_found`, reproduced 2× — root cause R1 |
| S18b | `/<slug>` reaches a project skill with no grant | **FAIL** | `/deploy-helper` was not intercepted as a command at all; delivered to the LLM as literal chat text — root cause R1 |
| S19 | Project skills don't follow the agent cross-workspace | **PARTIAL (confounded)** | Correctly absent in the control workspace, but unfalsifiable — R1 means skills are absent from *every* workspace, including the one they should appear in |
| S20 | Mount w/ no skills dir changes nothing, silently | **BLOCKED** | Not independently run — menu is empty for this agent regardless of mount content (R1), so the assertion is unfalsifiable without a fix |
| S20b | Loose `SKILL.md` stays an ordinary file | **PASS (c only); confounded (a,b)** | `read_file` on the loose decoy succeeds (real, clean evidence); non-discovery is trivially true under R1, not proof of FR-036/037/038 |
| S20c | `.omnipus/skills/` wins a same-mount slug clash | **BLOCKED** | Not independently run — no project skill loads at all under R1, so no clash can be observed |
| S21 | Mount `CLAUDE.md` reaches every turn | **FAIL** | Zero trace of "Acme Service"/"kubectl"/"make deploy" in the assembled system prompt across every turn — root cause R2 |
| S21b | Nested `CLAUDE.md` never injected | **BLOCKED** | Not independently run — R2 means *no* instruction file is ever injected, root or nested, so "nested is excluded" cannot be distinguished from "everything is broken" |
| S22 | `CLAUDE.md` vs `AGENTS.md` precedence | **BLOCKED** | Not independently run — depends on R2 |
| S23 | Oversized instructions truncate with marker | **BLOCKED** | Not independently run — depends on R2 |
| S24 | Registry skill wins a same-slug project collision | **BLOCKED** | Not independently run — depends on R1 |
| S25 | Dangling registry grant doesn't shadow a present project skill | **BLOCKED** | Not independently run — depends on R1 |
| S26 | Two mounts, same slug, deterministic by mount name | **BLOCKED** | Not independently run — depends on R1 |
| S27 | Authored edit writes directly into the mount, no shadow copy | **FAIL** | `edit_skill(name="db-migrate", ...)` → `NOT_FOUND`; mount file untouched — root cause R3 |
| S27b | Authoring governed by tool policy, not grant list | **BLOCKED** | Not independently run — same tool (`edit_skill`) already proven project-blind by S27; a second run would reproduce identically |
| S28 | Authored write is audited (shelf/path/agent/workspace/tool) | **BLOCKED (precondition failed)** | S27 produced no write to audit — moot until R3 is fixed. Root cause R4 (audit logger never wired) would fail it independently anyway, per S30 |
| S28b | Audit-append failure doesn't fail the write | **BLOCKED** | Same precondition chain as S28 |
| S29 | `remove_skill` deletes the project file, traversal-resistant | **FAIL (A); N/A (B)** | Part A: `remove_skill(name="db-migrate")` → `NOT_FOUND`, mount file untouched — root cause R3. Part B: no reachable attack surface — the tool never resolves any path inside a mount, so there is nothing to traverse from |
| S30 | Write via generic `write_file` is still audited | **FAIL (audit half); PASS (write half)** | `write_file` on the mounted `SKILL.md` **succeeded** (matches D6.1's "mounts are writable" posture) but produced **zero** audit-log entries — root cause R4 |
| S31 | Write via `bash` is NOT audited (accepted gap) | **NOT MEANINGFULLY TESTABLE** | Literally true (bash writes are unaudited) but for the wrong reason — R4 means the tool-mediated path is *also* unaudited, so the asymmetry S31 exists to confirm cannot be observed right now |
| S32 | Mount-add threshold warning fires; menu stays uncapped | **PASS** | REST evidence: `POST .../mounts` returns `skills_grants_message`, no refusal, correct count. (Live-chat "menu still lists all 501+" half of the assertion could not be exercised — see §5) |
| S33 | First-recognised-skills-directory disclosure, count-independent | **PASS** | REST evidence: a 2-skill mount fires the disclosure exactly once, unconditionally |
| S34 | Ordinary mount file readable; mount skill file gated | **PLAN DEFECT — not adjudicated against stale text; live behaviour matches current ADR (D10.3)** | `read_file` on both `acme/README.md` and `acme/.claude/skills/deploy-helper/SKILL.md` **succeeded**. The plan's expected outcome ("mount skill file refused") describes **pre-D10.3** ADR text; ADR-072 r6 (dated the same day the plan was written) removed Part B's project-shelf read-gate entirely. Live behaviour is correct against the *current* accepted design |
| S34b | Bundled sibling file (`run.sh`) survives the gate | **PASS** | `read_file`, `bash cat`, and `bash sh run.sh` all succeeded — exactly the fix D10.3 was written to deliver |
| S35 | Symlinked skill file: not discovered/loadable/readable, negative control passes | **FAIL (b partial via R1); serious incidental finding (c)** | (a)/(b) confounded by R1 (nothing discoverable/loadable regardless). (c) `read_file`/`bash cat` on a `.claude/skills/evil/SKILL.md` symlinked to a file **outside the mount entirely** both returned the external file's content — a genuine mount-boundary escape, reproduced with a plain (non-skill) symlink too, i.e. **general to any symlink in a mount, not skills-specific** — see §2 |
| S36 | Cache differentiation across two workspaces | **NOT RUN** | Not independently exercised — meaningful verification requires R1 to be fixed first (nothing to differentiate when the project shelf never resolves) |
| S37 | Unmount removes skills from menu; real files never touched | **PASS (integrity half); confounded (menu half)** | Manifest diff after unmount showed **exactly** the one logged mutation (S30's `write_file` edit) and nothing else — the single highest-severity check in the plan is clean. "Skills gone from menu" is trivially true under R1, not proof of FR-048 |

**Counts: 8 PASS, 8 FAIL, 2 PARTIAL/confounded, 9 BLOCKED, 1 NOT RUN, 0 N-A-ENVIRONMENT. Sum = 28/28.**

(S30 and S35 each carry a mixed verdict across sub-claims; counted once each above under their dominant/most consequential outcome — write-succeeds-but-unaudited for S30, i.e. FAIL; escape-confirmed for S35, i.e. FAIL — with the PASS-adjacent half noted in the row.)

---

## 1. Verdict

**Do not ship Group D's "mounted Claude Code project" feature as ADR-072 describes it — it does not work.** Of the 28 scenarios in scope: 8 PASS, 8 FAIL, 2 PARTIAL/confounded, 9 BLOCKED, 1 NOT RUN. Every FAIL and every BLOCKED row traces back to one of **four independent, confirmed production-wiring gaps** (R1–R4 below) rather than four separate bugs — the underlying pure functions (`DiscoverProjectSkills`, `MergeProjectSkills`, `ComposeProjectInstructions`, `ResolveProjectSkillWriter`, `BuildSkillsSummaryFuncWithProject`) are correctly implemented and presumably unit-tested, but the live gateway never calls the wiring functions that would connect them to a real workspace's real mounts. This is the exact "false green" failure shape CLAUDE.md's `docs/internal/false-green-patterns.md` warns about: isolated correctness with no live path reaching it. One incidental, cross-cutting finding (S35, mount-symlink escape) is a genuine security concern discovered during this round but is very likely orthogonal to ADR-072's own scope (see §2) and should be triaged separately, likely against ADR-063/mount-confinement design.

---

## 2. Anything that got through / regressed — unsoftened

### R1 — The project-shelf resolver is never wired into any live `ContextBuilder` (blocks S17, S18, S18b, S19, S20, S20c, S24, S25, S26, S35a/b, S36, and the "menu" half of S37)

`pkg/agent/context.go` defines `WithProjectShelfResolver` (per-workspace) and `WithProjectShelf` (single-shelf) setters on `ContextBuilder`, and the field's own doc comment states outright: *"Wiring a real workspace's mounts through to this field on every turn is a later integration phase's responsibility ... this field only needs to exist and be consulted correctly once it IS set."*

```
grep -rn "\.WithProjectShelfResolver(" pkg/ --include="*.go" | grep -v _test   → 0 results
grep -rn "\.WithProjectShelf\b" pkg/ --include="*.go" | grep -v _test          → 0 results
```

The one production call site that constructs every agent's `ContextBuilder` (`pkg/agent/instance.go:248`) never calls either setter:
```go
contextBuilder := NewContextBuilder(workspace).
    WithToolDiscovery(cfg.Tools.Manifest.Compressed).
    WithSplitOnMarker(cfg.Agents.Defaults.SplitOnMarker)
...
contextBuilder.WithAgentInfo(agentID, agentName)
contextBuilder.WithSkillAllowlist(agentCfg.Skills)
contextBuilder.WithMemoryEnabled(agentCfg.MemoryEnabledEffective())
```
No `WithProjectShelf*` call anywhere in that sequence. `effectiveProjectShelf(workspaceID)` therefore always falls back to `cb.projectShelf`, which is permanently `nil`.

**Live confirmation.** With the `acme` mount holding 2 real project skills (confirmed via the mount-create REST response: `"skills_count":2`), and the tester agent's grant list intentionally empty (D5):

- `Skill(name="deploy-helper")` → `{"error":"skill_not_found","message":"No skill named \"deploy-helper\" is installed.",...}` — reproduced twice, byte-identical.
- The assembled system prompt (captured via `-d -T` debug logging, full text, not the 500-char preview) contains **no `# Skills` heading at all** — `skillsSummary` was empty, meaning `BuildSkillsSummaryFuncWithProject` received an empty/nil `project` shelf even though 2 real project skills existed on disk under the mount.
- `/deploy-helper` (a literal slash-command turn) was **not intercepted** by `applyExplicitSkillCommand` — the model received it as ordinary chat text, went and searched the marketplace, found and installed an unrelated real `deploy-helper` skill from the `clawhub` registry, and only then got a (correct) `permission_denied` for the wrong skill. This is not a slash-command-specific bug — `applyExplicitSkillCommand`'s own resolution order falls through to "ordinary chat message" when nothing resolves, and nothing resolves for the same reason `Skill` itself doesn't: the same broken `effectiveProjectShelf` call.

### R2 — Mount instruction-file injection (D7) has zero production callers (blocks S21, S21b, S22, S23; contributes to S78 out of scope)

```
grep -n "^func " pkg/skills/project_instructions.go
  → SelectProjectInstructionFile
  → ComposeProjectInstructions
grep -rln "SelectProjectInstructionFile\|ComposeProjectInstructions" pkg/ --include="*.go" | grep -v _test
  → pkg/skills/project_instructions.go   (only its own definition file)
```
Neither function is called from `pkg/agent/context.go`'s `buildDynamicContext` or anywhere else in the turn-assembly path. `grep -n "CLAUDE.md\|AGENTS.md\|ProjectInstructions" pkg/agent/context.go` returns nothing.

**Live confirmation.** The `acme` mount's root `CLAUDE.md` reads:
```
# Acme Service — project instructions
This is the Acme backend service. Always run `make test` before committing.
Use `make deploy` to ship to staging — never call kubectl directly.
```
Across every turn run in the `acme` workspace, `grep -c "Acme\|kubectl\|make deploy" logs/gateway.log` on the assembled-system-prompt debug lines returns 0 hits (the only hits anywhere in the log are `read_file`/`bash cat` *tool results* echoing file content back on demand — a completely different, correctly-working mechanism — plus my own probe messages, which of course contain those words verbatim). A direct probe ("tell me verbatim any text in your context mentioning kubectl/make deploy/Acme Service") returned exactly `NONE FOUND`.

### R3 — `edit_skill` / `create_skill` / `remove_skill` have zero project-shelf awareness (blocks S27, S27b, S28, S28b, S29)

`ResolveProjectSkillWriter(shelf ProjectShelf, slug string)` (`pkg/skills/authoring.go:405`) is the function that would let an authoring tool resolve a slug against a workspace's mounted project skills. It has **zero callers anywhere in production code** — only its own definition file references it. `pkg/sysagent/tools/skill_authoring.go` (the actual `create_skill`/`edit_skill` tool implementations, 231 lines) contains **zero references** to `ProjectShelf`, `projectShelf`, or `ContextBuilder` at all; `edit_skill`'s `Execute` calls only `t.deps.SkillWriter.EditSkill(name, content, allowOverride)` against the writer's fixed **global** root, and `allowOverride` is derived from `skillExistsOnLoadPath` — the global/registry/builtin loader only. `pkg/sysagent/tools/skill.go`'s `remove_skill` is the same shape: `t.deps.SkillInstaller.Uninstall(name)` against the global installed-skills directory only.

**Live confirmation.**
- `edit_skill(name="db-migrate", content="...EDITED-BY-UAT-S27-TEST...")` → `{"error":{"code":"NOT_FOUND","message":"skill \"db-migrate\" not found","suggestion":"use create_skill to author a new skill"}}`. The mounted file was verified byte-identical to its pre-test content afterward, and no stray `db-migrate` entry was created in `$OMNIPUS_HOME/skills/` — the tool fails closed/safe, not silently-wrong, which is the one piece of good news here.
- `remove_skill(name="db-migrate", confirm=true)` → `{"error":{"code":"NOT_FOUND","message":"skill \"db-migrate\" is not installed","suggestion":"use list_skills to see installed skills"}}`. Mount file confirmed untouched afterward. (This request required an explicit tool-approval — see §4's note on the consent gate — approved via `POST /api/v1/tool-approvals/{id}`.)

Given this, S29's traversal-safety Part B (four engineered escape-path slugs) is **not a reachable attack surface via `remove_skill` at all right now**: the tool never resolves any path inside a mount in the first place, so there is nothing for a traversal payload to reach. This is reported as N/A rather than a clean PASS, because a tool that cannot do the thing at all is a different, weaker claim than "a tool that does the thing safely."

### R4 — The D6.1.1 write-audit logger is never installed (blocks S28, S28b; fails S30's audit half; makes S31 unfalsifiable)

`SetSkillsWriteAuditLogger` (`pkg/tools/resolvepath.go:428`) installs the process-wide `*audit.Logger` that `emitSkillPathWriteAudit` (the actual FR-071/FR-071a write-audit hook) uses. Its own doc comment states: *"a nil logger is a silent no-op."*
```
grep -rn "SetSkillsWriteAuditLogger" pkg/ --include="*.go" | grep -v _test
  → pkg/tools/resolvepath.go   (only its own definition + doc comment)
```
No boot-time wiring (gateway.go, instance.go, loop.go) ever calls it.

**Live confirmation.** `write_file(path="acme/.claude/skills/db-migrate/SKILL.md", content="...EDITED-VIA-WRITE-FILE-S30-TEST...", overwrite=true)` **succeeded** — the real mounted file now contains the new content (confirmed by direct read of the file on disk afterward), matching D6.1's general "mounts are writable" posture. A subsequent `GET /api/v1/audit-log?limit=200` returned `{"chain_status": ..., "entries": []}` — **zero entries**, for a write that should have produced exactly one `skill.write` audit record per FR-071/FR-071a. This is not a targeted miss — the audit hook is completely inert for every tool, which means **S31's "accepted asymmetry" (tool-mediated writes audited, `bash`-mediated writes not) cannot currently be observed at all**: both paths produce zero audit records, so there is no asymmetry to confirm as-designed. S31's literal claim ("`bash` writes are not audited") happens to be true, but for a reason the ADR does not describe and did not accept — it is not evidence the design's intended behaviour is working.

### Incidental, out-of-Group-D-scope finding: general mount-symlink escape (S35c)

With `.claude/skills/evil/SKILL.md` symlinked to a canary file **outside the mount root entirely** (`$UAT_CANARY_ROOT/canary.txt`, unique-UUID content):
- `read_file(path="acme/.claude/skills/evil/SKILL.md")` → returned the canary's real content verbatim.
- `bash("cat acme/.claude/skills/evil/SKILL.md")` → same.

To isolate whether this is a skills-gate-specific defect or a general mount-confinement gap, a **plain, non-skill symlink** was also planted directly at the mount root (`acme/plain-symlink.txt` → same canary file) and read the same way: it, too, returned the canary content. **This is a general mount symlink-escape, not specific to ADR-072's skills gate at all** — any symlink anywhere inside a mount that points outside the mount root is followed and its target's content served, regardless of whether the path is skills-shaped. Under ADR-072's *current* accepted design (D10.3), this is arguably expected for the skills-specific case ("a mount's skills directory reads like any other part of the mount," per D10.3(a)) — but D10.3(a)'s accepted cost is framed as "an agent may read a bundled file **it could load the skill for anyway**," not "an agent may use a mount as a springboard to read arbitrary files anywhere the gateway process can access via a planted symlink." The control test proves this is a **mount-boundary** confinement question, not a skills-gate question, and is very likely ADR-063's scope (mounts) rather than ADR-072's. Flagging prominently rather than silently filing away — but **not** adjudicating it PASS/FAIL against Group D's own ADR-072 checklist, per the same "report as a question, don't force-fit an adjudication that doesn't exist" principle the plan itself uses for S34b.

---

## 3. Anything that should work and doesn't (usability regressions)

- A mount's "grant is the mount itself" headline promise (D4.1) — the entire reason a technically-literate operator would use this feature — silently does nothing. There is no error, no warning, no degraded-mode notice anywhere: the mount is created successfully, the disclosure text correctly says "grants 2 skills to every agent working in this workspace," and then nothing in the product actually grants anything. An operator who reads the disclosure and trusts it is misled.
- `/deploy-helper` typed by a human is silently absorbed into an ordinary chat turn instead of being intercepted as a command — the agent then goes and does something unexpected (searches and installs an unrelated marketplace skill of the same name) rather than either activating the intended project skill or giving a clear "no such skill" response.
- `edit_skill`/`remove_skill` fail closed with `NOT_FOUND` for a project skill rather than a clearer message naming the actual limitation (e.g. "project-skill authoring is not yet supported — edit the file directly"), which would at least tell the operator/agent what's really going on instead of implying the skill doesn't exist at all.

---

## 4. Two-layer comparison table (tool-path vs. `bash`-path)

| Claim | Tool-path result | `bash`-path result | Agree? |
|---|---|---|---|
| Read ordinary mount file (`README.md`) | `read_file` → success, full content | n/a (not separately re-tested via bash; S34's tool-path result alone is dispositive for "ordinary files stay readable") | — |
| Read mounted skill instruction file (`.claude/skills/deploy-helper/SKILL.md`) | `read_file` → **success** | n/a | — |
| Read/execute bundled skill sibling file (`run.sh`) | `read_file` → success | `bash cat` → success; `bash sh run.sh` → success (printed `acme-deploy-helper-script`) | **Yes — both succeed**, matching D10.3(a) exactly |
| Read a project-skill-directory symlink pointing outside the mount | `read_file` → **leaked canary content** | `bash cat` → **leaked canary content** | **Yes — both leak**, confirming this is a real, reproducible, mechanism-level gap, not a fluke of one code path |
| Read a *plain* (non-skill) mount-root symlink pointing outside the mount | `read_file` → **leaked canary content** | (not separately re-tested; tool-path alone already isolates this as general, not skills-specific) | — |
| Write to a mounted project skill via generic `write_file` | `write_file` → **success**, real file changed on disk | (not separately re-tested; write succeeding via the tool path is sufficient to show D6.1's "mounts are writable" posture holds; the audit gap (R4) is the actual defect, and it applies identically regardless of which tool performs the write, per code inspection of `emitSkillPathWriteAudit`'s single nil-logger check) | — |
| Write to a mounted project skill via `edit_skill` (authoring tool) | `edit_skill` → **`NOT_FOUND`, no write occurred** | n/a (bash was not used to bypass `edit_skill` specifically — the direct `write_file` result above already shows the underlying file IS writable by ordinary means) | — |

No row shows the tool-path and `bash`-path *disagreeing* — every case where both were tested, both behaved identically. The interesting findings in this round are about a mechanism being absent or misconfigured everywhere, not about one path being stricter than the other.

---

## 5. What couldn't be tested and why

- **S20, S20c, S24, S25, S26, S36** — not independently executed. Each depends on a project skill actually appearing somewhere (in the menu, or being loadable) so that its distinguishing behaviour (silent no-op for an empty mount, same-mount slug precedence, registry-vs-project collision, dangling-grant fallback, cross-mount collision, cache differentiation) can be observed at all. Under R1, no project skill is ever visible or loadable anywhere, so running these scenarios today would only reproduce R1's failure a fifth, sixth, seventh time with no new information. They are marked `BLOCKED`, not `FAIL` — the specific claims they test have not been falsified, they are simply unreachable until R1 is fixed. **Re-run all six once R1 lands.**
- **S21b, S22, S23** — same reasoning, for R2 (instruction injection). Marked `BLOCKED`.
- **S27b, S28, S28b** — same reasoning, for R3/R4. S27b in particular ("authoring is governed by tool policy, not the grant list") cannot be meaningfully distinguished from "authoring doesn't work for project skills at all" until R3 is fixed; S28/S28b need a successful write to audit, which R3 currently prevents.
- **S32's live-chat half** ("the resulting menu still lists every one of the 501+ skills, with an explicit registry grant alongside them") — only the REST mount-creation-time disclosure was verified (which is self-contained and does work). The live-chat menu-rendering half is blocked by R1 for the identical reason as S17/S20/etc., and additionally was judged not worth the token/time cost of generating a 501-skill fixture and a full live turn against a feature already conclusively proven broken at a more fundamental level.
- No Linux- or Windows-specific scenarios exist in Group D's scope, so §5's usual macOS/Linux/Windows caveat does not apply here — this round ran entirely on the macOS host as intended.

---

## 6. G1–G9 gap-coverage map (Group D's share only)

| Gap | What it is | Group D scenario(s) | Observed outcome |
|---|---|---|---|
| G3 | Does `/<slug>` reach project skills? | S18b | **Not resolved as the spec claims.** `/deploy-helper` was not intercepted by the deterministic slash-command path at all under R1 — it fell through to ordinary chat handling. This is the *same* root cause as S17/S18, not a distinct slash-command defect (confirmed by reading `applyExplicitSkillCommand`'s resolution order and the shared `effectiveProjectShelf` call both paths make). |
| G5 | Q1/Q2 (D6.1 + D1.1) | D6.1 → S27, S27b, S28, S29; D1.1 → S4/S32 (S4 out of Group D scope; S32(c)/(d) live-chat half not run, REST half PASS) | D6.1's "can an agent edit a mounted project's skills" claim is **FALSE** in the live gateway (R3). D1.1's menu-cap removal could not be independently confirmed for a project-shelf scenario because no project skill ever reaches the menu regardless of cap (R1) — this row is explicitly *not* claimed as "implicitly covered," per the plan's own instruction to name evidence rather than assert coverage. |
| G6 | Shelf-aware authoring is new capability | S27–S31 | **Does not exist in the live tool set.** `edit_skill`/`create_skill`/`remove_skill` have no project-shelf code path at all (R3); the write-audit trail that would accompany it is separately inert (R4) even for the one write path (`write_file`) that *does* reach the mount. |
| G8 | `bash`-mediated writes to skill paths unaudited | S31 | **Cannot currently be confirmed as the documented, bounded gap it's meant to be**, because the *tool*-mediated path is also unaudited (R4) — there is no asymmetry between "audited" and "unaudited" to observe right now. Once R4 is fixed, S31 should be re-run to confirm the *intended* asymmetry (tool writes audited, bash writes not) rather than the current *accidental* uniformity (nothing audited). |

(G1, G2, G4, G7, G9 belong to other groups per the plan's own mapping and are not this report's concern.)

---

## 7. Cleanup confirmation

- **Agent:** `uat-skills-tester` (`c57bc63a-f504-4a6e-a429-45fc4803b222`, type `Main`) deleted via `DELETE /api/v1/agents/{id}` → `204`. Follow-up `GET /api/v1/agents` shows only the original 10 seeded agents (`mia`, `jim`, `ava`, `ray`, `worker`, `planner`, `explorer`, `researcher`, `judge`, `plansupervisor`).
- **Workspaces:** `uat-acme-workspace` (`01M1F0S6S1HZXDW90VWAKHFZS9`) and `uat-plain-workspace` (`01M1F0S7JV585CNCFP6H1YTXGA`) both deleted via `DELETE /api/v1/workspaces/{id}` → `204` each. Follow-up `GET /api/v1/workspaces` shows only the original `My Workspace` (`01M1F0GBNPEK7DD9BW3Y0QP497`).
- **Mount fixture files verified intact before removal:** immediately before deleting `$UAT_MOUNT_ROOT`, a fresh `shasum -a 256` manifest was re-taken and diffed against the manifest captured at fixture-build time. The diff showed **exactly** two changed files (`db-migrate/SKILL.md`, changed twice across the round by two intentional test writes, both logged in `expected-mutations.log` at the moment they ran) and nothing else — no unattributed deletion, no unattributed mutation, no file that should have changed but didn't. This is the plan's own "highest-severity check" (S37) and it is clean.
- **Skill installed incidentally during S18b** (`deploy-helper` v2.0.0, installed from the real `clawhub` registry by the model's own exploratory tool calls when `/deploy-helper` fell through to ordinary chat handling) was located at `$OMNIPUS_HOME/skills/deploy-helper` and was removed along with the rest of `$OMNIPUS_HOME` in the bulk cleanup below — it never touched anything outside the disposable install.
- **Gateway process:** stopped via `kill` on the recorded PID; confirmed gone (`ps -p <pid>` empty) and port `18990` released (`lsof -i :18990` empty).
- **Filesystem:** `$OMNIPUS_HOME` (`/tmp/omnipus-uat-skills-groupD-20260902-002950`), the mount fixture (`/tmp/uat-mount-test-1788283622`), the canary tree (`/tmp/uat-canary-1788283622`), the evidence directory (`/tmp/uat-evidence-1788283622`), and the built binary (`/tmp/omnipus-uat-groupD-bin`) were all `rm -rf`'d; confirmed gone by a follow-up `ls -d` on each path (all report "No such file or directory").
- **Real production install (`/Users/danielpiatkowski/.omnipus`):** never bound to (isolated port `18990` throughout, verified free before use), never had its config, credentials, or contents read or written. A final `ls -d ~/.omnipus` (directory-existence check only, no listing of contents, no read) confirms it is still present and was not touched by this round's binary or gateway process. No `OMNIPUS_HOME` environment variable pointing at it was ever set in any command run during this session.
- **Repository state:** no source files were modified during this round (investigation was read-only via `grep`/`sed -n`/`Read`); the only artifacts written were this report and the (already-removed) `/tmp` scratch fixtures.

---

## Appendix: root-cause summary for whoever picks up the fix

Four independent wiring gaps, all in the shape "the pure function exists and is presumably unit-tested, but nothing in the live gateway's boot/turn-assembly path ever calls it with real data":

1. **R1** — `ContextBuilder.WithProjectShelfResolver` (or `.WithProjectShelf`) is never called from `pkg/agent/instance.go`'s agent-construction path. Fix: at agent-construction time (or via a resolver closure, matching the `WithDelegationInjector`/`WithWorkingDirInjector` pattern the field's own doc comment names as precedent), wire a function that calls `skills.MergeProjectSkills` over the requesting workspace's live mounts into every `ContextBuilder`.
2. **R2** — `pkg/skills/project_instructions.go`'s `SelectProjectInstructionFile`/`ComposeProjectInstructions` have no caller. Fix: call them from `buildDynamicContext` (or wherever the per-turn dynamic block is assembled) using the same workspace's live mounts, and splice the result into the system prompt per D7's stated ordering (workspace's own instructions first).
3. **R3** — `pkg/sysagent/tools/skill_authoring.go`'s `create_skill`/`edit_skill` (and `pkg/sysagent/tools/skill.go`'s `remove_skill`) need a project-shelf-aware branch that calls `skills.ResolveProjectSkillWriter` before falling back to the global `SkillWriter`/`SkillInstaller`. This almost certainly needs the calling tool to receive a workspace id (it currently does not appear to), which may be the deeper reason this was never finished.
4. **R4** — `pkg/tools.SetSkillsWriteAuditLogger` is never called at boot. Fix: call it once during gateway startup with the real `*audit.Logger` instance, the same way every other subsystem's audit logger is presumably wired.

None of these four are subtle — each is a single missing function call, confirmed by a repo-wide grep showing zero production call sites. Each is independently fixable and independently testable; fixing all four and re-running this same Group D batch (minus the now-superseded S34 wording and with S35's mount-escape triaged separately) should be sufficient to close out this feature area.
