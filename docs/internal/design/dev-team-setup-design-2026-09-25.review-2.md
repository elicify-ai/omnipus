# Re-review — Omnipus Development Team Agent Setup Design (third revision)

**Reviewed:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-agent-refresh/docs/internal/design/dev-team-setup-design-2026-09-25.md` (read-only; not modified by this review)
**Reviewer:** prometheus-prompt-engineer (agent-definition quality owner)
**Date:** 2026-09-25
**Scope (as commissioned):** (1) every founder-interview decision implemented faithfully and consistently; (2) the skills-based rules delivery implementable in Claude Code agent frontmatter; (3) role boundaries after the changes; (4) no model/CLI content; (5) the first review's findings still resolved.

Repo-relative paths below are under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-agent-refresh/`.

---

## Verdict

**APPROVE WITH CHANGES.**

The third revision is faithful: all twenty interview decisions are implemented and internally consistent, all twenty findings from the first review are resolved in the text, and the document still scrubs clean of model/provider/ladder content. The two High findings below are new facts, not regressions: one is the same allow-list failure class as the original H1, now on the `Skill` tool instead of MCP; the other is a "validated" claim about the Elicify repository that a commit landing two minutes before this revision was saved has made false. Both are cheap to fix and both must land before the rollout steps that draft the affected files (step 2, steps 5–6). The Mediums fix a mechanism the design leaves unspecified and an impossible git state in the RED step; the Lows are wording alignments.

Counts: **2 High / 2 Medium / 4 Low** (8 findings). Interview decisions: 20/20 implemented faithfully. Previous findings: 20/20 still resolved (two by mandate whose object is still on disk, as expected pre-rollout).

---

## Findings

