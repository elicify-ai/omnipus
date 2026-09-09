# UAT report — Skill activation and loading (ADR-072), Batch 3: Groups E–J

Date executed: 2026-09-01/02. Branch: `release/v0.1.1`. Build commit: `a828c133700d1d21e983448e0b610276e612680c` (confirmed via `git rev-parse HEAD` immediately before the build; all evidence in this report shares this one sha — no rebuild occurred mid-round for this batch).

Plan: `docs/internal/qa/uat-plan-skill-activation-2026-09-01.md`. This report covers **Groups E through J only** (S38–S80, plus lettered sub-scenarios S57b and S67b) — the largest of several parallel batches; Groups A–D (S1–S37 plus S6b/S18b/S20b/S20c/S21b/S27b/S28b/S34b) are covered by sibling batch reports run in parallel by other agents in this session. Where this batch's own testing produced evidence directly relevant to a Group D scenario (S34, cross-referenced by this batch's own S47), it is reported here as a flagged cross-check, not folded into this batch's own scenario count.

**Pre-flight re-verification of the plan's own "not implemented yet" status block** (required before running): re-ran the five checks the plan names, against `a828c133`:
1. `Skill` tool exists — confirmed present (`pkg/tools/skill.go`).
2. `maxSkillsInSummary` cap — confirmed removed (comment at `pkg/skills/loader.go` states "NO CAP (D1.1)").
3. `skillAllowed` nil-allowlist semantics — confirmed flipped to deny (`pkg/agent/skill_allowed_semantics_test.go::TestSkillAllowed_NilAllowlistDeniesEverything`).
4. `requested_skill` on `delegate` — confirmed present (`pkg/tools/delegate.go`, `RequestedSkill` field, `ErrRequestedSkillDenied`/`ErrRequestedSkillNotFound`).
5. `activeSkillNames` unioning `SkillsFilter` — confirmed removed per the code's own comment ("Before ADR-072, this unioned agent.SkillsFilter...").
All five confirm the feature has landed. This plan is executable against this build.

## Environment

