# WU-9 draft — Omnipus prompt text (first draft, not wired)

Work unit: spec §15 **WU-9** — "Workflow skills (W15 mailbox text lands with WU-18), core prompt, routing table, `AskUserQuestion` text" (landing 1; owner prometheus, backend-lead wires). Sources: `docs/internal/specs/omnipus-system-agent-spec.md` (§15, §10 FRs, §8 BDD), `docs/internal/architecture/ADR-20261011-omnipus-system-agent.md` (OSA-1–OSA-11), binding amendment `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/L1-AMENDMENTS-20261013.md` (§1.2 R3: **W1/W18 are chat-only**; R6: approvals inline in the root chat), flow authority `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/omnipus-workflows/index.html` (WF), issue **#1061** (provider-config skill, folded in).

## Why these drafts sit under docs/ and not in pkg/

Placing them where the text will finally live would change runtime behaviour or require Go edits — both out of this unit's scope (brief: "do not wire them into Go code"):

| Draft | Eventual home | Why not now |
|---|---|---|
| `skills/*.md` | `pkg/skills/embedded/<name>/SKILL.md` | `pkg/skills/embed.go` — `//go:embed all:embedded` plus `SeedDefaults`: a new folder ships in the binary and is seeded into `~/.omnipus/skills` on next boot. That is a runtime change, and the grants (FR-010, Omnipus-only) are WU-2's seeding work. |
| `core-prompt.md` | a Go string constant in `pkg/coreagent/prompts_adr090.go` (replacing the `"ava"` / `"admin"` entries) | prompt text lives in Go string literals here; writing it is Go wiring. |
| `askuser-question-description.md` | `pkg/tools/ask_user_question.go::AskUserQuestionTool.Description()` | Go method text. |

So per the brief's escape hatch, the whole draft lives in `docs/internal/specs/omnipus-wu9-draft/`. Nothing in `pkg/` was touched; the current Ava/Admin text is untouched (WU-2/WU-11 remove it).

## Files and per-workflow coverage

| W-row / item | File | FRs / sources covered |
|---|---|---|
| Core prompt + routing table | `core-prompt.md` | FR-010, FR-011, FR-012, FR-013, FR-026, FR-027, FR-030, FR-033, FR-040, FR-053, FR-095; FR-015 note text; amendment R3 (chat-only W1/W18); ADR OSA-2/3/6/8/9.1/11 |
| `AskUserQuestion` text | `askuser-question-description.md` | FR-014 (one plan-confirmation use; Go never `default_safe`); God Mode note carrier for FR-015 |
| W1 create/add agent | `skills/create-agent.md` | FR-045, FR-046, FR-041, FR-042, FR-043, FR-053; BDD-4.1–4.8; WF W1 |
| W3 change agent | `skills/change-agent.md` | FR-024 (permission tool split), FR-033; ADR OSA-3 W3 rows |
| W4 remove/delete agent | `skills/remove-agent.md` | FR-243, FR-077, §6.19 lifecycle; BDD-3.6 |
| W5 write/edit skill | `skills/write-skill.md` | FR-042 (target shelf); OSA-4 activation check |
| W6 install skill | `skills/install-skill.md` | FR-053; ADR OSA-9 Dependencies (E2) |
| W7 team & delegation | `skills/team-delegation.md` | FR-032, FR-085 (revision); WF W7 |
| W8 welcome | `skills/welcome-workspace.md` | FR-047, FR-048, FR-052; BDD-5.1, 5.7 |
| W10 add connector | `skills/add-connector.md` | FR-070 (references only), FR-053; OSA-4 `add_mcp_server` check; WF W10 |
| W11 remove connector | `skills/remove-connector.md` | ADR OSA-3 ask list; WF W11 |
| W12 add/change provider | `skills/add-provider.md` | FR-031, FR-070; BDD-9.1–9.3, 9.7; **#1061** |
| W13 connect channel | `skills/connect-channel.md` | FR-080, FR-081 (QR in chat), FR-121/FR-127 (binding); BDD-8.1–8.5, 8.7, 8.8 |
| W14 disable channel | `skills/disable-channel.md` | ADR OSA-3; WF W14 |
| W16 doctor & usage | `skills/doctor-usage.md` | ADR OSA-9 Dependencies (E5); WF W16 |
| W17 shell diagnosis | `skills/shell-diagnosis.md` | ADR OSA-1 (bash: ask, diagnosis only); WF W17 |
| W18 team setup | `skills/team-setup.md` | FR-048–FR-051, FR-047, FR-053; FA:FQ-14 A (reuse unchanged / separate proposal); BDD-5.3–5.6; amendment R3 |
| Capability planning | `skills/capability-plan.md` | FR-010 ("capability planning"); WF build #4 |
| Dependency checks | `skills/dependency-check.md` | FR-053, FR-040; ADR OSA-9 Dependencies; WF §2a |
| #1061 provider-config | `skills/provider-config.md` | #1061 asks 1–3 (skill + tool reference + description line); FR-031 |
| W15 mailbox | **absent on purpose** | lands with WU-18 (landing 2), per mandate |

## Names this draft assumes

Every name below is used in the text with the exact spelling the spec gives. "Exists today" is grep over production `pkg/**/*.go` at this baseline (`8754a1a4a`), excluding tests.