| ID | Severity | Section | Finding | Change requested |
|---|---|---|---|---|
| R2-1 | High | 4.2 vs 4.3 rule 1, 6.6, 8.1 check 3 | The `tools:` allow-lists for **docs-verifier** and **prometheus-prompt-engineer** (`Read, Grep, Glob, Edit, Write`) omit the `Skill` tool. By the design's own verified premise (4.2: a specified list allow-lists the whole surface; an unnamed tool is revoked), these two roles cannot load `omnipus-shared-rules` — yet rule 1 orders every role to load it before acting, 6.6 says every agent file names it, and guard check 3 requires every agent file to name it. The guard cannot catch the conflict (it checks naming, not tool consistency) and neither role's dry-run pass criteria mention the skill load. Same failure class as the original H1, one field over. | Before rollout steps 5–6: either add `Skill` to both lists, or (cleaner — `disallowedTools` is a real field, verified this session) replace both allow-lists with a denylist: `disallowedTools: mcp__*`, which removes only MCP and lets everything else, including `Skill` and future tools, inherit. State the general rule once: a role whose file names a skill must retain the `Skill` tool. |
| R2-2 | High | 6.4 + Appendix + R12 + rollout step 2 | "test-integrity-audit **does not exist upstream** — validated 2026-09-25" is stale. The Elicify repository's branch `feat/test-integrity-audit-skill` (local and on origin, checked out in worktree `elicify-Skills-wt-audit`) carries commit `d87fdcf` — "feat(skills): add test-integrity-audit skill (auditor as a loadable skill)", authored 2026-09-25 12:24 (+0700), two minutes before this revision was saved — adding `skills/test-integrity-audit/SKILL.md` (353 lines) plus five knowledge files (878 insertions). Not merged into `main` or `feat/elicify-document-skills`, so the interview-time validation was likely honest when made; the shipped revision nonetheless asserts non-existence as present fact, and R4.2's founder instruction ("take the NEWEST auditor **+ its skill** from the Elicify skill/agent repository") is now satisfiable as written. | Re-validate including all branches, then either vendor the branch's skill (SOURCE.yaml naming branch + `d87fdcf`) — the founder-instructed path — or record an explicit founder ruling to keep the local derivation until the branch merges. Point guard check 9's drift-watch for `test-integrity-audit` at the upstream **skill** (branch or its merge), not only the agent file. Update 6.4, R12, the appendix row, and rollout step 2 to match whichever source is chosen. |
| R2-3 | Medium | 6.1 + 6.6 + 8.1 check 6 | The skill-delivery mechanism is left unspecified, and the permitted-skills mapping is incomplete. (a) 6.6 says "the agent file names its permitted skills" without saying whether naming means the `skills:` frontmatter field or body prose. Verified against the official Claude Code agent docs this session: the `skills:` field **exists and is a preload mechanism** — the full skill content is injected into the subagent's context at startup — and it does **not** restrict access (restriction is done via `tools`/`disallowedTools` on the `Skill` tool). Used as preload, rule 1 becomes structural (rules in context with no Skill-tool turn and no compliance risk) and open question R4's "does a named-skill load fire reliably" largely dissolves for the always-on rules. (b) Check 6 enforces "the mapping of 6.1", but 6.1 omits `omnipus-design-system` — which finding F8, the 6.3 split, and the 10.2 frontend dry-run all require frontend-lead to load — and the `gitnexus-*` skills the code-touching roles cite for rule 9. As written, a compliant frontend-lead.md fails CI. | (a) Adopt the `skills:` frontmatter field as the mechanism: preload `omnipus-shared-rules` on all ten agents plus the role skill on backend-lead/frontend-lead; keep situational skills (`omnipus-failure-triage`, `elicify-test-writing`, `test-integrity-audit`) as on-demand Skill-tool loads, matching their "on … dispatches only" audiences. State the preload-vs-on-demand split per skill in 6.1. (b) Extend the check-6 permitted-skills table beyond the six dev-team skills: `omnipus-design-system` for frontend-lead; the gitnexus skills for the code-touching roles that cite them. Isolation stays guard-enforced (preload cannot restrict), which is worth saying explicitly. |
| R2-4 | Medium | 7.1 (RED step) | "several qa-lead instances in parallel, one per area, EACH in its own worktree, **on the feature's work branch**" is an impossible git state: one branch cannot be checked out in two worktrees at once, so every RED lane after the first fails at `git worktree add`. 5.6 point 3 already contains the correct shapes; only 7.1's phrasing combines the two that conflict. | Reword: each RED author works in its own worktree on its **own per-area branch off the feature's work branch**, with team-lead merging the RED packs into the feature branch — or one RED worktree with disjoint test-file trees and immediate-commit discipline. Optionally evaluate the `isolation: worktree` frontmatter field (exists, verified this session — temporary isolated worktree per subagent) for RED lanes before rollout. |
| R2-5 | Low | 8.1 check 7 + 8.2 | `teammates:` is not a Claude Code frontmatter field (verified against the official docs this session). The convention still works — Claude Code ignores unknown keys, and the guard reads its own registry key — but the design presents it as a frontmatter list without saying it is guard-defined, so a future editor may trust it as harness semantics. | One clause in 8.1 check 7: "a guard-defined key, ignored by the harness"; and have a step-9 dry-run confirm a file carrying it still loads normally. |
| R2-6 | Low | 4.1 team-lead Must-never vs 5.7 vs 7.1 | Three wordings for `main`: 4.1 forbids team-lead to "merge or push to main" outright; 5.7's team-lead cell says "Never without founder approval" (implying it may, with approval); 7.1 ends with a human-driven merge under founder approval. The interview only says "main needs founder approval". | Pick one story and align all three. Recommended (matches 7.1 and the standing repo rule): team-lead never merges or pushes `main`; a human performs the merge, always under founder approval. |
| R2-7 | Low | 7.2 | A failure fix touching the four security packages is a "change touching pkg/security/…" and therefore falls under 7.5's security-lead review — but 7.2 never says so; only the reviewer-finding routing edge in 4.1 covers security-area changes. A failure dispatch under time pressure would read 7.2 alone. | One line in 7.2: a failure fix in the security packages follows the 7.5 flow (backend-lead fixes, security-lead reviews before landing). |
| R2-8 | Low | 10.2 | No dry-run row for prometheus-prompt-engineer, although this revision expands its mandate (author of the six dev-team skills) and rollout steps 1–2 depend on exactly that work. Every other role has a known task and pass criteria. | Add one row, e.g.: given a written mandate, draft a toy agent file plus a one-page skill split; pass = frontmatter valid against this repo's conventions, structured payload returned, no mandate drift (it did not decide the role's job). |

---

## Check 1 — Interview decisions: 20/20 implemented, consistently

