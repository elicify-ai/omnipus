# Dev Team Setup Design — Decision Record (companion file)

**Design:** `docs/internal/design/dev-team-setup-design-2026-09-25.md` (final revision, 2026-09-25)
**What this file is:** the provenance layer the design body deliberately does not carry — the mapping of every founder-interview round to where it is implemented, and the disposition of every review the design passed through. The design file states the current design; this file records how it got there.
**Binding input:** `uat/agent-refresh/INTERVIEW.md` (Rounds 1–16, the verified-fact note, and both lead-defaults blocks). Later rounds win over earlier ones wherever they touch the same point.

ID-collision warning, stated once: **four different review numberings live in this file**, each scoped to its own table — the first prometheus review (H/M/L), the second prometheus review (R2-*), the independent Opus review (D/Q/G/C/S, including its own C1–C6), and the final independent review (C/Q/N/G/S, C1–C11). Letter prefixes mean nothing across tables; always read an ID inside its table.

---

## 1. Founder-round mapping

Every row of the interview is a founder decision and overrides prior text. Mapping to implementation (section numbers refer to the design file):

| Interview ref | Decision | Implemented in |
|---|---|---|
| R1.1 | team-lead hybrid, ~95% orchestrator; may read code and do small steps | 4.1 team-lead row; 5.5 (the 95/5 boundary table) |
| R1.2 | Stop for decisions **and** give status updates — timing superseded by R12.1: event-driven only, no timed rounds | 5.5 (duties row + status-format paragraph); 5.2 |
| R1.3 | Specialists commit/push own work branch only; team-lead lands on the integration branch after green gates + founder agreement; `main` needs founder approval — landing actor refined by R9-c/R10.4 and re-split by R15.1 (in-session hand-back; separate sessions land themselves) | 5.7 (rights table); 4.1 Must-never columns; 7.1 landing step; 5.9 |
| R1.4 | Review gate on the feature's work branch **before** integration merge; whole epic gated before `main`; integration branch never hard-coded | 5.7; 7.1 (gate placement); guard check 10 |
| R2.1 | Rules delivered as skills: ONE shared skill + role-specific skills; no cross-loading; CLAUDE.md references the skills | Section 6 (6.1 set, 6.6 consumers); guard check 6; rollout stage 1 |
| R2.2 | CLAUDE.md = facts/hard constraints (the what); skills = procedure (the how), linking not copying | 6.5; 6.2 item 7; 6.3 split table |
| R2.3 | Specialists report terse/technical with evidence; team-lead translates to founder style | Rule 14 (4.3); 6.3 (reporting split); 6.2 item 9 |
| R2.4 | Rule conflict: stop and report blocked (rule, reason, alternative); never break a rule | Rule 15 (4.3); 6.2 item 8 |
| R3.1 | security-lead becomes auditor + reviewer; check for an Anthropic security-audit skill (found: `claude-security` v0.11.0) | 4.1 security-lead row; 7.5 (scan flow) |
| R3.2 | Several qa-leads in parallel, test-driven, proposal on skill fit | Superseded by R5.1's adopted flow and S2's one-instance default; implemented as RED in 7.1, 5.6 |
| R3.3 | UAT: qa-lead plans, lanes run, one validator per lane | 7.3; 4.1 qa-lead and uat-validator rows |
| R3.4 | CI triage ownership question — explain before deciding | Explanation and resolution in 7.2's opening paragraph (origin is irrelevant; HC#7) |
| R4.1 | `claude-security` plugin installed project-wide (`.claude/settings.json`) | 7.5; rollout stage 3; 2.1 inventory row |
| R4.2 | No separate test-integrity-auditor agent; take newest auditor + skill from Elicify; make the process lighter | 6.4 (skill vendored from the founder-commissioned branch, SOURCE.yaml); 7.1 CHECK; ownership edge in 4.1 |
| R4.3 | Failure origin/ownership irrelevant; orchestrator coordinates; every failure fixed incl. pre-existing (HC#7) | 7.2 (opening + flow); 5.4 row; 4.1 failure edge |
| R4.4 | backend-lead implements security code; security-lead audits/reviews | 4.1 backend-lead + security-lead rows; 7.5; findings-routing edge |
| R5.1 | Lighter 3-step flow: RED -> GREEN -> CHECK -> reviewer gate | 7.1 (the RED/GREEN/CHECK pipeline); 5.6 point 4. Round 6 revises: the gate is 8 reviewers; RED's default shape is one qa-lead instance (S2); the flow is the **feature** size of the three-size table |
| R5.2 | Skills copied into `.claude/skills/` with source commit recorded; guard warns on newer upstream | 6.4 (SOURCE.yaml); guard check 9; section 9 refresh policy |
| R5.3 | Auditor skill validated as NOT existing upstream; derive from the agent file; separate-context rule honoured | Premise flipped same-day: the founder commissioned the skill upstream, so it is vendored, not derived — 6.4; 4.1 RED-vs-CHECK edge; R12 |
| R5.4 | No failure-fixer agent; troubleshooting/debug skill; orchestrator dispatches a developer with it | 7.2 (the `omnipus-failure-triage` skill + flow); 3 (diagram note); 4.1 failure edge |
| R6.1 | Team-lead switch-on stays repo-wide; orchestration is GENERIC — default workers are ordinary Claude Code subagents (Agent tool); command-line delegation is founder-only, outside the repo, and its workers must start with an explicit agent override | Section 1 (scope + terms); 3 (reading-rules row); 5.3 (personal-layer paragraph) |
| R6.2 | Three change sizes — small (one code-reviewer), standard (build with tests in one step + 3 reviewers), feature (full flow + 8-reviewer gate); urgent is small-or-standard moved to the front of the queue | 7.1 (sizes table with per-size effort lines, G7); 5.7 (gate per size) |
| R6.3 | Landing: merge and push to the integration branch after the founder says yes in chat, then close the resolved issues with a comment citing the commit | 5.7 (the landing act); 7.1 landing step — actor split per R15.1 |
| R6.4 | security-lead joins the review gate (6 plugin reviewers + architect + security-lead = 8) and also reviews on demand | 7.1 (gate box); 7.5; 4.1 security-lead row |
| R7.1 | omnipus-design-system always preloaded into frontend-lead; the UX skills stay on demand for frontend-lead and architect | 6.1 (preload paragraph); 8.1 checks 3 and 6; 4.1 frontend-lead and architect rows |
| R7.2 | prometheus WRITES the product's prompt text, tool descriptions and embedded skills (`pkg/coreagent`, `pkg/sysagent`, `pkg/skills/embedded`); backend-lead wires the code | 4.1 prometheus + backend-lead rows; ownership edges; 3 (diagram) — extended to `pkg/tools` Description() text by R16.3 |
| R7.3 | New user docs drafted by the implementing lead, checked by docs-verifier against the code before landing | 4.1 docs-verifier row; 7.4; ownership edges |
| R7.4 | security-lead may write proof tests (test files only) that go into qa-lead's test pack | 4.1 security-lead row; 7.5 (proof tests); ownership edges |
| R8.1 | Unowned areas split: contracts (architect decides the shape, backend-lead edits + regenerates), CI/deploy/Makefile (backend-lead), e2e (qa-lead); cross-stack order contract first → stacks in parallel → one combined review | 4.1 rows + ownership edges; 7.1 routing rule |
| R8.2 | Bad result: retry once (sharper brief or fresh instance); second failure escalates to the founder with the evidence; never redo silently | 5.5 (bad-result row + paragraph); 4.1 team-lead Must-never |
| R8.3 | Standard-size reviewers are fixed: code-reviewer, silent-failure-hunter, pr-test-analyzer; security-lead only in the full gate (and on demand) | 7.1 (sizes table + gate box) |
| R8.4 | No default-prompt essentials in the shared skill for subagents — they stay in team-lead only | 5.1/5.2; 6.2 states the exclusion explicitly |
| Opus-review lead defaults | C1, C3, C4, C5, C6, D5, G3, G5, G7, G8, S2, S3, S4 — obvious fixes applied without asking | 5.3 (C1), 5.2 (C3), 8.1 check 8 + section 9 (C4), appendix (C5), 6.4 (C6), 7.2 (D5), 7.1 (G3 urgent, G7 effort lines), rollout stage 1 (G5), 10.2 (G8), 7.1 RED default + 5.6 (S2), 8.1 shipping note (S3 — later superseded in part by the all-at-once rollout, table 4), 10.2 + rollout stage 2 (S4) — full disposition in table 3 |
| R9.1 | Multiple teams and squads; a squad may have its own orchestrator; team-lead takes over the cross-session coordination the founder does by hand today | 5.9 (both levels); 4.4; 7.6 |
| R9.2 | Never wait idle; maximise safe parallelism; worktree isolation where needed; separate feature branches for bigger features delivered in parallel | 5.8 (principles a, b); 5.6 (branching, overlap); 7.6 |
| R9.3 | Use waiting time for safe work; every quality gate still obeyed | 5.8 (b, c); 7.7 |
| R9.4 | A planning-and-orchestration skill should be considered | Adopted — `omnipus-planning-orchestration`; contents fixed by Round 10 (6.1) |
| R9-a | Squads: nesting double-check requested; where workers are started outside the Agent tool, the squad orchestrator must be a subagent | Verified-fact row + 5.1; 5.9 level-1 closing paragraph |
| R9-b | Coordination: a ledger file outside the repo plus messages; messages only for urgent calls ("hold pushes") | 5.9 (ledger spec; messages rule — mechanism named per final-review N4); 8.2 path-matching note |
| R9-c | Landing: squads land on the integration branch themselves, announcing first — NOT a chief-run queue | 5.9 (landing rule); 5.7 (rights table) — refined by R15.1 (in-session hand-back) and R15.2 (landing lock) |
| R9-d | Personality: impatient with idle time, patient with quality; calm and factual; owns problems whatever their origin | 5.8 |
| R10.1 | Idle-time work allowed without asking — all four classes (docs/cleanup; preparing next work; independent teams/squads on backlog work; reviews, docs checks, security scans on ready branches) | 7.7 (scan line conditioned on the R16.2 test); 5.8 (b) |
| R10.2 | Branching by size: feature-size gets its own feature branch and squad with worktrees; small/standard a short-lived work branch off the integration branch | 5.6 point 1; 7.1 (feature row) |
| R10.3 | Planning-skill contents (parallel planning; coordination protocol; idle-time playbook; status reporting) and MANDATORY load at session start and before every new plan | 6.1 (row + contents-and-mandate paragraph); 8.1 check 6; rollout stage 1; 7.1 planning step; N5 restates it as team-lead.md's and squad-lead.md's first body instruction |
| R10.4 | Squad landing sequence: announce → merge latest integration branch → checks green on that result → founder OK → push → post the commit; red integration branch = nobody lands | 5.9 (landing rule + red stop); 5.7 — re-check narrowed to affected-area CI + conflict-resolution review by R15.3; red stop gains the fix-only exception (final-review C1) |
| VF | Verified fact (2026-09-25): a subagent CAN start its own subagents — one level tested ("NESTED-OK") | 5.1; section 1 terms; section 3 reading rules; appendix; 10.2 (depth-2 dry-run); Q2 retested scope |
| R11.1 | Squad model is both: in-session squad-lead subagents orchestrating their own specialists; across sessions, one chief aligning squad-lead sessions through ledger and messages | 5.9; 4.4; 3 (diagram) — in-session squads now run from `squad-lead.md` (R16.1) |
| R11.2 | Chief named by the founder, records it in the ledger; other sessions join as squad leads, read the ledger at start, follow its holds and plans | 5.9 (chief file; read-before-acting); 4.4 — vacancy handling per final-review C5 |
| R12.1 | Status updates on events only — landed, failed, needs the founder; no timed status tables; supersedes R1.2's timing | 4.1 team-lead row; 5.5 (row + paragraph); 7.6; 7.7; 10.2 team-lead dry-run |
| R12.2 | Same-file overlap: parallel, merge later — both streams run in their own worktree and branch, conflicts resolved at merge; never two writers in one working copy | 5.6 point 2; rule 8 (4.3); 7.6 reading note |
| R12.3 | No width cap: a system-resources monitor holds new dispatches while saturated; heavy builds and tests still go to CI | 5.6 point 3 + monitor spec (signals redesigned per final-review N1/S3); 7.6; 7.7; rollout stage 1; R19 |
| R13.0 | Founder statement: every developer and reviewer agent ALWAYS verifies its own work, with evidence; reviewers treat every claim as untrue and wrong until they have verified it; every agent corrects itself and must not hallucinate | 4.5 (charter + shared traits + reviewer discipline) |
| R13-a1 | Evidence table mandatory at the end of every report: claim, evidence, certainty; a claim without evidence is Unknown; tests shown red before green | 4.5 (evidence table — self-check row added per final-review G5; red evidence defined per N6); rule 12 pointer (4.3); 6.2 item 9; 7.1 GREEN/CHECK reporting; 10.2 (every dry run shows one) |
| R13-a2 | Reviewers re-verify every claim their verdict depends on; a claim they cannot verify is UNVERIFIED — a WARNING, not a block; team-lead decides | 4.5 (reviewer discipline — re-verification by reading code/CI per N3); 5.5 (adjudication row); 7.1 (gate routing); 5.7 ("clean" gate definition); R24 |
| R13-a3 | All four anti-hallucination rules: read before citing; docs over memory; test the instrument; no fabricated gaps | 4.5 (the four rules) |
| R13-a4 | Final self-check before every report; visible correction at the top | 4.5 (shared traits 2–3); 5.2 (team-lead's own self-verification row); 10.2 (uat-tester dry-run criterion) |
| R14-a1 | The verification rules live in the AGENT PROMPT — the body of each developer and reviewer agent file, not a skill; the 6 plugin reviewers' rules ride team-lead's dispatch prompt | 4.5 (where-it-lives + delivery table); 6.1 (the two non-skill templates); 6.2 (exclusion list); 6.6 (dispatch-template row); 8.1 checks 11–12; rollout stage 1 |
| R14-a2 | Every reviewer finding gives a concrete failure scenario, evidence, severity and certainty; a style preference with no failure scenario is not a finding | 4.5 (reviewer discipline); 7.1 (gate routing); 7.5 (security findings); 4.1 reviewer rows |
| R14-a3 | Developers: do exactly the task; report every bug found by accident as a note, never fix on the side; impact analysis before editing a symbol | 4.5 (developer discipline); 4.1 developer rows; rule 9 (4.3); 7.2 (failure-fix reporting) |
| R14-a4 | When unsure: stop and ask team-lead with options and a recommendation; never guess silently | 4.5 (developer discipline); 4.3 rule 15 |
| R15.1 | In-session squad landing: hand back — the squad lead finishes a fully gated branch and hands it back; team-lead asks the founder and lands. Squads in separate sessions still land themselves | 5.9 (level 1 + landing rule); 5.7 (rights table — squad lead in-session "never lands"); 7.1 landing step; 7.6; 4.1 squad-lead row; 10.2 hand-back dry run |
| R15.2 | Simultaneous landings: a landing lock in the ledger — announcing takes a lock line, released after the push; first come, first served | 5.9 (`LANDING-LOCK`, landing rule step 1/5); 5.7; 7.6; 10.2 lock dry run; R25 |
| R15.3 | Re-check after merging the latest integration branch: CI for the AFFECTED areas plus a review of the conflict resolution — not the full size gate again | 5.7 (pre-landing re-check); 5.9 landing rule step 2; 7.6 |
| R15.4 | code-simplifier may edit the branch under review; its edits are covered by the remaining reviewers or a follow-up code-reviewer pass on its diff | 7.1 (gate box); 4.1 code-simplifier edge |
| R16.1 | A new `squad-lead.md` (the 11th agent file): preloads the planning skill, carries the discipline block, may start subagents, and hands back gated branches | 3 (team size + diagram); 4.1 squad-lead row; 4.4; 4.5 (classification + delivery); 6.1 (preloads); 8.1 checks 6 and 11; rollout stage 1 |
| R16.2 | Security scan in waiting time: first test whether an agent can start the claude-security scan; if it can, allow it on ready branches during waiting time; if not, security-lead recommends a scan and the founder starts it | 7.5 (scan flow); 7.7 (scan line conditioned); 10.2 agent-started-scan dry run; R27 |
| R16.3 | prometheus: remove its tool restriction and state its limits in its prompt instead; it also owns the tool `Description()` text in `pkg/tools`; backend-lead still wires the code | 4.2 (tools table); 4.1 prometheus row; ownership edges (product agent text); 3 (diagram) |
| R16.4 | Rollout all at once — the phased option declined; a full test/dry-run pass precedes switching team-lead on | 10.1 (three-stage, single switch-on); 10.2 as the stage-2 gate; R28; guard ships complete (8.1 shipping note) |
| Final-review lead defaults | C1–C11 contradictions fixed in favour of the later founder round; N1/S3, N3–N6, G1–G6, S1, S4 as recorded | Applied throughout the design body — full disposition in table 4 |

---

## 2. Review dispositions

Four reviews shaped the design. Each table below is that review's own numbering; dispositions state what happened and where the design handles it now.

### Table 1 — First review (prometheus-prompt-engineer, 2026-09-25): 1 High / 13 Medium / 6 Low

| ID | Severity | Disposition | Where addressed / why |
|---|---|---|---|
| H1 | High | Accepted | 4.2 rewritten: a `tools:` list allow-lists the whole surface including MCP, so the MCP-dependent roles omit the field and keep restrictions behavioural; Q3 resolved; trade-off recorded as R10; rollout drafts files under this policy |
| M1 | Medium | Accepted | 6.2: budget restated with the token-cost rationale; per-domain detail moved into role skills (6.3), shrinking the shared skill further |
| M2 | Medium | Accepted | 5.2: untrusted-content and secrets-hygiene rows added |
| M3 | Medium | Accepted | 5.3: positive-evidence self-check plus fail-safe default (indeterminate presence = worker); the recommended structural hook is tracked as Q7 |
| M4 | Medium | Accepted | 5.3: headless rule refined — no dispatching, no PR life-cycle actions, no `main` pushes, no integration landings; working-branch pushes follow the standing founder grant |
| M5 | Medium | Accepted | 4.2 (browser tools omitted from every list), 7.3 provisioning split (lane launcher provisions per-lane browser and account), 4.1 uat-tester inputs note |
| M6 | Medium | Accepted | 4.1 architect "Must never" gains the recusal rule; 7.1 states it inside the gate |
| M7 | Medium | Accepted | Superseded by the interview: ci-triage is removed as a role (7.2); the failure edge in 4.1 replaces the CI-adjacent-fixes edge |
| M8 | Medium | Accepted | 4.1 edges: qa-lead local-run scope retained; the qa-lead vs auditor split is now RED-vs-CHECK instances (R4.2/R5.3) |
| M9 | Medium | Accepted | 8.1 check 3: skill lookup recursive at any depth under `.claude/skills/`, with the gitnexus nesting called out |
| M10 | Medium | Accepted | 8.2: `# agent-guard: allow` marker covers checks 2 and 6; the dead-path warning needing a marker dies with the old file at split (6.3) |
| M11 | Medium | Accepted | Rule 13 (4.3) puts the acknowledgement line in every report and team-lead's review; 10.2 adds the adversarial dispatches that prove skills/rules outrank dispatch prompts |
| M12 | Medium | Accepted | 6.3 (split table): the model-ruling line is deleted outright at split time (the ruling stays in root CLAUDE.md); the design never names it |
| M13 | Medium | Accepted | 5.2 (statement) + rollout: team-lead.md carries no `model:` frontmatter line — now generalized by guard check 13, which bans the key in every repo agent file |
| L1 | Low | Accepted | 8.1 check 1: the frontmatter parser must handle YAML block scalars (`>-`, `|`), with the current `description: >-` file named as the trap |
| L2 | Low | Accepted | 8.1 check 7 + 8.2: an explicit optional `teammates:` frontmatter list is the convention; prose mentions never fire the check |
| L3 | Low | Accepted | 2.1 + 3: "kept unchanged" restated as bounded deltas (governance header, guard findings, `model:` removal, limits restated in its prompt — Round 16). The founder decision to keep the role stands |
| L4 | Low | Accepted | 6.2 item 6 (twin rule into the shared skill) + 4.1 edges (`scripts/` to backend-lead, agent/skill scripts to prometheus; docs-verifier corrections reviewed by team-lead) |
| L5 | Low | **Superseded** | Originally accepted as a dry-run log file; superseded by the later no-central-log ruling (final-review C9 records the stale acceptance): dry-run results live in commit messages, git history is the record — 10.2, rollout stage 2, and the governance trigger (section 9) read it from there |
| L6 | Low | Accepted | 7.1 + 4.1 edges: reviewer findings route to the owning tree's lead — security-package findings route to backend-lead as fixer with security-lead verifying (R4.4) |
| D2 | — | Nothing to flag | The review's independence scrub found no model/tier/ladder/CLI content in the design; re-checked at every revision since — still none |
| D3 | — | Addressed via M12 | The one model-name line sat in the adopted shared-rules file, not the design; deletion mandated in 6.3 + rollout stage 1 |

Counts: **19 accepted / 0 declined / 1 superseded (L5)**. L5's supersession is the final review's C9 catch — the disposition row had survived two rulings that changed it.

### Table 2 — Second review (prometheus-prompt-engineer re-review, 2026-09-25): 2 High / 2 Medium / 4 Low — all applied

| ID | Severity | Disposition | Where addressed / why |
|---|---|---|---|
| R2-1 | High | Accepted | 4.2: `Skill` added to the docs-verifier allow-list and the general rule stated — any `tools:` allow-list of a role that names skills lists `Skill`; the `disallowedTools: mcp__*` denylist alternative considered and rejected (removes only MCP, hands back Bash and the rest). prometheus's allow-list later removed outright (R16.3) |
| R2-2 | High | Accepted | 6.4 + 6.1 + R11/R12 + appendix + rollout: `test-integrity-audit` is **vendored** from the founder-commissioned upstream branch `feat/test-integrity-audit-skill` (commit `d87fdcf`); `SOURCE.yaml` records repo, branch and commit; the plan notes the source moves to `main` once PR #1 and the branch merge; the drift watch is retargeted at the upstream skill |
| R2-3 | Medium | Accepted | 6.1 (Delivery column + the `skills:` preload mechanism: preloads, does not restrict), 6.6, 4.2 (the Skill-tool rule), 4.3 rule 1, 8.1 check 6 (mapping extended with `omnipus-design-system` and `gitnexus-*`; the guard named the only mechanical isolation layer), R4 downgraded |
| R2-4 | Medium | Accepted | 7.1 RED step reworded: per-area branches cut from the feature's work branch, the dispatcher merges the packs (one branch cannot occupy two worktrees); the single-instance default named; the `isolation: worktree` field flagged for pre-rollout evaluation |
| R2-5 | Low | Accepted | 8.1 check 7: `teammates:` marked a guard-defined key the harness ignores; 10.2 adds a frontmatter load check to the dry-run programme |
| R2-6 | Low | Accepted | One story aligned across 4.1, 5.2, 5.7, 7.1: team-lead never merges or pushes `main`; a human performs the merge, always under founder approval |
| R2-7 | Low | Accepted | 7.2 routing line added: security-package failure fixes follow 7.5 (backend-lead fixes, security-lead reviews before landing) |
| R2-8 | Low | Accepted | 10.2 gains the prometheus-prompt-engineer dry-run row; mandate drift is the fail condition |

Counts: **8 accepted / 0 declined**.

### Table 3 — Independent review (Opus, 2026-09-25): D/Q/G/C/S — answered by interview Rounds 6–8 and the Opus-review lead defaults

| ID | Item (as recorded) | Disposition | Where handled / why |
|---|---|---|---|
| D1 | The founder's personal delegation shortcuts start unattended workers with permission prompts off and no agent override — they would inherit team-lead | Accepted | The fix lives where the failure lives: the founder-only personal layer must start every worker with an explicit agent override. The design's part is the 5.3 personal-layer paragraph plus the corrected "nothing launches Claude unattended" claim |
| D2 | Round 6 item, answered by the generic-orchestration ruling | Accepted | Orchestration is generic, the default workers are ordinary Claude Code subagents (Agent tool), command-line delegation is founder-only and outside the repo — section 1, section 3 reading rules, 5.3 |
| D3 | Only 4 of 11 security-sensitive packages were named; moot once security-lead is in the full gate and on demand, but the areas belong in the role's row | Accepted | Focus areas listed in the 4.1 security-lead row and 7.5 (verified against `pkg/`); gate membership per Round 6 |
| D4 | The three-sizes ruling | Answered (Round 6) — accepted | Three change sizes: small (one code-reviewer), standard (build with tests in the same step + the 3 fixed reviewers), feature (full RED/GREEN/CHECK + the 8-reviewer gate); urgent is a queue priority, not a size — 7.1 |
| D5 | One failure dispatch per red check at a time; a "who is on it" line | Accepted | 7.2 (the one-dispatch rule + status line); 4.1 team-lead row |
| Q1 | Team-lead stays repo-wide; the orchestrated workers are generic subagents | Answered (Round 6) — accepted | The project-wide `"agent": "team-lead"` setting stands; workers are ordinary subagents; command-line delegation stays a founder-only personal layer with the one override requirement — section 1, section 3, 5.3 |
| Q2 | Round 6 item — the sizes and/or landing rulings | Accepted | Round 6 rows 2–3, implemented in 7.1 (sizes) and 5.7 + 7.1 (landing, later refined by Round 15) |
| Q3 | Round 6 item — the sizes and/or landing rulings | Accepted | As Q2 |
| Q4 | Design-system delivery | Accepted | Round 7: omnipus-design-system preloaded into frontend-lead; UX skills on demand for frontend-lead and architect — 6.1, 8.1 checks 3 and 6 |
| Q5 | Round 7 ownership ruling (product agent text / user docs / security proof tests) | Accepted | All three implemented — 4.1 prometheus row + edges (product text), 7.4 + docs-verifier row (user docs), 7.5 (proof tests) |
| Q6 | Round 7 ownership ruling (as Q5) | Accepted | As Q5 |
| Q7 | Round 7 ownership ruling (as Q5) | Accepted | As Q5 |
| G1 | Unowned areas | Accepted | Round 8 split: contracts (architect shapes, backend-lead edits + regenerates), CI/deploy/Makefile (backend-lead), e2e (qa-lead); cross-stack order — 4.1 rows, ownership edges, 7.1 routing rule |
| G2 | Cross-stack order | Answered (Round 8) — accepted | Fixed order: contract first, then backend and frontend in parallel, then one combined review over the combined diff — 7.1 routing rule; 4.1 cross-stack edge |
| G3 | Urgent work | Accepted | Urgent = small or standard size, moved to the front of the queue — 7.1 sizes table |
| G4 | Bad-result handling | Accepted | Retry once with a sharper brief or a fresh instance; second failure escalates to the founder with the evidence; never redo silently — 5.5, 4.1 team-lead Must-never |
| G5 | Per-session opt-outs documented in CLAUDE.md | Accepted | Rollout stage 1 (`--agent ""`, a local settings entry); referenced from 5.3 |
| G6 | UX-skills delivery | Answered (Round 7) — accepted | `omnipus-design-system` preloaded into frontend-lead; the UX skills on demand for frontend-lead and architect — 6.1; 8.1 checks 3 and 6 |
| G7 | One rough effort line per size | Accepted | 7.1 sizes table, effort column |
| G8 | A dry run proving the `skills:` preload works for team-lead as the main session | Accepted | 10.2 team-lead dry-run row |
| — | Standard-size reviewers (recorded without its ID, Round 8) | Accepted | Fixed trio: code-reviewer, silent-failure-hunter, pr-test-analyzer; security-lead only in the full gate and on demand — 7.1 |
| C1 | The design claimed nothing launches Claude unattended; the founder's personal shortcuts do | Accepted | 5.3 residual-risk text corrected; the handling itself lives in the founder-only rule file |
| C2 | Default-prompt essentials for subagents | **Declined** | Founder: not needed — do not add them to the shared skill. They stay in team-lead.md only (5.1, 5.2); 6.2 states the exclusion explicitly |
| C3 | "Confirm first" scope | Accepted | Applies to pushes to shared branches; commits and pushes to one's own working branch need no confirmation — 5.2 |
| C4 | Vendored skills and the "Last reviewed" header | Accepted | Vendored skills exempt; `SOURCE.yaml` records their date — 8.1 check 8, section 9 |
| C5 | Appendix alignment with R4 | Accepted | The appendix row reads Inferred-yes |
| C6 | Superseded user-level test skills | Accepted | `elicify-test-writing` supersedes `test-plan-and-write` and `test-driven-development` for the RED step — 6.4 |
| S1 | The three-sizes ruling (as D4) | Answered (Round 6) — accepted | As D4 — 7.1 |
| S2 | RED worktree default | Accepted | One qa-lead instance in one worktree, separate test files, immediate commits; parallel per-area branches (each its own worktree) only for large epics — 7.1 RED step, 5.6 point 4 |
| S3 | Guard ship order (checks 1–6 and 10 first, 7–9 later, drift watch manual until then) | Accepted, then **superseded in part** | The phased ship died with the phased rollout: Round 16's all-at-once ruling means the guard ships complete (checks 1–13, drift watch in from the start) — 8.1 shipping note, 10.1 |
| S4 | Dry-run results in commit messages | Accepted | No central log file — 10.2 and rollout stage 2; supersedes the first review's L5 log-file mechanism; the governance trigger reads git history |

Counts: **30 accepted / 0 partly / 1 declined / 0 record-incomplete** — 31 rows: the 30 numbered IDs plus the unnumbered standard-reviewers row. The one decline is C2, declined by the founder ("not needed"). S3 is accepted-then-partly-superseded by a later founder ruling, noted in its row.

### Table 4 — Final independent review (2026-09-25): C1–C11, Q1–Q6, N1–N6, G1–G6, S1–S4

Provenance note: the final review's own text is not part of the repo record; `INTERVIEW.md` carries its resolutions — Rounds 15–16 answer its Q-items and one implementability/simplification pair each, and the "Lead defaults from the final review" block records the rest as grouped bullets (nine bullets covering the eleven contradictions). The Q/N/G/S rows below follow the interview's per-item record exactly. The C rows follow the nine recorded resolutions; where a bullet covers more than one contradiction, both rows cite it, and the per-ID split is this file's reconstruction from the contradictions the prior revision actually carried — flagged here so nobody reads more precision into the IDs than the record supports.

| ID | Item | Status | Where handled |
|---|---|---|---|
| C1 | "A red integration branch stops all landings" would also stop the fix that makes it green again | Accepted | Fix-only landings may land, under the lock like any landing — 5.7, 5.9 (red-stop paragraph), 7.2 |
| C2 | Ledger claims were written as binding ("a claim is respected until released") while everything else treats them as coordination information, and the hook enforces only holds and locks | Accepted | Claims are information only — 5.9; the enforced state is holds + landing lock (pre-push hook) |
| C3 | The qa-lead master-table row still said "several instances in parallel" against the one-instance RED default | Accepted | RED defaults to ONE qa-lead instance — 4.1 qa-lead row, 7.1 RED step, 5.6 point 4 |
| C4 | Parallel RED was phrased so lanes could share the feature branch / working copy | Accepted | Parallel RED always uses separate worktrees, one per-area branch each — 7.1 RED step |
| C5 | In-session squads were told to land themselves, but the founder's yes lives in team-lead's chat (and the prior 5.5 row said "team-lead alone lands") | Accepted (Round 15) | Hand-back: in-session squads hand back gated branches; team-lead asks and lands — 5.7, 5.9, 7.1, 7.6, 4.1 |
| C6 | prometheus "kept unchanged" while its file carries a `model:` frontmatter key — model choice must stay out of repo assets | Accepted | `model:` removed at rollout; guard check 13 bans the key in every repo agent file — 2.1, 8.1 check 13, rollout stage 1 |
| C7 | "The fifteen every-role rules" against sixteen numbered rules in 4.3 | Accepted | Rules renumbered 1–15; rule 12 is the pointer to the discipline block (whose traits carry the honest-reporting duty) — 4.3, 6.2 item 1 |
| C8 | Root CLAUDE.md still carried the outdated reviewer-count wording and security-lead-as-implementer, updated in a later rollout step than the skills | Accepted | CLAUDE.md updated **in the same rollout stage as the skills** — 6.5, rollout stage 1(d) |
| C9 | The first review's L5 disposition (dry-run log file) still stood as accepted against the no-central-log ruling | Accepted | L5 marked superseded — table 1 in this file; dry-run results live in commit messages (10.2) |
| C10 | The vacant-chief path (any squad lead records the succession) left no authority trail and no defined actor | Accepted | Vacancy marked in `CHIEF.md`; the founder is asked to name a successor — 5.9; R18 |
| C11 | The planning skill was mapped to team-lead.md alone while every squad-lead appointment needed it — a mode with no file carries no stable preload | Accepted (resolved via Round 16) | `squad-lead.md` preloads `omnipus-planning-orchestration`; check 6 maps it to team-lead **and** squad-lead — 4.4, 6.1, 8.1 check 6 |
| Q1 | In-session squads cannot land themselves (no chat of their own; the relayed yes is second-hand) | Accepted (Round 15) | Hand-back — 5.9, 5.7, 7.1, 7.6 |
| Q2 | Simultaneous landings can race on the integration branch | Accepted (Round 15) | The landing lock: first come, first served, released after the push — 5.9 |
| Q3 | Re-running the full size gate after the merge-latest step re-pays the whole gate for a merge | Accepted (Round 15) | Pre-landing re-check = CI for the affected areas + a conflict-resolution review — 5.7, 5.9 landing rule step 2 |
| Q4 | prometheus's `tools:` allow-list restricted a text author that needs no restriction | Accepted (Round 16) | Restriction removed; limits stated behaviourally in its prompt; it also owns the `pkg/tools` Description() text — 4.2, 4.1 |
| Q5 | Can an agent start the claude-security scan during waiting time? | Accepted (Round 16, conditional) | Rollout test decides; until it passes, security-lead recommends and the founder starts the scan — 7.5, 7.7, 10.2 |
| Q6 | code-simplifier's edits vs the review in flight | Accepted (Round 15) | It may edit the branch under review; its diff is covered by the remaining reviewers or a follow-up code-reviewer pass — 7.1, 4.1 edge |
| N1 | The capacity monitor's agent-process count is not reliably measurable | Accepted | The monitor counts active dispatches from the ledger; memory and disk are the hard signals; no process count — 5.6 |
| N2 | A squad-lead "mode" without a file has no stable prompt carrier | Accepted (Round 16) | `squad-lead.md`, the eleventh agent file — 3, 4.1, 4.4, 6.1 |
| N3 | Reviewer "re-run the narrow check" collides with the one-local-test machine rule | Accepted | Reviewers verify by reading code, CI results and artifacts; the single local narrow re-run at a time is team-lead's — 4.5 (reviewer discipline), 5.5 |
| N4 | The cross-session "messages" channel was unnamed and unenforced | Accepted | Mechanism named (the harness's session messaging, `MESSAGES.md` as durable record/fallback) + a git pre-push hook enforcing the ledger's holds and locks — 5.9 |
| N5 | The planning skill's mandatory load rested on preload alone | Accepted | The first body instruction of team-lead.md (and squad-lead.md) loads `omnipus-planning-orchestration` — 5.2, 6.1, 10.2 team-lead dry-run |
| N6 | "Tests shown red before green" implied local full-suite runs | Accepted | Allowed red evidence: CI on a tests-only commit, or the one narrow local run; small-size changes exempt — 4.5 (evidence table), 7.1 (sizes + RED step) |
| G1 | A capacity HOLD could sit silent forever | Accepted | A HOLD longer than 20 minutes fires an event update to the founder, who can override — 5.6, 10.2 monitor dry run |
| G2 | One ledger file for all squads is a write-contention point | Accepted | One ledger file per squad, plus an append-only landing log and a lock file — 5.9 (ledger directory) |
| G3 | "Stale" ledger rows were undefined ("timestamps are the signal") | Accepted | Stale = 2 hours with no update and no live session; the chief asks the founder before releasing — 5.9, R18 |
| G4 | Squad reports were unbounded, and ledger state was lost on context compaction | Accepted | Reports capped at ~40 lines plus the evidence table; the ledger re-read after compaction or resume — 5.9, 4.4, R20 |
| G5 | The evidence table had no self-check proof, and dry-run plants were undefined | Accepted | Mandatory self-check row; the planted false claim defined per dry run now — 4.5, 10.2 (every row) |
| G6 | Pending landing asks arrived one by one | Accepted | Batched into one event message; the founder answers per branch — 5.5, 5.7, 7.1; R29 records the latency trade-off |
| S1 | Pasting the shared-skill body into six reviewer dispatches per gate was token-heavy | Accepted | Plugin reviewers load `omnipus-shared-rules` with the Skill tool; only the discipline block (plus the load instruction) is pasted — 4.5, 6.6, 8.1 check 12 |
| S2 | Make the squad lead a real agent file rather than an appointment brief | Accepted (Round 16, with N2) | `squad-lead.md` — 4.4, 4.1, rollout stage 1 |
| S3 | Simplify the capacity monitor (with N1) | Accepted | Ledger-based dispatch count, hard memory/disk signals, no process count — 5.6 |
| S4 | Split the design from its decision record | Accepted | This companion file — the design body carries sections 1–11 + appendix with no revision history; the round mapping and all disposition tables live here |

Counts: **31 accepted / 0 declined** (11 C, 6 Q, 6 N, 6 G, 4 S). Q5 is conditional by its own terms (the rollout test decides the scan's starter); its fallback half is implemented unconditionally.

---

## 3. Maintenance

- A new founder round or review disposition appends a row to the relevant table above and updates the design body in the same change; the design file never grows revision-history narration.
- The C-row reconstruction note under table 4 stays until a copy of the final review's own text enters the repo record — if that happens, the per-ID rows can be checked against it and the note deleted.
- Discrepancies between this file and the design body are design-body wins on what the design *is*; this file wins on what was *decided and when* — and any such discrepancy is itself a finding to fix in one of the two files.