| Name | Spec section | Exists today | Evidence |
|---|---|---|---|
| `request_secret` | §6.5, FR-060 | no | grep ABSENT (all pkg, non-test) |
| `list_secrets` | §6.5, FR-073 | no | grep ABSENT |
| `remove_secret` | §6.5, FR-077 | no | grep ABSENT |
| `select_secret_version` | §6.5, FR-086 | no | grep ABSENT |
| `connect_channel` | FR-080, WU-7 | no | grep ABSENT |
| `set_agent_permissions` | FR-024, FR-021 | no | grep ABSENT |
| `workflow_start` (MessageFrame field) | K8, FR-012 | no | grep ABSENT |
| `delete_everything` (delete_agent param) | K50, FR-243 | no | grep ABSENT |
| `expected_revision` (shared-resource token) | FR-085, FR-088 | no (WU-3; `revision` on workspace/agent exists) | grep ABSENT |
| `environment_setup` validated target parameter | FR-040 (WU-3) | tool exists; target param lands with WU-3 | tool in `pkg/coreagent/seed.go` static list |
| `GodModeActive` | ADR OSA-3, FR-015 | **yes** | `pkg/agent/instance.go::GodModeActive` |
| `Skill`, `ToolSearch`, `AskUserQuestion` | §6.5 | yes | `pkg/tools/skill.go::SkillTool.Name`, `pkg/tools/tools_tool.go::ToolsTool.Name`, `pkg/tools/ask_user_question.go::AskUserQuestionToolName` |
| `create_agent`, `update_agent`, `delete_agent`, `get_agent`, `get_agent_tools`, `read_agent_metadata`, `list_agents`, `list_workspaces`, `get_workspace`, `update_workspace`, `create_skill`, `edit_skill`, `remove_skill`, `install_skill`, `find_skills`, `list_skills`, `list_mcp_servers`, `add_mcp_server`, `remove_mcp_server`, `configure_provider`, `list_providers`, `test_provider`, `list_models`, `list_channels`, `enable_channel`, `disable_channel`, `configure_channel`, `test_channel`, `environment_setup`, `bash`, `run_doctor`, `get_usage`, `delegate`, `message_parent`, `grep`, `read_file`, `list_directory` | §6.5 | yes | `pkg/coreagent/seed.go` static name list (grep-verified 2026-10-13) |
| `connect_mailbox`, `list_mailboxes`, `read_conversation` | FR-082 / FR-088 / FR-192 | no — **landing 2**; deliberately not used in this draft | grep ABSENT |

## Replacement map (old embedded skill → this draft)

The Ava/Admin-era embedded skills stay untouched until WU-2 (grants) and WU-11 (removals) swap them:

| Old (`pkg/skills/embedded/`) | New draft |
|---|---|
| `agent-authoring` | `create-agent`, `change-agent`, `remove-agent` |
| `workspace-team` | `team-setup`, `team-delegation` |
| `skill-authoring` | `write-skill` |
| `skill-mapping` / `tool-mapping` | `capability-plan` |
| `mcp-install` | `add-connector`, `remove-connector` |
| `provider-setup` | `add-provider`, `provider-config` |
| `channel-setup` | `connect-channel`, `disable-channel` |
| `doctor` | `doctor-usage`, `shell-diagnosis` |
| `interview` | **kept** (shared with Mia/Jim; workflow skills interview in their own steps) |
| `delegation-graph` | folded into `team-delegation` / `team-setup` |
| `plan`, `orchestrate`, `define-goal`, `deep-research`, `inbox-triage`, `elicify-*`, `verify` | not Omnipus workflow skills — out of WU-9 scope |

## Wiring checklist for backend-lead (after WU-7, per plan §2)

1. Copy each `skills/<slug>.md` to `pkg/skills/embedded/<slug>/SKILL.md` — the directory name must equal the frontmatter `name:` (`pkg/skills/CLAUDE.md`; the draft filenames already are the slugs). Add the 18 names to Omnipus's role skill grants (WU-2's inventory) and remove the replaced set per the map above (with WU-11).
2. Replace the `"ava"` / `"admin"` prompt constants with the core-prompt text (one constant); inject the God Mode note per turn from `GodModeActive(cfg)` — FR-015's carrier is per-turn context, not the static prompt.
3. Apply the two `Description()` clause edits in `pkg/tools/ask_user_question.go` (this draft's `askuser-question-description.md`), plus #1061's one-line limitation note and skill reference in `configure_provider`'s description (backend-lead's text edit; the wording proposal is in `skills/provider-config.md` §Handoff).
4. Landing-2 amendments this draft already anticipates: append `connect_mailbox` to the core prompt's ask list; extend `connect-channel` with FR-150's person-row offer when WU-14 lands.
5. Wording tests that pin this text: `pkg/coreagent::TestOmnipusPrompt_GateRulesAndRoutingTable` and `::TestOmnipusPrompt_DependencyAndRoutingRules` (spec tests 51/83, wording only); live thresholds §9.6 (Go question 5/5, no change tool on unrelated question 5/5, planted-instruction inertness 5/5, routing ≥4/5).

## Open questions (for team-lead; none blocking the draft)

| # | Question | Recommendation |
|---|---|---|
| 1 | Skill ids: spec BDD-2.5 names skills `change-agent` / `add-connector`; ids are stable once shipped (`pkg/skills/CLAUDE.md`). | Keep these semantic ids (BDD precedent) — decide before WU-9 wiring. |
| 2 | #1061 states `test_provider` is a presence check (zero network calls). This draft repeats it; WU-3 reworks provider checks (FR-031). | Verify `test_provider`'s behaviour in WU-3 and correct the one line in `provider-config.md` if it changed. |
| 3 | Should Omnipus load the existing `interview` skill for W1/W18 interviews, or interview in the workflow's own Ask steps as drafted? | Keep as drafted (workflow-owned steps); `interview` stays for other agents. |
| 4 | `w17-shell-diagnosis` says "never through shell edits" for configuration — confirm no counter-example is wanted for doctor-style repairs. | Keep: configuration has change tools; shell stays diagnosis-only (ADR OSA-1). |