Every row of `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/agent-refresh/INTERVIEW.md` maps to implemented text (the design's own section-12 table is accurate; each row below was independently located in the body):

| Ref | Decision (compressed) | Where implemented | Status |
|---|---|---|---|
| R1.1 | Hybrid lead, ~95% orchestration, small steps allowed | 4.1 team-lead row; 5.5 boundary table | Implemented |
| R1.2 | Stop for decisions + regular status updates | 5.5 duties row + status-format paragraph; 10.2 team-lead dry-run ("status update appears unprompted") | Implemented |
| R1.3 | Specialists push own branch only; lead lands integration after gates + founder; main needs founder | 5.7 rights table; 4.1 Must-never columns; 7.1 landing step | Implemented (wording wobble on `main` — R2-6) |
| R1.4 | Gate on work branch before integration merge; epic gate before main; branch never hard-coded | 5.7 structural rules; 7.1 gate placement; guard check 10 | Implemented |
| R2.1 | Skills delivery: one shared + role-specific, no cross-loading; CLAUDE.md references skills | Section 6 (6.1, 6.6); guard check 6; rollout step 3 | Implemented (mechanism + mapping gaps — R2-3) |
| R2.2 | CLAUDE.md = what, skills = how, link don't copy | 6.5; 6.2 item 7; 6.3 split table | Implemented |
| R2.3 | Terse/technical specialists; lead translates | Rule 14; 6.3 reporting split; 6.2 item 9 | Implemented |
| R2.4 | Rule conflict: stop, report blocked, propose alternative | Rule 15; 6.2 item 8 | Implemented |
| R3.1 | security-lead = auditor/reviewer; security-audit skill checked (claude-security v0.11.0) | 4.1 security-lead row; 7.5 | Implemented |
| R3.2 | Parallel qa-leads, TDD, skill-fit proposal | Superseded by R5.1 (supersession recorded in section 12); RED in 7.1 + 5.6 | Implemented as superseded |
| R3.3 | UAT: qa-lead plans, lanes run, one validator per lane | 7.3; 4.1 qa-lead / uat-validator rows | Implemented |
| R3.4 | CI triage: explain before deciding | 7.2 opening paragraph (origin irrelevant; HC#7) | Implemented |
| R4.1 | claude-security installed project-wide | 7.5; rollout step 10; 2.1 inventory row | Implemented |
| R4.2 | No separate auditor agent; take newest auditor + skill from Elicify; lighter process | 6.4 derivation + SOURCE.yaml; 7.1 CHECK | Implemented, but the upstream fact flipped — R2-2 |
| R4.3 | Every failure fixed whatever origin; orchestrator coordinates | 7.2; 5.4 row; 4.1 failure edge | Implemented |
| R4.4 | backend-lead implements security code; security-lead audits | 4.1 both rows; 7.5; findings-routing edge | Implemented |
| R5.1 | RED / GREEN / CHECK + 7-reviewer gate | 7.1 pipeline; 5.6 point 2 | Implemented (RED worktree phrasing — R2-4) |
| R5.2 | Copy skills in with source commit; guard warns on newer upstream | 6.4 SOURCE.yaml; guard check 9; section 9 refresh | Implemented |
| R5.3 | Auditor skill validated absent upstream; derive; separate contexts | 6.4; 4.1 RED-vs-CHECK edge; R12 | Premise now stale — R2-2 |
| R5.4 | No failure-fixer agent; debug skill; developer dispatched with it | 7.2 (skill + flow); 3 diagram note; 4.1 failure edge | Implemented |

No section contradicts an interview decision. ci-triage is fully gone (role, table row, guard expectations); its only remaining mentions are historical context, which is correct.

## Check 2 — Skills delivery in frontmatter: implementable, and one free upgrade available

Verified this session against the official Claude Code subagent documentation (code.claude.com) and the repo's own files:

- The `skills:` field exists (three of the six current repo files already carry it — architect, security-lead, qa-lead — listing the phantom skills finding F7 documents). Semantics: **preload** — full skill content injected at startup; it restricts nothing; blocking skill use is done via `tools`/`disallowedTools` on the `Skill` tool.
- Consequence for this design: the shared + role-specific split is implementable as `skills:` preload (structural delivery of rule 1); the founder's isolation rule ("no role loads another role's detailed rules") is *satisfied by construction* for preloaded content — a role only ever has in context what its file lists — while runtime cross-invocation of a foreign skill remains possible but guard- and review-visible only via the acknowledgement line. That is an acceptable residual and stronger than the current prose-only arrangement. See R2-3 for the requested changes.
- `tools:` is confirmed a whole-surface allow-list, MCP included — H1's resolution (omit the field for MCP-dependent roles) was the right call and stands.
- Plugin reviewers: unchanged and implementable — they are plugin-supplied, so the paste-the-shared-skill-body-into-the-dispatch contract (6.6) is the correct and only channel; nothing frontmatter-related is required of them.
- Bonus for R4: the documented existence of `omitClaudeMd` ("skip user/project/local CLAUDE.md files") implies subagents load CLAUDE.md by default. That downgrades R4 from Unknown to Inferred-yes (docs-based; still worth the planned early-rollout test) — the design's self-sufficiency hedge remains good practice either way.

## Check 3 — Role boundaries after the changes: hold, with one line to add

| Boundary | Verdict |
|---|---|
| security-lead (auditor/reviewer) vs backend-lead (implementer) | Clean and consistent everywhere it appears: 3 (diagram), 4.1 (both rows + Must-nevers), the findings-routing edge, 7.5 (implement -> review -> verify-fix loop), 10.2 (security-lead dry-run "writes no code"). security-lead holds no production trees. One gap: 7.2 does not route security-package *failure* fixes through 7.5 review — R2-7. |
| qa-lead RED vs CHECK | Clean: different instances, CHECK starts fresh-context and never audits its own RED suite (4.1 edge, 7.1), which honours the Elicify README's separate-context rule structurally. Local-suite limits restated (4.1 edge) — the original M8 conflict stays dead. The user-level test-integrity-auditor agent is superseded by the derived skill for this process, with retirement correctly left as a founder call outside the repo (Q4). |
| developer + troubleshooting skill | Clean: no failure-fixer role anywhere; 7.2's omnipus-failure-triage is dispatched to backend-lead/frontend-lead owning the tree (3, 4.1 edge, 5.5 row, 7.2); "not mine" is explicitly forbidden (HC#7 alignment). |
| Remaining soft spots | 4.1's architect recusal and scripts/ ownership fixes from the first review are in place. New minor seams are R2-4 (RED worktrees), R2-6 (main-merge wording), R2-7 (security failure fixes). |

## Check 4 — Model/CLI independence: clean

Re-scrubbed this session: grep over the design for glm, grok, codex, minimax, openrouter, claudez, claudeg, claudem, gpt-, sonnet, haiku, opus, z-ai — zero matches. The only harness references (Claude Code version numbers, `claude -p`, the `--agent ""` opt-outs, `/claude-security`, plugin names) are mechanics the founder's own decisions require, not model or runner choice. The one remaining model-name line in the ecosystem sits at line 66 of `.claude/shared/agent-rules.md` (still present, as expected pre-rollout); its deletion is mandated by the 6.3 split and rollout step 1 — that step is where M12's resolution actually lands, and the step-1 review should confirm it.

## Check 5 — Previous findings: 20/20 still resolved

All twenty dispositions were re-located in the revised text and are genuine: H1 (4.2 omit-for-MCP + Q3 resolved + R10), M1–M13 and L1–L6 as the disposition table claims. Two notes, neither a regression:

- M1/M12 resolve by **mandate**: the 241-line file with the model-ruling line is still on disk by design and dies at rollout step 1. Resolution is real but pending that step.
- L2's `teammates:` convention is implemented as proposed and is fine as a guard-defined key — refined by R2-5 now that the docs confirm it is not a harness field.

---

## Notes — what this re-review verified itself

- Frontmatter ground truth: grepped all six files in `.claude/agents/` — `skills:` on three, `model` on six, no `tools:` in any real frontmatter (the one grep hit is inside prometheus's embedded fallback skeleton, body text). The design's claim "no existing agent file uses `tools:`" holds.
- Official docs fetch (code.claude.com, subagent reference): field table and semantics for `skills` (preload, non-restrictive), `tools` (whole-surface allow-list, MCP included), `disallowedTools` (denylist; `mcp__*` supported), `omitClaudeMd`; `teammates` confirmed absent. These carry the day's findings R2-1, R2-3, R2-5.
- Elicify repository, by git inspection: current branch `feat/elicify-document-skills`; `skills/elicify-test-writing` and `agents/test-integrity-auditor.md` as the design describes; branch `feat/test-integrity-audit-skill` (local + origin, checked out in worktree `elicify-Skills-wt-audit`) with commit `d87fdcf` (2026-09-25 12:24 +0700) adding `skills/test-integrity-audit/` — SKILL.md 353 lines + 5 knowledge files; not an ancestor of `main` or `feat/elicify-document-skills`. Basis of R2-2.
- `.claude/settings.json` holds `enabledPlugins` only; `.claude/shared/agent-rules.md` at 241 lines with the model line at line 66; eleven SKILL.md files under `.claude/skills/` (six nested under `gitnexus/`); ten user-level agent files (nine lane ops + test-integrity-auditor) — all matching the design's inventory.
- Not re-verified (accepted on the design's founder-verified labels): the Claude Code main-session behaviours of 5.1/5.3 (prompt replacement, headless inheritance, opt-outs) and the UAT campaign overturn statistics.

**Order-sensitivity:** R2-2 belongs before rollout step 2; R2-1 and R2-3 before steps 5–6 (the files and skills get drafted there); everything else can ride the normal drafting flow.