- Isolated `$OMNIPUS_HOME=/tmp/omnipus-uat-skills-20260902-003001` (deleted at end of run).
- Isolated port `18901`, confirmed free via `lsof -i` before use (port 5000 was occupied by the real production install `omnipus-f`, confirmed untouched throughout — see Cleanup).
- Binary built via the documented SPA-embed pipeline: `npm run build` → sync `dist/spa/` into `pkg/gateway/spa/` → `CGO_ENABLED=0 go build -tags goolm,stdjson`.
- Provider: OpenRouter (`OPENROUTER_API_KEY` from shell env), model **`z-ai/glm-5-turbo`** throughout (every live-model result below is a statement about this one model, not models in general, per the plan's own coverage-limits note).
- Onboarded via `POST /api/v1/onboarding/complete` (admin user `uat-admin`), REST-authenticated with the returned bearer token.
- All scenarios driven as real WebSocket chat turns against `/api/v1/chat/ws` using a purpose-built Python driver (`websocket-client`) — genuine LLM tool selection throughout, no mocked/scripted provider. Real tool-call arguments and results are pasted or summarized with exact field values below; full raw JSON transcripts were captured to local evidence files for every scenario (paths cited per scenario; not attached to this report, available on request).
- Fixture built per the plan's Environment-setup script (mount root, canary root, race-fixture root, hash manifests, expected-mutations log) under disposable `/tmp` paths — never inside `~/.omnipus` or any real project directory.
- Sandbox mode: default (kernel-enforced on this macOS host; Seatbelt confirmed independently active — see S49).
- **Build-sha discipline**: no rebuild occurred during this batch's execution; two gateway *restarts* did occur (both against the same binary and same `$OMNIPUS_HOME`, no config wipe) — once to temporarily disable the app-level `workspace_path_guard` for S49's kernel-isolation test (then restored), and once to enable `sandbox.audit_log` for Group I (kept enabled for the remainder of the run). Both restarts are called out at their point of use below; all agent/workspace state was confirmed to persist correctly across both.

## 0. Scenario ledger (this batch's scope: S38–S80, S57b, S67b — 45 rows)

| ID | Status | One-line result | Evidence pointer |
|---|---|---|---|
| S38 | PASS | requested_skill loaded deterministically into child's first turn, result names it | `S38_delegate_permitted.json` |
| S39 | PASS | Unpermitted requested_skill → literal `delegation_denied`, no child session created (pre-dispatch refusal) | `S39_delegate_denied_v3.json` |
| S40 | PASS | Unresolvable slug → literal `skill_not_found`, distinct from S39's classification field | `S40_delegate_notfound_v2.json` |
| S41 | PASS | Proven by S38's own setup (parent already ungranted) — not re-run, substitution recorded | S38 evidence |
| S42 | PASS (characterization) | N=5: 2 genuine Skill loads, 3 honest non-loads, no FAIL condition fired | `S42_trial{1-5}.json` |
| S43 | BLOCKED | Plan-premise defect: literal self-delegation is unconditionally denied platform-wide, unrelated to skills | `S43_self_delegate.json` |
| S44 | PARTIAL | 14/20 trials completed (6 lost to model tool-arg glitches); precision=recall=100% on completed subset | `S44_enum.json` |
| S45 | PASS | `read_file` on registry skill refused 2/2, literal `permission_denied`, cites ADR-072 D10.3 Part A | `S45_S46_readgate.json` |
| S46 | PASS | Same turn: `Skill` load still succeeds, full content returned | `S45_S46_readgate.json` |
| S47 | PASS | Cross-references S34 (see flagged row below) in the same turn — confirms the interaction, but S34 itself is a **CRITICAL FAIL** | `S48_S34_mount_files.json` |
| S48 | PASS | Mounted `CLAUDE.md` readable directly via `read_file`, not gated | `S48_S34_mount_files.json` |
| S49 | PASS | macOS Seatbelt independently blocks `bash cat` on skill path even with app-level guard disabled; `Skill` load still succeeds same turn | `S49_seatbelt_v2/v3.json` |
| S50 | PASS (accepted gap) | `git -C acme show HEAD:...SKILL.md` bypasses grant+audit — matches ADR's own named, accepted gap | `S50_git_bypass_v2.json` |
| S51 | PASS | Zero refusals attributable to the literal word "skills" in path/command/content | `S51_shellguard_v3/v4.json`, `S51_debug.json` |
| S52 | NOT RUN | Linux host (ci-omnipus) not dispatched to within this round's time budget — NOT N-A-ENVIRONMENT per the plan's own rule | — |
| S53 | N-A-ENVIRONMENT | No Windows host available in this environment | — |
| S54 | PARTIAL | Cache-differentiation signal unobservable — no positive content in either workspace to differentiate (see cross-cutting finding) | `S54_*.json` |
| S55 | NOT RUN | Same missing-signal issue as S54; not attempted | — |
| S56 | PARTIAL | No eviction log line exists — exactly the adjudication rule's own anticipated PARTIAL shape | gateway.log grep |
| S57 | NOT RUN | 501-skill fixture not built given time budget and the cross-cutting mount-skill defect | — |
| S57b | NOT RUN | Same as S57 | — |
| S58 | NOT RUN | Same as S57 | — |
| S59 | **FAIL** | Empty (`description: ` / YAML-null) description bypasses validation; whitespace-only correctly rejected | `S59_S60_authoring.json` |
| S60 | PASS | Exact name-echo rejected; genuine near-miss correctly accepted | `S60_retry.json` |
| S61 | PARTIAL | Code confirms exact 1024/1025 boundary; live LLM could not reliably transcribe the precise strings (928-accept / 1218-reject bracketing observed instead) | `S61_1024only.json`, `S61_over.json`, code cite |
| S62 | PASS | Well-formed trigger description accepted, then a live model genuinely triggers on it unprompted | `S62_create.json`, `S62_trigger_v2.json` |
| S63 | NOT RUN | Not independently re-run given time budget; counters otherwise confirmed working (see corrected-finding note) | — |
| S64 | NOT RUN | Frontend/SPA render-layer claim; this round drove raw WS only, no SPA instance | — |
| S65 | NOT RUN | Same as S64 | — |
| S66 | NOT RUN | Same as S64 | — |
| S67 | **FAIL** | Search-mode `Skill` calls produce ZERO audit records — code-confirmed (`execSearch` has no audit references) | `S67_search.json`, code cite |
| S67b | PASS | (a) load-mode-only structurally guaranteed for delegate preloads; (b) shape-argument confirmed, not independently cross-checked against a live write record | code cite |
| S68 | PASS | Denial produces a full audit record regardless of thread-visibility state | `S67_denied.json`, audit log |
| S69 | PASS | `last_invoked` populates correctly for loaded/denied, absent for never-invoked — with a flagged dependency on `sandbox.audit_log` (off by default) | GET /api/v1/skills before/after |
| S70 | BLOCKED | `edit_skill` cannot even locate the mount-based race-fixture skill (NOT_FOUND) — same cross-cutting defect, extended to authoring | `S70_precheck.json` |
| S71 | BLOCKED | Same as S70 — no reachable write path to race against | `S70_precheck.json` |
| S72 | **FAIL** | 5/5 trials picked-neither — FAIL CONDITION fired (threshold ≥3/5) | `S72_trial{1-5}.json` |
| S73 | NOT RUN | Not run given time budget and the dominant S72 confound | — |
| S74 | BLOCKED | Same cross-cutting mount-skill defect | — |
| S75 | BLOCKED | Same cross-cutting mount-skill defect | — |
| S76 | PARTIAL | (a)/(d) confirmed; (b)/(c) blocked by the same cross-cutting defect — NOT a security finding | `S76_precheck.json` |
| S77 | PASS | Denied-vs-not-found distinguishes existence with zero extra metadata leaked (15/20 completed) | `S77_enum.json` |
| S78 | BLOCKED | Same cross-cutting mount-skill defect | — |
| S79 | NOT RUN | N=3×18-turn conversations infeasible in remaining time budget | — |
| S80 | NOT RUN | Large-skill authoring attempts timed out twice (model transcription-fidelity issue) | `S80_create*.json` (empty result) |

**Counts**: PASS = 18, FAIL = 3, PARTIAL = 5, NOT RUN = 12, BLOCKED = 6, N-A-ENVIRONMENT = 1. **Sum = 45**, matching this batch's full scope (43 numbered S38–S80 + S57b + S67b). A 46th row (S34, formally Group D's scenario) is reported separately below as a cross-referenced bonus finding, not counted in this batch's own tally.

