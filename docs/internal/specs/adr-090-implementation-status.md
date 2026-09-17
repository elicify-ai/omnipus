# ADR-090 implementation and verification ledger

Status: in progress. Baseline: `12d97e7e70a869b72a246c9385776e71e9bdaa22`.

The founder authorized the agreed parallel implementation on 2026-09-17, using Sol workers, full code review, Prometheus review of every changed prompt/skill, correction of all findings, and green local tests. CI is explicitly deferred: do not push implementation branches, open implementation PRs or dispatch workflows during this phase.

## Scope and ownership

| Work package | Requirements | Status |
|---|---|---|
| Public contracts and generated clients | Configuration FR-002/003/007/012 | Parent implementing; targeted schema cases observed red then green before regeneration |
| Agent persistence and management | Configuration FR-001–005/007/012 agent portions | Sol worker implementing in isolated agents branch |
| Visual reader and private lifecycle | Visual FR-001–015 reader/lifecycle portions | Sol worker implementing in isolated visual branch; provider adapters follow |
| Roster, effective policies and global visibility | Configuration FR-001/008/009 | Sol worker implementing in isolated roles branch |
| Role prompts and packaged procedures | Configuration FR-009/013 | Roles worker; Prometheus review pending |
| Workspace graph, skill mutation and Ava routing | Configuration FR-006/007 | Pending next available worker |
| Connector assignment at discovery and dispatch | Configuration FR-003/004/005 | Pending integration owner |
| Settings and all configuration callers | Configuration FR-002/003/007 | Pending generated contract/backend seam |
| Provider image request matrix and candidate checks | Visual provider requirements and BDD-10/11/12 | Pending visual contract |
| Elicify document packages and actual runtime setup | Configuration FR-010/011; visual document acceptance | Pending baseline package integration |
| Local end-to-end verification | All BDD and acceptance requirements | Pending implementation |
| Full code review and seven reviewer lenses | Complete implementation diff | Pending; all findings must be corrected and rechecked |
| Prometheus review | All changed prompts, tool descriptions and skills | Definition located; review and corrections pending |

The requirements and BDD identifiers refer to the accepted companion specifications. This ledger does not replace them or mark omitted requirements complete. The advanced document-skill maturity/competitive campaign remains deferred under Elicify Skills #2 and Omnipus #730; baseline runtime document integration remains required here.

## Review sources and evidence discipline

Prometheus source: /Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/.claude/agents/prometheus-prompt-engineer.md. The named agent definition is used in spec-driven review mode, with Sol per the founder's model instruction. Skill review follows its skill-creator branch.

The installed `codex review` command supplies the full code-review pass; the repository's seven-reviewer gate adds correctness/conventions, simplification, comments, tests, silent failures, type design, and security. Reviewers run independently of the author of the reviewed slice. Fixes use separate ownership and are re-reviewed.

Record exact tree/commit, commands, collected tests, failures, mutation checks and runtime evidence before marking any row complete. A helper test, generated schema or prompt snapshot alone is not evidence that a user or agent can reach the feature. No CI result is claimed for this implementation.
