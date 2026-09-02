# UAT Report — Skill activation and loading (ADR-072), Batch 1: Groups A, B, C

Date: 2026-09-02
Branch under test: `release/v0.1.1`
Commit under test: `a828c133700d1d21e983448e0b610276e612680c` (confirmed HEAD at start of this run; branch clean, no reset needed). All scenario evidence below was collected against a single binary build from this exact sha — no rebuild occurred mid-round.
Plan followed: `docs/internal/qa/uat-plan-skill-activation-2026-09-01.md`
Scope: **Groups A, B, C only** (scenarios S1–S6b, S7–S12, S13–S16 — 17 rows total). Groups D and E–J were run by other agents in parallel; not duplicated here.

Pre-flight re-verification of the plan's own "NOT EXECUTABLE YET" status block (required before running anything, since that block was already stale at plan-authoring time): re-ran all five checks at `a828c133`.
- `grep -rn '"Skill"' pkg/sysagent/tools/ pkg/tools/` → `pkg/tools/skill.go` defines a real `SkillTool` (`Name() string { return "Skill" }`). **Implemented.**
- `pkg/skills/loader.go` no longer enforces `maxSkillsInSummary` as a hard cap (confirmed live in S4 below — 29 granted skills, 0 truncation of menu entries). **D1.1 entry-count removal implemented** (but see DEFECT-1 below — a **different** piece of D1.1/FR-009 was not implemented).
- `pkg/agent/context.go::skillAllowed` denies by default (confirmed live via S7/S13/S14 `permission_denied` refusals on ungranted/absent-grant skills). **D5 implemented.**
- `requested_skill` exists on `delegate` (`pkg/tools/delegate.go`) — **D9 implemented** (Group E's scope, not tested here, but confirmed present in code).
- Not independently re-checked here (Group D's scope): `activeSkillNames`/`SkillsFilter` union.

**Conclusion: the feature is live and testable.** This plan is no longer blocked.

Environment: isolated `$OMNIPUS_HOME=/tmp/omnipus-uat-skills-batch1-20260902-002956` (main batch) plus a second, completely separate fresh-install home `/tmp/omnipus-uat-s16-fresh-20260902-010903` (S16 only), both deleted at cleanup. Isolated ports `19417` (main) and `19533` (S16), each confirmed free via `lsof -i` before use — **not** port 5000 (occupied by macOS ControlCenter) and **not** port 18901 (a different, concurrently-running parallel UAT session's gateway — discovered via `lsof`/`ps aux` mid-run and avoided; see §5 "Environment turbulence" below). Binary built from `a828c133` via the documented SPA-embed pipeline (`npm run build` → sync `dist/spa/` → `CGO_ENABLED=0 go build -tags goolm,stdjson`). Provider: OpenRouter (`OPENROUTER_API_KEY` from shell env), model `z-ai/glm-5-turbo`, onboarded via `POST /onboarding/complete`. All scenarios driven as real WebSocket chat turns (`/api/v1/chat/ws`) against the live gateway process, using a small Python driver script (`websocket-client`) — genuine LLM tool selection throughout, no mocked/scripted provider. Debug logging (`-d -T`, i.e. `--debug --no-truncate`) was used to capture full, untruncated system-prompt content for the static/dynamic cache-boundary checks (S6) and the menu-content checks (S4).

## 0. Scenario ledger (batch 1: 17 rows)

| # | Scenario | Verdict | Evidence pointer |
|---|---|---|---|
| S1 | No-skill turn carries zero skill instructions | **PASS** | §4.S1 |
| S2 | `Skill` loads exactly the named skill | **PASS** | §4.S2 |
| S3 | Loaded skill does not persist to next message | **PASS** | §4.S3 |
| S4 | Menu is genuinely uncapped | **FAIL** | §4.S4 — DEFECT-1 |
| S5 | Search stays bounded at 5 despite uncapped menu | **PASS** | §4.S5 |
| S6 | Per-turn reminder present, ≤240 bytes, outside cached prefix | **PASS** | §4.S6 |
| S6b | Trigger disambiguation / back-door text / no-narration (3 obligations) | **FAIL** | §4.S6b — DEFECT-1 (obligation b) |
| S7 | Load door refuses ungranted slug | **PASS** | §4.S7 |
| S8 | Search door excludes ungranted skill | **PASS** | §4.S8 |
| S9 | Menu door excludes ungranted skill | **PASS** | §4.S9 |
| S10 | Slash-command door refuses ungranted skill | **PASS** | §4.S10 |
| S11 | `list_skills` grant-filtered, drops `path` for every entry | **PASS** | §4.S11 |
| S12 | Five-door sweep, tabulated | **PASS** | §4.S12 |
| S13 | Absent grant list denies everything | **PASS** | §4.S13 |
| S14 | Empty `[]` byte-identical refusal to absent | **PASS** | §4.S14 |
| S15 | CRIT-002 regression: emptied core-agent grant list survives 3 restarts | **PASS** | §4.S15 |
| S16 | Fresh install seeds core roster; does not re-fire on 2nd boot | **PASS** | §4.S16 |

**Counts: 15 PASS, 2 FAIL, 0 PARTIAL, 0 NOT RUN, 0 BLOCKED, 0 N-A-ENVIRONMENT. Total: 17/17 rows adjudicated.**

## 1. Verdict

**Do not ship as-is — one real, reproducible, low-risk-to-fix defect (DEFECT-1) blocks S4 and S6b.** 15 of 17 scenarios in this batch passed cleanly with live evidence; the on-demand activation core (S1–S3), grant enforcement's five doors (S7–S12), and the default-none grant regression protections (S13–S16, including the CRIT-002 restart-survival check) are all solid. The two failures are **both caused by the same single stale block of prompt text** at `pkg/agent/context.go:566–570` that D1.1/FR-009 required removed but which was never touched when the rest of ADR-072 landed — see DEFECT-1 below. It is a small, precisely-located fix (rewrite one `fmt.Sprintf` header string), not a design or architecture problem.

## 2. Anything that got through / regressed (unsoftened)

### DEFECT-1 (CONFIRMED, live-reproduced): the pre-ADR-072 `# Skills` header text was never deleted — it still tells the agent to bypass the `Skill` tool via `read_file`, still claims a cap that no longer exists, and still points at `find_skills` (the marketplace tool) for already-installed skills.

**Location:** `pkg/agent/context.go`, inside `buildStaticPrompt`'s skills block (search the file for the literal string below; the surrounding function moves — cite the string, not a line number, per this repo's own churn warning):

```go
parts = append(parts, fmt.Sprintf(`# Skills

The following skills extend your capabilities. To use a skill, read its SKILL.md file using the read_file tool. This list is capped — call find_skills to search the full installed catalog, including any not shown below.

%s`, skillsSummary))
```

**Why this is wrong, verified two ways:**

1. **Static-text check (S6b obligation b, FR-009/N3).** The plan requires grepping the assembled system prompt for `read_file`, for a filesystem path adjacent to a skill name, and for any instruction to use marketplace search for *installed* skills — all three must be absent. All three are **present, verbatim**, in the live prompt (captured via `-d -T` debug logging, S1's turn, `total_chars=6827`/`static_chars=5491`):
   > "The following skills extend your capabilities. **To use a skill, read its SKILL.md file using the read_file tool.** This list is capped — **call find_skills** to search the full installed catalog, including any not shown below."

   This is the exact N3-named back door ADR-072 exists to close: an instruction telling the agent to walk around the `Skill` tool (and, by extension, D10's read-gate) via `read_file`.

2. **Live behavioral confirmation (S6b obligation a, 3×3 confusion matrix).** Prompted "I want to use my release-notes skill to draft some release notes now" (a real granted skill, unambiguous intent), the model's **first tool call was `read_file({"path": "skills/release-notes/SKILL.md"})`** — not `Skill`. It only reached `Skill(name="release-notes")` after that `read_file` call failed (wrong path — file tools are workspace-rooted, not global-skills-rooted, so it errored `file not found` rather than leaking content) and after a `ToolSearch` disambiguation round. Raw evidence:
   ```
   tool_call_start | read_file        | {"path": "skills/release-notes/SKILL.md"}
   tool_call_result | read_file       | "[UNTRUSTED_CONTENT]\nfailed to open file: ... no such file or directory\n[/UNTRUSTED_CONTENT]"
   tool_call_start | ToolSearch       | {"query": "find skills search catalog"}
   tool_call_start | Skill            | {"name": "release-notes"}
   tool_call_result | Skill           | "RELEASE-NOTES-BODY-MARKER. Draft release notes from the recent commit log, ..."
   ```
   The model did exactly what the stale prompt text told it to do. This is not a hypothetical risk — it is the measured behavior of a real tool-calling LLM against the live system prompt.

3. **S4 (menu uncapped) is a second, independent casualty of the same string.** With 29 skills granted (well past the deleted 20-entry cap), the actual **entries** were genuinely uncapped — all 29 `<skill>` blocks present in the XML menu, confirming D1.1's entry-count removal did land. But the **footer sentence** — "This list is capped — call find_skills to search the full installed catalog, including any not shown below." — is still emitted regardless of count, which is now simply false (there is no cap, and there is nothing "not shown below"). The plan's S4 explicitly requires "no truncation **and no 'call find_skills' footer text anywhere in the system prompt**" — the footer text's mere presence is a FAIL of that stated requirement, independent of whether truncation itself occurs.

**Fix suggestion (not applied — release notes require human-authored commits per this repo's CLAUDE.md; flagging for a human/backend-lead to apply):** replace the hardcoded header with wording that matches the `Skill` tool's own `Description()` (`pkg/tools/skill.go:153-158`), e.g. "Call the `Skill` tool with the skill's exact slug (or a `query` describing what you need) to load its full instructions for this turn." — drop the `read_file` instruction, the cap claim, and the `find_skills`-for-installed-skills suggestion entirely. Add a regression test asserting the assembled system prompt never contains the substring `read_file` adjacent to skill-menu content, and never contains `capped`/`not shown below`.

**No other defects found.** Every other check in this batch — the five grant-enforcement doors (S7–S12), the default-none grant behavior and its restart-survival guarantee (S13–S16), the search-bound-at-5-vs-uncapped-menu asymmetry (S5), and the reminder's cache placement (S6) — matched the design exactly, with no silent fallback, no wildcard policy, and no fail-open surprise.

## 3. Anything that should work and doesn't (usability regressions)

Covered fully by DEFECT-1 above: a user who names a skill they already hold gets a slower, noisier path to the right answer (an extra failed `read_file` call, visible in tool-call verbosity, before the correct `Skill` call) purely because of stale prompt copy — not because the underlying mechanism is broken. The mechanism itself (`Skill` tool, grant enforcement, D5 default-deny) works correctly in every scenario tested.

## 4. Scenario detail (raw evidence)

### S1 — No-skill turn carries zero skill instructions — PASS
Granted `alpha-skill`/`beta-skill`/`gamma-skill` to a disposable `Main` agent (`uat-skills-tester`), sent an unrelated message ("Name one fact about the planet Saturn..."). Debug log (`System prompt built`, `static_chars=5491 dynamic_chars=1329 total_chars=6827`) plus the full untruncated preview showed the `# Skills` menu listing all three with descriptions, and **zero** occurrence of `ALPHA-SKILL-BODY-MARKER`/`BETA-SKILL-BODY-MARKER`/`GAMMA-SKILL-BODY-MARKER` anywhere in the prompt or response. WS response contained no tool calls, direct answer only.

### S2 — `Skill` loads exactly the named skill — PASS
Asked the agent to call `Skill(name="alpha-skill")` and report the body verbatim. Raw frames:
```
tool_call_start | Skill | {"name": "alpha-skill"}
tool_call_result | Skill | "ALPHA-SKILL-BODY-MARKER. This is the alpha skill's full instructions."
```
`grep -c "BETA-SKILL-BODY-MARKER\|GAMMA-SKILL-BODY-MARKER"` over the full debug log for this and the following turn = **0**. Only the named skill's content appeared, anywhere.

### S3 — Loaded skill does not persist to next message — PASS
Immediately after S2, sent a second unrelated message in the **same session**. Response: direct answer ("Blue"), no tool call. Debug log for this turn: `static_chars=5491 dynamic_chars=1329 total_chars=6827` — identical to S1's baseline, confirming no residual skill-body injection.

### S4 — Menu is genuinely uncapped — **FAIL**
Granted 29 skills total (`alpha-skill`, `beta-skill`, `gamma-skill`, `release-notes`, `multi-skill-01`…`multi-skill-25`). Debug log: `static_chars=11284` (up from 5491 at 3 skills, confirming a real rebuild). Full untruncated preview: **all 29** `<skill><name>…</name></skill>` entries present (`grep -c "<name>"` = 29, `grep -c "<skill>"` = 29) — entry-count half of D1.1 confirmed working. **But** `grep -n "capped\|find_skills to search"` on the same preview returns a hit: *"This list is capped — call find_skills to search the full installed catalog, including any not shown below."* — the footer text the plan explicitly requires absent. **FAIL** per the plan's stated requirement ("no truncation and no 'call find_skills' footer text"). Root cause: DEFECT-1 (§2).

### S5 — Search stays bounded at 5 — PASS
Same 29-skill grant. `Skill(query="widget-processing procedure")` (a query matching all 25 `multi-skill-*` descriptions):
```
tool_call_result | Skill | "Found 5 skills. Call Skill again with the exact slug in 'name' to load one.\n{\"matches\":[{\"name\":\"multi-skill-22\",...},{\"name\":\"multi-skill-19\",...},{\"name\":\"multi-skill-03\",...},{\"name\":\"multi-skill-08\",...},{\"name\":\"multi-skill-11\",...}]}"
```
Exactly 5 of the ~25 matching candidates returned — confirms the deliberate menu-unbounded/search-bounded asymmetry.

### S6 — Per-turn reminder present, ≤240 bytes, outside cached prefix — PASS
Reminder text extracted verbatim from the S4 turn's untruncated preview: *"When starting a new task or acting on a new request, check the "# Skills" list for one whose description matches, call Skill before proceeding, and do this silently -- never narrate it."* — measured **185 bytes** (UTF-8), ≤240. Positional check: the `---` separator between the static `# Skills` block and the dynamic `## Current Time` block falls at preview line 1721 (this turn's numbering); the reminder appears at line 1747, well inside the dynamic (post-separator) region. `cached=true` in the same debug log entry confirms the static block (containing the menu) is served from the workspace prompt cache, while `dynamic_chars=1329` (containing the reminder) is rebuilt every turn — the cached/uncached split matches the design.

### S6b — Trigger disambiguation, back-door text, no-narration — **FAIL** (obligation b)
Three lettered obligations, all evidenced (no partial-PASS per plan rule 4; obligation (b) failed outright, making the whole scenario FAIL):

- **(a) 3×3 confusion matrix (characterization, reported not adjudicated):**
  - Prompt 1 ("I want to use my release-notes skill…") → model called `read_file` first (wrong tool, back-door text), then `Skill(name="release-notes")` (eventually correct). See DEFECT-1 evidence above.
  - Prompt 2 ("What skills do I have installed right now?") → **no tool call** — answered directly from the already-visible `# Skills` menu (reasonable, not confused, but doesn't exercise `list_skills`).
  - Prompt 3 ("find me a skill for sentiment analysis in the marketplace… I don't have installed") → correctly called `find_skills(query="sentiment analysis", limit=10)`, returned marketplace results. Correct disambiguation.
  - Dumped tool descriptions for all three via `ToolSearch(names=["Skill","list_skills","find_skills"])` — each description text is internally well-disambiguated (each explicitly says what it is *not*); the confusion in prompt 1 traces to the separate stale header text, not to the tool descriptions themselves.
- **(b) FR-009/N3 back door — FAIL.** Confirmed present, confirmed live-triggered. See DEFECT-1.
- **(c) FR-015 no narration — PASS.** Across S1–S6 and S6b's three prompts, no turn narrated skill consideration ("let me check my skills…") in user-visible output. The dynamic-context reminder text itself explicitly instructs *against* narration ("do this silently -- never narrate it") — this satisfies FR-015's requirement that no prompt text *ask for* narration.

### S7 — Load door refuses ungranted slug — PASS
Agent granted `alpha-skill`/`beta-skill`/`gamma-skill`/`release-notes`/25×`multi-skill-*`; `skill-y-ungranted` installed but never granted. `Skill(name="skill-y-ungranted")`:
```json
{"error": "permission_denied", "message": "You are not granted the \"skill-y-ungranted\" skill.", "permanent": true, "reason": "skill not granted to this agent", "tool": "Skill"}
```
Literal `permission_denied` classification (the existing `PermissionDeniedCode`), names the slug specifically.

### S8 — Search door excludes ungranted skill — PASS
`Skill(query="ungranted-Y procedure UAT batch1 door tests")` — a query near-verbatim-matching `skill-y-ungranted`'s own description. Result: 5 matches, **none** named `skill-y-ungranted` (`gamma-skill`, `alpha-skill`, `beta-skill`, `multi-skill-12`, `multi-skill-16` instead) — the ungranted skill's name/description are absent from the result set itself, not merely unloadable.

### S9 — Menu door excludes ungranted skill — PASS
`grep -n "skill-y-ungranted"` over the S4 turn's full menu preview (29 other skills listed) → **0 matches**. Never appeared regardless of query.

### S10 — Slash-command door refuses ungranted skill — PASS
Sent literal content `/skill-y-ungranted`. Server-side resolution (`applyExplicitSkillCommand` → `ResolveSkillName` → `skills.ResolveSkillName(..., cb.skillAllowed, ...)`) applies the same grant check as the `Skill` tool; since the agent isn't granted it, resolution fails and the token falls through as an ordinary chat message (per D4: unknown/denied `/<x>` is not an error). The model, correctly seeing no such skill in its own menu, replied in prose: *"I do not have a skill called 'skill-y-ungranted' available. It is not in my installed skill catalog, and searching for it returns no results. This skill either does not exist or has not been granted to this agent."* No `SKILL-Y-BODY-MARKER` content appeared anywhere — the skill was never activated for the turn.

### S11 — `list_skills` grant-filtered, drops `path` for every entry — PASS
`list_skills()` raw result: `"count": 29`, and `skill-y-ungranted` absent from the 29 (grant-filtered, matches the 29-skill grant exactly). Programmatic check over all 29 entries: `any('path' in s for s in skills)` → **False**. Union of all keys across all 29 entries: `{source, description, id, name}` — no entry, including fully-granted ones, carries a filesystem path.

### S12 — Five-door sweep, tabulated — PASS
| Door | Mechanism | Result |
|---|---|---|
| Load | `Skill(name=Y)` | `permission_denied`, names Y (S7) |
| Search | `Skill(query=...)` matching Y | Y absent from results (S8) |
| Menu | `# Skills` static block | Y absent regardless of query (S9) |
| Slash | `/Y` | Falls through as plain text, not activated (S10) |
| `list_skills` | enumeration | Y absent; no entry leaks `path` (S11) |

No door leaked Y's name, description, or existence.

### S13 — Absent grant list denies everything — PASS
Created a fresh `Main` agent (`uat-batch1-nofield`) with no `skills` field in the create payload at all (response omits the `skills` key entirely, confirming D5's "absent = none" at the wire level). `Skill(name="alpha-skill")`:
```json
{"error": "permission_denied", "message": "You are not granted the \"alpha-skill\" skill.", "permanent": true, "reason": "skill not granted to this agent", "tool": "Skill"}
```

### S14 — Empty `[]` byte-identical refusal to absent — PASS
Second fresh agent (`uat-batch1-emptyarr`) created with `"skills": []` explicitly. Identical `Skill(name="alpha-skill")` call produced the **byte-for-byte identical** payload to S13's:
```json
{"error": "permission_denied", "message": "You are not granted the \"alpha-skill\" skill.", "permanent": true, "reason": "skill not granted to this agent", "tool": "Skill"}
```
Same classification, same message, same every field.

### S15 — CRIT-002 regression: emptied core-agent grant list survives 3 restarts — PASS
**Methodology note (plan deviation, justified):** the plan's text says "empty Mia's skill grant list via the API." Live-tested: `PUT /api/v1/agents/mia` with `{"skills": []}` returns **403** `"cannot modify locked agent identity or prompt"` — the REST handler (`pkg/gateway/rest.go`, `updateAgent`) explicitly blocks `req.Skills != nil` on any locked agent as "B-2 defense-in-depth" (core agents have compiled-in capability sets; runtime skill reassignment via the API is deliberately forbidden even for the operator). This is **stricter** than the plan's premise, not weaker — the only surviving path to empty a locked agent's grants is a direct on-disk edit of the per-agent entity file (`$OMNIPUS_HOME/entities/agents/mia.json`), which is exactly the "operator can edit the seed on their own installation" workflow CLAUDE.md's Constraint #6 describes. Used that path (disposable `$OMNIPUS_HOME` only, reverted at cleanup — confirmed reverted, see §7).

Emptied `mia.json`'s `"skills"` array to `[]`, then restarted the gateway three consecutive times, checking `GET /api/v1/agents/mia` after each:
| Restart | `skills` | `locked` | `name` | `type` |
|---|---|---|---|---|
| 1 | `None` (empty, field omitted) | `True` | `Mia — Assistant` | `core` |
| 2 | `None` | `True` | `Mia — Assistant` | `core` |
| 3 | `None` | `True` | `Mia — Assistant` | `core` |

The emptied grant list was never silently re-seeded across three boots, while identity fields (`locked`/`name`/`type`) remained correctly re-enforced every boot — confirming the `isFreshInstall` gate scopes to *just* the skills-seeding block, not the neighboring identity-tamper protection.

### S16 — Fresh install seeds core roster; does not re-fire on 2nd boot — PASS
Genuinely fresh, separate `$OMNIPUS_HOME` (never previously booted). First boot, `GET /api/v1/agents/{mia,jim,ava,ray}`:
```
mia: ['summarize', 'daily-briefing']
jim: ['plan']
ava: ['skill-authoring']
ray: ['summarize']
```
All four core agents received their default grants on first boot. Then directly edited `ray.json`'s `"skills"` to `[]` (simulating an operator override) and rebooted the same, now-non-fresh install:
```
BOOT2 mia: ['summarize', 'daily-briefing']   (unchanged)
BOOT2 jim: ['plan']                           (unchanged)
BOOT2 ava: ['skill-authoring']                (unchanged)
BOOT2 ray: None                               (stayed emptied — NOT re-seeded)
```
Confirms `SeedConfig`'s fresh-install gate fires exactly once: it does not re-fire on a second boot of the same install, and does not overwrite an operator's manual edit made between boots.

## 5. Two-layer comparison table (tool-path vs shell-path)

**Not applicable to this batch.** None of Groups A/B/C's 17 scenarios name a filesystem/sandbox-relevant claim with both a tool-call path and a `bash` shell path — that pairing is exercised by Group F (read-gating: `Skill` vs `read_file` vs `bash`), which is out of this batch's scope. `bash` was left `deny`d on all disposable test agents in this batch, matching the seeded default for a fresh `Main`/`Subagent` agent, and was never needed to complete S1–S16/S6b.

### Environment turbulence (methodology note, not a product defect)
Mid-round, the gateway process was twice reaped mid-session (once due to `config.json` missing a required `"version": 1` field on first boot attempt — self-corrected; once due to a genuine port collision with a **different, concurrently-running parallel UAT session's gateway** also using port 18901 — discovered via `lsof`/`ps aux`, resolved by moving to port 19417, confirmed via process-owner inspection that this was another agent's disposable session, never the founder's real `~/.omnipus` install). Separately, the gateway's own file-based JSON logger (`$OMNIPUS_HOME/logs/gateway.log`) stopped appending new entries for several turns despite those turns completing successfully (real LLM responses, real cost/token stats) — worked around by restarting the gateway (state fully persisted across restart, confirmed via `GET /agents/{id}` showing all prior grants intact) rather than relying on that log stream. This logging-silence behavior is flagged here as an incidental observation for future investigation; it was not adjudicated as an ADR-072 defect (out of this plan's scope) and did not affect any scenario's evidence quality — all scenario evidence above was captured either from live WS tool-call frames (primary, always available) or from the debug log at times it was confirmed actively appending.

## 6. G1–G9 gap-coverage map

None of G1–G9 (or the git-plumbing bypass, MIN-003/MAJ-002, Windows posture) are probed by Groups A/B/C — those gaps are all Group D/F/G/J territory. Not applicable to this batch; see the Group D report and the forthcoming Group E–J report(s) for that coverage.

## 7. Cleanup confirmation

- **Agents:** `uat-skills-tester` (Main, id `b10e4a1e-7e99-410d-a619-2e26e1329760`), `uat-batch1-nofield` (Main, id `f4ac1f8a-b095-4d46-bc59-c83472c3e551`), `uat-batch1-emptyarr` (Main, id `f8d3f195-bc8c-4cd5-b98d-07f1fa2f3b1b`) all `DELETE`d (`204` each). Two earlier missteps — `uat-batch1-nofield`/`uat-batch1-emptyarr` first created as `Subagent` type, discovered unaddressable via direct chat WS ("worker is invoked via delegation, not as a chat target") — were also `DELETE`d (`204` each) before being recreated as `Main`. Follow-up `GET /api/v1/agents` on the batch-1 gateway showed only the original 10 seeded agents (`mia, jim, ava, ray, worker, planner, explorer, researcher, judge, plansupervisor`) — all disposables confirmed gone.
- **Workspace:** `uat-batch1-workspace` (id `01M1F0T81HFZ615PQF960GDN5W`) `DELETE`d (`204`). Follow-up `GET /api/v1/workspaces` shows only the original `My Workspace` (`01M1F0E9C8P3PG8W8T8AQRVAPB`).
- **Mia's grant list (S15's on-disk edit):** reverted to its original seeded value `["summarize", "daily-briefing"]` directly in `entities/agents/mia.json`, confirmed via a follow-up gateway restart + `GET /api/v1/agents/mia` → `skills: ["summarize", "daily-briefing"]`, before the gateway was stopped for the final time.
- **Skill fixture files:** all test skills (`alpha-skill`, `beta-skill`, `gamma-skill`, `skill-y-ungranted`, `release-notes`, `multi-skill-01`…`25`) lived entirely inside the disposable `$OMNIPUS_HOME/skills/` directory, removed wholesale with the home directory (below) — no separate removal needed, nothing installed outside the disposable home.
- **Gateway processes:** both stopped via `kill` on their recorded PIDs (`19417`'s and `19533`'s owning PIDs) — confirmed via `lsof -i :19417`/`lsof -i :19533` returning empty after.
- **Filesystem:** `$OMNIPUS_HOME` (`/tmp/omnipus-uat-skills-batch1-20260902-002956`), the S16 fresh-install home (`/tmp/omnipus-uat-s16-fresh-20260902-010903`), and the built binary (`/tmp/omnipus-uat-batch1-bin`) all `rm -rf`'d; confirmed gone via follow-up `ls` (all three return "No such file or directory"). All other stray `/tmp/uat-batch1-*` scratch files removed.
- **Real production install (`~/.omnipus`):** never bound to, never had its port reused (batch-1 gateway ran on `19417`, S16's on `19533`, throughout). Real gateway's own process, PID `19940`, confirmed running **before** this session (`Mon05PM` start time) **and after** (`ps -p 19940` still shows the identical PID, elapsed `01-07:40:27`, i.e. it was never restarted or touched) — same PID before and after, confirming continuity.
- **Repository state:** no source files were modified as part of this UAT round (DEFECT-1 was identified and reported, not fixed — per this repo's CLAUDE.md, a genuine defect gets a human-authored fix with independent re-verification, which is a separate follow-up action, not part of this report).