## 1. Verdict

**Do not ship this build as-is.** This batch found **3 confirmed defects within its own scope** (S59: empty-description validation bypass; S67: search-mode Skill calls are never audited; S72: a live model reliably fails to reach for a granted, plainly-matching skill 5/5 times) plus **1 CRITICAL cross-cutting defect discovered while testing S54/S70/S76/S77 and directly confirmed by this batch's own S34/S47 evidence**: mounted-project-skill discovery, load, search, and authoring (D4.1/D6/D6.1 — the entire "mount is the grant" surface this ADR's Group D exists to prove) appear **non-functional** in this build for every code path this batch could reach it from (`Skill` tool load returns `skill_not_found` for real, present, readable project skills; `edit_skill` returns `NOT_FOUND` for the same; and separately, `read_file` on the SAME project skill files succeeds and returns full content — meaning the read-gate that is supposed to force those loads through the Skill tool is ALSO not applied to the mount-skills subtree). This is squarely Group D's assigned scope (S17–S33) and MUST be cross-verified by whoever owns that batch before any release decision — if confirmed there too, it invalidates a large fraction of Group D's own scenario set for the same root cause, not five independent defects.

Counts from the ledger above: **18 PASS, 3 FAIL, 5 PARTIAL, 12 NOT RUN, 6 BLOCKED, 1 N-A-ENVIRONMENT** (45 total). A round with 12 NOT RUN and 6 BLOCKED rows cannot be reported as anything close to a clean pass regardless of the PASS count — the verdict names what was not run: S52 (Linux kernel-deny spike — genuinely available environment, simply not exercised this round), S57/S57b/S58 (cache byte/count-bound stress under the 501-skill fixture), S63–S66 (SPA thread-render claims — this round never launched a browser), S73/S79/S80 (live-LLM scenarios that ran out of time budget), and the six BLOCKED rows (S43, S70, S71, S74, S75, S78 — all blocked by either a plan-premise mismatch or the cross-cutting mount-skill defect).

The three in-scope FAILs and the cross-cutting finding are the blocking reason. Everything else — the accepted gaps (S50's git-plumbing bypass, S77's MIN-003 disclosure bound), the environment-limited rows (S52/S53), and the correctly-behaving majority — are not release blockers on their own.

## 2. Defects found (unsoftened, in dispatch-ready detail)

### 2.1 CRITICAL — Mounted project skills are non-functional across discovery, load, search, authoring, AND the read-gate meant to protect them

