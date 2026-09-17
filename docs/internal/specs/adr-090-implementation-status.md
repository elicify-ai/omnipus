# ADR-090 implementation and verification ledger

Status: in progress. Baseline: `12d97e7e70a869b72a246c9385776e71e9bdaa22`.

The founder authorized the agreed parallel implementation on 2026-09-17, using Sol workers, full code review, Prometheus review of every changed prompt/skill, correction of all findings, and green local tests. CI is explicitly deferred: do not push implementation branches, open implementation PRs or dispatch workflows during this phase.

## Scope and ownership

| Work package | Requirements | Status |
|---|---|---|
| Public contracts and generated clients | Configuration FR-002/003/007/012 | Agent/workspace/skill contracts committed; full generated API package green; TypeScript consumers still being updated |
| Agent persistence and management | Configuration FR-001–005/007/012 agent portions | Sol worker implementing in isolated agents branch |
| Visual reader and private lifecycle | Visual FR-001–015 reader/lifecycle portions | Reader/lifecycle first slice integrated; Sol implementing provider adapters and correcting parent review findings |
| Roster, effective policies and global visibility | Configuration FR-001/008/009 | First slice integrated; Sol correcting affected full-suite failures |
| Role prompts and packaged procedures | Configuration FR-009/013 | Roles worker; Prometheus review pending |
| Workspace graph, skill mutation and Ava routing | Configuration FR-006/007 | Workspace worker active; skill mutation checkout prepared for next available worker |
| Connector assignment at discovery and dispatch | Configuration FR-003/004/005 | Implemented locally; focused tests pass, four mutations caught and restored; integration/review pending |
| Settings and all configuration callers | Configuration FR-002/003/007 | Client save-state/revision wrappers tested; Settings Sol worker active |
| Provider image request matrix and candidate checks | Visual provider requirements and BDD-10/11/12 | Provider and transport commits integrated; full provider suites pass locally; live-model/document acceptance pending |
| Elicify document packages and actual runtime setup | Configuration FR-010/011; visual document acceptance | Four source-pinned packages imported; full skills package tests green, three package mutations caught; actual runtime setup and document acceptance pending |
| Local end-to-end verification | All BDD and acceptance requirements | Pending implementation |
| Full code review and seven reviewer lenses | Complete implementation diff | Pending; all findings must be corrected and rechecked |
| Prometheus review | All changed prompts, tool descriptions and skills | Definition located; review and corrections pending |

The requirements and BDD identifiers refer to the accepted companion specifications. This ledger does not replace them or mark omitted requirements complete. The advanced document-skill maturity/competitive campaign remains deferred under Elicify Skills #2 and Omnipus #730; baseline runtime document integration remains required here.

## Review sources and evidence discipline

Prometheus source: /Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/.claude/agents/prometheus-prompt-engineer.md. The named agent definition is used in spec-driven review mode, with Sol per the founder's model instruction. Skill review follows its skill-creator branch.

The installed `codex review` command supplies the full code-review pass; the repository's seven-reviewer gate adds correctness/conventions, simplification, comments, tests, silent failures, type design, and security. Reviewers run independently of the author of the reviewed slice. Fixes use separate ownership and are re-reviewed.

Record exact tree/commit, commands, collected tests, failures, mutation checks and runtime evidence before marking any row complete. A helper test, generated schema or prompt snapshot alone is not evidence that a user or agent can reach the feature. No CI result is claimed for this implementation.

## Integration evidence, 2026-09-17

- Shared contracts: local commits `54635e24e` and `7b9209472`; generated API package test exits zero. Agent and workspace contract mutation checks caught three deliberate defects each. Full generator is not green yet: TypeScript callers and fixtures still lack required revision fields.
- Visual slice integrated as `67da3a886`; roles slice as `2e7f18513`. These are implementation checkpoints, not completion claims. Parent review identified configured byte-budget, sniff accounting and actual resized-dimension issues; visual worker is correcting them with provider integration.
- Client save-state gate: real updateAgent HTTP wrapper tests cover complete/active, saved/inactive, partial/none, missing/malformed envelopes and conflict non-retry. Six original cases failed before implementation; 16 current cases pass. An initial persistence mutation survived because tests lacked contradictory partial+active state; those cases were added and all three mutation categories were caught on rerun. Final restored check and broader caller integration remain separate gates.
- Elicify package inventory pins source `f5c241825045341b9b71a9c1f369c24ba6de4f9c` and every copied asset hash. Source bytes are unchanged apart from added license notices. Public distribution terms remain unresolved; nothing has been pushed or published. Worker runtime readiness is explicitly pending in the inventory.

- Agent management checkpoint integrated as `2e50d5e44`; its worker is finishing create staging/aliases, storage envelopes and affected legacy tests. This checkpoint is not a full-suite pass.
- Document package seeding: full `pkg/skills` suite passed after adding the four approved names to the exact roster; three asset changes (helper, knowledge file, license) were detected against pinned hashes, restored, and the focused seeding check passed again. No agent execution/runtime readiness is inferred from this package check.

- Client mutation transport integrated as `51b6f9a81`: delete helpers require the reviewed revision; graph updates take the generated request; tool updates carry override intent. Agent/workspace creation and updates, graph/tools updates and skill installation all check persistence plus activation before resolving successfully. The two real-fetch test files pass 33 cases; three transport mutations were caught and restored; changed client files pass ESLint. Full frontend typecheck remains red until Settings callers and fixtures are migrated.
- Next Settings worker checkout prepared at /Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/adr090-settings. The Sol Settings worker is now running there.

- Ready for integration: agent mutation completion `7997df189`, role-suite repair `1a11ef3e4`, visual provider integration `513e54663`, transport capture tests `4a2775dd5`. These remain worker-branch evidence until integrated and checked together. Role worker reports full local coreagent/tools/config/skills suites green; visual worker reports scoped providers, Bedrock tagged transport, and isolated owner-wake tests green.
- Workspace worker now owns /Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/adr090-workspaces; document runtime worker owns /Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/adr090-documents. Runtime prefix/sandbox/probe implementation is underway; actual worker execution and four-format acceptance remain unproven.
- Connector tests reproduced unassigned access, stale retained-agent authority after unbinding, and unbinding during policy approval followed by dispatch. Assignment filtering, immutable assignment snapshots, current-registry lookup and a final pre-dispatch check now pass the focused local agent/tools tests. Wildcard-only assigned-server selection is preserved. All four deliberate mutations failed through the expected assertions (assignment filter, snapshot copying, current-registry lookup, post-approval dispatch guard); production was restored and the focused tests passed again.

- Integrated checkpoints now include connector enforcement `c43d43b59`, atomic agent completion `a9ae507fe`, role repairs `8e42374ed`, provider integration `e24691a96`, and transport captures `9ac64c19c`. Combined local tests passed for coreagent, agentmutation, agentstore, config, skills, every default-build provider package and generated API. The tools suite initially found three stale fixtures: explicit connector assignment in a precedence test, the approved 33→35 management catalog count, and the two new readback-tool snapshot entries. After those corrections, the full tools suite passed (113.742 seconds). This does not claim tagged-provider or full-feature end-to-end completion.
- Ava skill authoring/removal guard integrated as `09f2431e4`. Twelve real-file cases cover attended success and zero-write rejection for delegated, unattended and missing-session Ava across create/edit/remove. Each guard was independently removed, caught by the assertions and restored; affected skill-tool tests passed again. Skill revision/replace/install and publication parity remain pending.