**Discovered via**: investigating S54 (cache differentiation), independently reconfirmed via S47/S34 (cross-checking Group D), S70/S71 (race-test precheck), S76 (malicious-content precheck), and consistent with S44's Group E footnote (`requested_skill` never resolved acme's project skills).

**Evidence, all against the same healthy, correctly-workspace-bound mount** (confirmed via `list_mounts` and session `meta.json.workspace_id` matching):

1. `Skill(name="deploy-helper")` — a real project skill physically present at `acme/.claude/skills/deploy-helper/SKILL.md`, confirmed independently readable via `read_file` — returns `{"error":"skill_not_found","message":"No skill named \"deploy-helper\" is installed.","skill":"deploy-helper","tool":"Skill"}`. NOT_FOUND, not a grant-denial.
2. `list_skills` returns `{"count":0,"skills":[]}` identically whether the agent is acting in the mounted workspace or a plain no-mount workspace — no differentiation at all.
3. `edit_skill(name="race-target", content="...")` against a dedicated, freshly-mounted single-skill fixture returns `{"error":{"code":"NOT_FOUND","message":"skill \"race-target\" not found","suggestion":"use create_skill to author a new skill"},"success":false}` — the authoring path cannot locate it either. The real on-disk file was confirmed unchanged afterward.
4. **Separately and independently**, `read_file` on the SAME project-skill files (`acme/.claude/skills/deploy-helper/SKILL.md`, `acme/.claude/skills/db-migrate/SKILL.md`) **succeeds and returns full content verbatim** — tested via both the workspace-relative mount path and the real absolute host path, both succeeding identically. This is the read-gate half of the defect: D10 Part B requires the whole `.claude/skills/`/`.omnipus/skills/` subtree inside a mount to be a deny-root for direct file access (matching how S45 proved this works correctly for `$OMNIPUS_HOME/skills/*` registry skills) — it is not applied to mount skills at all.
5. The mount-CREATION response (`POST /workspaces/{id}/mounts`) DOES correctly report `skills_count` and the D1.2 disclosure warning both times this was tested (2 and 1 skills respectively) — so the one-time directory walk that powers that warning works; it is specifically the ongoing runtime resolution (Skill tool load/search, `list_skills`, `edit_skill`, and the read-gate) that is disconnected from it.

**Likely root cause** (not confirmed by code read within this batch's time budget — flagged for whoever fixes this): the mount-skills directory walk that powers the mount-creation warning appears to run independently of whatever index/resolver the `Skill` tool, `edit_skill`, `list_skills`, and the read-gate's deny-root matcher all consult at runtime — i.e., two separate code paths were likely built for "detect project skills exist" (working) vs. "actually wire them into the runtime resolution/protection surfaces" (not working, or not reaching this particular call path).

**Blast radius within this batch's own scope**: S54 (PARTIAL), S55 (NOT RUN), S70/S71 (BLOCKED), S74/S75 (BLOCKED), S76(b)/(c) (BLOCKED, not a security result), S78 (BLOCKED), plus the flagged S34/S47 cross-check (FAIL). **Blast radius in Group D** (not this batch's own scope, but almost certainly affected — flag for that batch's owner): S17, S18, S18b, S19, S20–S33 all depend on the same mechanism this evidence shows is not working.

**Recommended fix locations** (for whoever picks this up): the mount-skill resolver used by `Skill`'s load/search resolver closures (`pkg/agent/loop.go`, the `SetResolver`/`canUse` block registering the `Skill` tool) and by `edit_skill`/`create_skill`'s shelf-aware authoring path need to actually consult the per-workspace mount-skills index that the mount-creation endpoint already builds; separately, `tools.ResolvePath`'s carve-out/deny-root matcher needs a second matcher for `<mount-root>/.claude/skills/**` and `<mount-root>/.omnipus/skills/**` (both path forms), mirroring the existing `$OMNIPUS_HOME/skills/*` matcher that S45 proved works.

### 2.2 FAIL — S59: empty (YAML-null) skill description bypasses required-field validation

`create_skill` with `description: ` (nothing after the colon) is **accepted** (`{"action":"created",...,"success":true}`); `description: "   "` (explicit whitespace) is correctly **rejected** (`"description is required and must not be empty or whitespace-only"`). FR-010 requires both forms rejected. Root cause hypothesis: the frontmatter parser likely treats an absent/null YAML scalar differently from an explicit-but-blank string before either reaches the shared empty-or-whitespace validator. Fix location: `pkg/skills/authoring.go`'s frontmatter validation should route a YAML-null/absent `description:` through the identical check applied to whitespace-only.

### 2.3 FAIL — S67: `Skill` search-mode calls produce zero audit records

Code-confirmed, not just a live gap: `pkg/tools/skill.go::execSearch` (the entire search code path) contains no references to `audit`/`Audit` anywhere in the file. The only caller of `audit.EmitSkillCall` in the codebase is the load-resolver closure registered in `pkg/agent/loop.go` (~lines 2884–2937) — five call sites, all inside the load path; the `canUse` closure that filters search results does not call it either. FR-018/018a requires every Skill call, load and search alike, to produce exactly one audit record; `mode="search"` is a defined, valid enum value (`IsValidSkillCallMode`, `pkg/audit/skill_call.go`) that the running system provably never emits. Live-confirmed: a genuine `Skill(query="release")` call (confirmed executed via its tool-result frame) produced no corresponding `skill.call` audit entry, while load-mode calls immediately before/after it did. Fix location: add `audit.EmitSkillCall(..., audit.SkillCallModeSearch, ...)` in `pkg/tools/skill.go::execSearch` or its caller.

### 2.4 FAIL — S72: live model reliably fails to reach for a granted, plainly-matching skill (5/5)

Two registry skills, both with the identical trigger-worded description "Use when the user asks about deployment," materially different bodies, both genuinely granted to a fresh agent. N=5 independent fresh conversations, each sent "I need help with a deployment." — **the `Skill` tool was never called in any of the 5 trials.** In every trial the model instead tried `read_file` directly on the skill's real path first (refused), explored unrelated tools, and ended by asking the user a clarifying question. This exceeds the plan's own FAIL CONDITION threshold (picked-neither in ≥3 of 5). The SAME first-instinct pattern (reaching for `read_file` before ever discovering `Skill`) was independently observed as a byproduct in 3 of 5 S42 trials and in S62's and S77's first attempts — a recurring, reproducible pattern across this whole round with this model, not a one-off. This is a genuine trigger-discipline concern for D2/D3's premise that the menu+reminder reliably surfaces the `Skill` tool as the natural next action, independent of the two-skill ambiguity S72 was designed to measure.

## 3. Accepted-and-documented gaps confirmed as-designed (not release blockers)

- **S50 (git-plumbing bypass, D10 Part B's own named gap)**: `git -C acme show HEAD:.claude/skills/deploy-helper/SKILL.md` via `bash` succeeds, zero grant check, zero audit — exactly as the ADR states this stays open by choice. Confirmed live, matches the ADR's own description → PASS for this plan, not a new finding.
- **S77 (MIN-003 disclosure bound)**: an ungranted agent CAN learn which of a wordlist of slugs are installed via the load door's denied-vs-not-found split, exactly as accepted — and critically, confirmed the refusal message never leaks anything beyond bare existence (no description/shelf/metadata in either template). Matches the accepted bound exactly.
- **S49 (macOS Seatbelt, T5c-macos)**: confirmed independently active at the kernel layer, distinct from and in addition to the app-level workspace guard — both mechanisms proven to co-exist correctly in the same turn as the Skill loader's below-the-gate exemption.

## 4. Two-layer comparison table (tool call vs. `bash`, every row tested both ways)

| Claim | Tool-path result | `bash`-path result | Agree? |
|---|---|---|---|
| Registry skill file read | `read_file` → `permission_denied`, cites ADR-072 D10.3 Part A (S45) | `cat` → refused, app-level guard message; with app-level guard disabled, kernel Seatbelt refusal "Operation not permitted" (S49) | Agree — both deny, at two independently-verified layers |
| Registry skill load | `Skill(name=...)` → succeeds, full content (S46, S49) | N/A (no shell-equivalent for the Skill tool's own resolution) | — |
| Project-mount skill file read | `read_file` on `.claude/skills/deploy-helper/SKILL.md` → **succeeds, full content** (S34/S47) | `cat` on the same path (implicit via S48/S50's successful reads of adjacent files) → also succeeds, no gate at all | **DISAGREE with the design** — both paths are open when the design requires both closed; this is the CRITICAL finding, not a tool-vs-shell divergence |
| Project-mount skill load | `Skill(name="deploy-helper")` → `skill_not_found` (cross-cutting finding) | N/A | — |
| git-plumbing read of a project skill | N/A (no tool path) | `git -C acme show HEAD:...` → succeeds, zero grant check (S50) | Confirmed accepted gap, not a disagreement |
| Literal word "skills" in shell commands | N/A | `grep`, `git commit -m "add skills"`, `echo`, `cat`, `ls` → all succeed, zero refusals (S51) | Confirmed clean — G2 stays closed |

## 5. What couldn't be tested and why

- **S52 (Linux kernel-deny spike, G1)** — a Linux host (the project's `ci-omnipus` Fly worker) is available but was not dispatched to within this round's time budget. Per the plan's own rule this is `NOT RUN`, explicitly **not** `N-A-ENVIRONMENT` — G1 stays an open gap requiring a dedicated follow-up run.
- **S53 (Windows read-gate gap, G7)** — no Windows host available in this environment at all; a permanent, not deferred, gap for this round.
- **S63–S66 (SPA thread-render behavior)** — this round drove the gateway exclusively via raw WebSocket frames; no browser/SPA instance was launched, so the frontend rendering half of these claims (hidden-by-default success, shown-by-default denial, verbose-chat toggle) genuinely cannot be observed from here. The backend/persistence half (every tool call present as full frames + transcript entries) is confirmed as a byproduct of every other scenario in this round.
- **S57/S57b/S58 (cache byte/count bounds under the 501-skill fixture)** — the fixture was not built given time constraints and the discovery that the underlying mount-skill mechanism is non-functional, which would make any such fixture's "menu stays uncapped" claim untestable for the same reason S54 is.
- **S73, S79, S80 (live-LLM, high-N or long-conversation scenarios)** — not run given this round's remaining time budget after the rest of Groups E–J; S80 was specifically attempted twice and timed out both times on a model-side long-string-generation issue.
- **S70, S71, S74, S75, S78** — blocked by the cross-cutting mount-skill defect (Section 2.1), not by time.
- **S43** — blocked by a plan-premise mismatch (Section "Defects" below covers this as a plan-authoring gap, not a code defect).

## 6. G1–G9 gap-coverage table (this batch's contribution)

| Gap | What it is | This batch's scenario(s) | Outcome |
|---|---|---|---|
| G1 | Linux child-only kernel deny unverified | S52 | NOT RUN (Linux host available, not dispatched to — G1 stays open) |
| G2 | `skills` shell-guard/path-deny split (closed in D10.1) | S51 | **Confirmed closed** — zero refusals on literal "skills" text across grep/git/echo/cat/ls |
| G7 | Windows shell-child read gate absent, stays absent | S53 | N-A-ENVIRONMENT (genuine gap, not testable here) |
| G8 | `bash`-mediated writes to skill paths unaudited | (covered by Group D's S31, not this batch — but S50's git-plumbing-bypass result is the read-side analogue and confirms the same "accepted, not silent" posture) | Cross-referenced, matches ADR description |
| G9 | Concurrent access — writer-vs-writer and reader-vs-writer | S70 (N=10 planned), S71 (N=50 planned) | **BLOCKED before any trial could run** — `edit_skill` cannot locate the mount-based race-fixture skill at all (see Section 2.1); G9 could not be probed this round for project-mount skills specifically |

G3–G6 are Group D's assigned scope in this plan and are not covered by this batch.

## 7. Cross-referenced Group D finding: S34 (bonus evidence, not counted in this batch's tally)

While executing this batch's own **S47** ("cross-reference: mount ordinary-file-vs-skills-subtree scope precision... using the SAME turn to catch any interaction" — which explicitly requires S34's result), this batch generated direct, reproducible evidence for **S34** itself (formally assigned to Group D):

**S34 — FAIL (CRITICAL)**: `read_file` on a mounted PROJECT skill's `SKILL.md` **succeeds and returns full content**, tested and reproduced via the workspace-relative mount path, a second project skill, AND the real absolute host path (all three succeed identically) — directly contradicting D10 Part B's requirement that the whole `.claude/skills/`/`.omnipus/skills/` subtree be a read-gate deny-root, exactly as it correctly IS for registry skills (proven working in this batch's own S45). Positive control in the same turn (`acme/CLAUDE.md`, `acme/README.md`) confirms this is not "everything in the mount is blocked" — it's specifically that the deny-root matcher was never extended to cover a mount's own skills subtree.

**This is passed to whoever owns the Group D batch report for their own adjudication and scenario ledger** — it is not counted among this batch's 45 rows, since S34 was not this batch's assignment, but the evidence is real, reproducible, and directly informs Group D's S20b/S34/S34b/S35 cluster.

## 8. Cleanup confirmation

All disposable resources created during this round were deleted and independently re-verified gone:

- **Agents**: 6 disposable agents created (`uat-skills-tester`, `UAT Skills Child`, `UAT Skills Child NoGrant`, `UAT Self-Delegate Tester`, `UAT S62 Trigger Tester`, `UAT S72 Ambiguity Tester`) plus 1 more created-and-deleted immediately per-scenario for S76 (`UAT S76 Disposable Injection Tester`, deleted before any other group resumed, per the plan's safety rule). Follow-up `GET /api/v1/agents` after all deletions shows exactly the 10 default core/system agents (`mia, jim, ava, ray, worker, planner, explorer, researcher, judge, plansupervisor`) — zero disposable agents remain.
- **Workspaces**: 3 disposable workspaces created (`uat-acme-workspace`, `uat-plain-workspace-groupG`, `uat-s76-disposable-injection-test` — the last deleted immediately after S76 per the plan's safety rule). Mounts were explicitly deleted before workspace deletion. Follow-up `GET /api/v1/workspaces` shows exactly the one auto-created default workspace (`My Workspace`) — zero disposable workspaces remain.
- **Mount fixture integrity (the single highest-severity check)**: re-ran the manifest command from Environment setup over `$UAT_MOUNT_ROOT`, `$UAT_CANARY_ROOT`, and `$UAT_RACE_ROOT` independently of the gateway and diffed against the recorded `MANIFEST-*.sha256` files:
  - **Canary root**: byte-identical, zero diff.
  - **Race fixture root**: byte-identical, zero diff (consistent with S70/S71 being blocked before any write was attempted).
  - **Mount root**: diff shows exactly the mutations this batch's own scenarios recorded as intentional — `acme-service/skills-note.txt` (S51's grep-target fixture file), `.git/config`/`.git/index`/`.git/logs/*`/`.git/refs/heads/main`/`.git/COMMIT_EDITMSG` and new git objects (S51's git-identity fix + two commits), a `skills-demo/notes.txt` fixture file, and `s76-malicious-project/.claude/skills/cleanup-helper/SKILL.md` (the S76 fixture, created after the baseline manifest was taken). **No unattributed change, no unexpected deletion, no file silently reverted to baseline when a mutation was expected.** Mount confinement held throughout this batch's testing.
- Fixture directories (`$UAT_MOUNT_ROOT`, `$UAT_CANARY_ROOT`, `$UAT_RACE_ROOT`, `$UAT_EVIDENCE`) deleted after the integrity check above.
- Gateway process stopped (`pkill`, confirmed via `lsof -i :18901` returning empty) and `$OMNIPUS_HOME` deleted.
- **Real production install confirmed untouched throughout**: `lsof -i :5000` shows the pre-existing `omnipus-f` process still listening, undisturbed, on its own separate port for the entire duration of this round; `~/.omnipus` was never read from or written to by this session (verified: its contents and mtimes are consistent with the founder's own prior activity, not this round's).

## Appendix: corrected mid-round finding (retraction notice)

Early in this round (roughly the first 40 minutes, spanning most of Group E and the start of Group F), a check of the `"System prompt built"` debug log line (the sole source of `static_chars`/`dynamic_chars`/`total_chars`, which OBS-002's falsifiability claim depends on) found zero occurrences despite `-d` being passed and other debug-level lines being present, and this was initially going to be reported as a CRITICAL observability defect. That conclusion was **wrong** and is not carried into this final report: after two later gateway restarts (done for S49 and for enabling audit logging), the counter log line began firing correctly and consistently, with real, sane values captured across 22+ turns from that point onward. What remains true and is worth a small follow-up: the log was reliably silent for the entire lifetime of the FIRST gateway process of this round despite an identical `-d` flag, and only started working after a restart — a minor debug-log-activation reproducibility quirk, not a missing or broken log line, and not an OBS-002 defect. Evidence gathered against the first (silent) gateway instance (most of Group E and the very start of Group F) could not use this signal for corroboration and is noted as such at point of use above (S38's caveat); evidence from S50 onward had full access to it.
