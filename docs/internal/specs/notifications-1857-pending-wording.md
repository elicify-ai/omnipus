# #1857 failure kinds: wording (founder-approved 2026-11-18)

Last reviewed: 2026-11-18

Status: **founder-approved 2026-11-18** (chief ruling D1 A, founder decision E4 A). The founder
approved all 21 W rows in #team-b ("the messages look good, go ahead", 11:02Z) and chose W11 A
(11:06Z): the W11 sentence stays as written and the Settings rate-limit field is repaired in the
same work package (#1882). The wording gate is closed. The frontend-round build wires W17 to W21
(rows 12 to 14, 17 and 18) and adds the neutral form of every first-person row.

This file lists every user-facing sentence the #1857 developer round (notifications spec §13.8,
WP-0 contract and the wording-independent behaviour) put into the LLMError catalogue. No sentence
here is new wording: each one is copied verbatim from the founder review page
(`/workspace/logs/1857/founder-examples.md`, rows W1 to W16), or is the existing generic `unknown`
sentence for a code that has no W row. The rows below were **proposed, founder review pending** until
the founder answered the E4 review (approved 2026-11-18); exact-copy tests follow from now on (the
`TODO(#1857 E4)` markers in the test files).

Where the catalogue holds the sentence: `contracts/components/schemas/LLMError.yaml` (and its three
hand-kept copies), generated into `pkg/api/generated/llm_error_messages.gen.go`
(`LLMErrorUserMessages`, `LLMErrorMessageVariants`) and `src/lib/api/generated/llm-error-messages.ts`.

Slots: `{cli}` is filled only from the closed display-name map (`claude-code`/`claude` to
"Claude Code", `codex` to "Codex", `opencode` to "OpenCode"); `{model}` is the model string Omnipus
passed; `{n}` and `{minutes}` are numbers. A sentence whose slot has no observed fact is replaced by
the generic `unknown` sentence, never shown with a literal slot.

## Rows

18 rows: 13 codes, 5 variants.

| # | Code (variant) | Source | Forms | Chat sentence | Neutral sentence | Tag |
|---|---|---|---|---|---|---|
| 1 | `external_cli_invalid_args` | W1 | chat + neutral | I couldn't start {cli}: it rejected its own command line, so nothing ran. Check the worker's Additional CLI arguments setting. | {cli} rejected its own command line, so nothing ran. Check the worker's Additional CLI arguments setting. | founder-approved 2026-11-18 |
| 2 | `external_cli_model_unavailable` | W2 | chat + neutral | I couldn't run the task in {cli}: it couldn't use the model {model}. Set this worker's model to one {cli} supports, in the agent's settings. | The run in {cli} could not use the model {model}. Set this worker's model to one {cli} supports, in the agent's settings. | founder-approved 2026-11-18 |
| 3 | `external_cli_needs_signin` | W3 | chat (= neutral) | {cli} is not signed in on the machine it runs on, so the run stopped. Sign in to {cli} there, then retry the task. | (same as chat) | founder-approved 2026-11-18 |
| 4 | `external_cli_failed` | W4 | chat + neutral | I couldn't finish the run in {cli}: it stopped with an error I couldn't identify. Retry the task; if it keeps happening, check that {cli} works on its own. | The run in {cli} stopped with an error that could not be identified. Retry the task; if it keeps happening, check that {cli} works on its own. | founder-approved 2026-11-18 |
| 5 | `external_cli_failed` (`stream_read`) | W6 | chat + neutral | I couldn't finish the run in {cli}: reading its output failed. Retry the task; if it keeps happening, check that {cli} works on its own. | The run in {cli} ended because reading its output failed. Retry the task; if it keeps happening, check that {cli} works on its own. | founder-approved 2026-11-18 |
| 6 | `external_cli_failed` (`start`) | W7 | chat + neutral | I couldn't start {cli}: the program could not be launched. Check the CLI worker's program path in Settings. | {cli} could not be launched. Check the CLI worker's program path in Settings. | founder-approved 2026-11-18 |
| 7 | `external_cli_turn_limit` | W5 | chat + neutral | I stopped the run in {cli}: it reached its limit of {n} turns for this task. Split the work into smaller steps, or raise the limit in the agent's settings. | The run in {cli} reached its limit of {n} turns for this task. Split the work into smaller steps, or raise the limit in the agent's settings. | founder-approved 2026-11-18 |
| 8 | `external_cli_upstream_refused` | W13 (limit form) | chat (= neutral) | The model {cli} uses is limiting requests right now. Try the task again in a moment. | (same as chat) | founder-approved 2026-11-18 |
| 9 | `external_cli_upstream_refused` (`out_of_credit`) | W13 (out-of-credit form) | chat (= neutral) | {cli}'s model provider account is out of credit. Top up that account to run tasks with {cli}. | (same as chat) | founder-approved 2026-11-18 |
| 10 | `agent_request_limit` | W11 | chat + neutral | I've reached my limit of model requests for this hour. I can continue in {minutes} minutes, or you can raise the limit in Settings. | This agent reached its limit of model requests for this hour. It can continue in {minutes} minutes, or the limit can be raised in Settings. | founder-approved 2026-11-18 |
| 11 | `provider_error` | generic (W12 not wired) | existing `unknown` sentence (= neutral) | This turn didn’t finish, and we can’t tell why. Retry — if it keeps happening, open Verbose chat for details, or try a different model. | (same as chat) | founder-approved 2026-11-18 |
| 12 | `tool_assembly_duplicate` (no MCP tool involved; the fail-closed class) | W21 | chat + neutral | I couldn't get my tools ready because two of them have the same name, so this turn didn't run. We don't know why this happened; if it keeps happening, please report it to the Omnipus.ai team. | The agent's tools could not be made ready because two of them have the same name, so the turn didn't run. Why this happened is not known; if it keeps happening, please report it to the Omnipus.ai team. | founder-approved 2026-11-18 |
| 13 | `transcript_save_failed` | W17 | chat + neutral | This turn finished, but I couldn't save its final state. Recent messages may be missing from your history; check the answer above before repeating anything. | The run finished, but its final state could not be saved. Recent messages may be missing from the history; check the result before repeating any part of the task. | founder-approved 2026-11-18 |
| 14 | `goal_state_unavailable` | W18 | chat + neutral | I couldn't read the goal for this conversation, so I didn't continue working on it. Try again in a moment. | The goal for this conversation could not be read, so the work did not continue. Try again in a moment. | founder-approved 2026-11-18 |
| 15 | `external_cli_not_found` (C8 program-path refusal) | W7 (reused, chief ruling Q1 A) | chat + neutral | I couldn't start {cli}: the program could not be launched. Check the CLI worker's program path in Settings. | {cli} could not be launched. Check the CLI worker's program path in Settings. | founder-approved 2026-11-18 |
| 16 | `external_cli_invalid_config` (B16 config gap with a known program, B17b invalid turn cap) | W7 (reused, chief ruling Q1 A) | chat + neutral | I couldn't start {cli}: the program could not be launched. Check the CLI worker's program path in Settings. | {cli} could not be launched. Check the CLI worker's program path in Settings. | founder-approved 2026-11-18 |
| 17 | `tool_assembly_duplicate` (`mcp`: an MCP tool among the colliding pair) | W20 | chat + neutral | I couldn't get my tools ready: two tools have the same name, and one comes from a connected MCP server. Check this agent's tools and MCP servers. | The agent's tools could not be made ready: two tools have the same name, and one comes from a connected MCP server. Check the agent's tools and MCP servers. | founder-approved 2026-11-18 |
| 18 | `external_cli_invalid_config` (`no_program`: B16 pre-checks, no program known yet) | W19 | chat + neutral | I couldn't start the coding program: its settings for this agent are missing or invalid. Check the agent's coding program settings. | The coding program could not start: its settings for this agent are missing or invalid. Check the agent's coding program settings. | founder-approved 2026-11-18 |

## Codes added by chief ruling Q1 A (2026-11-18)

`external_cli_not_found` and `external_cli_invalid_config` are attributed `config`, and neither has a W row. Under rule D1 A they would get the generic `unknown` sentence. The existing copy guard `TestUserMessages_OurFaultsDoNotSendUsersModelShopping` forbids that sentence ("Retry", "try a different model") on a config code, so the chief ruled Q1 A: both codes reuse W7 verbatim.

Frontend round (2026-11-18): the two B16 pre-checks ("missing executor config" and an empty
`executor.cli`) refuse before any CLI is known; they now use the `no_program` variant (W19), which
has no `{cli}` slot. One case still falls back to the generic sentence:
- **CLI not in the display-name map.** A configured CLI outside the closed map leaves `{cli}` without
  a fact (W7 cannot be filled); W19 was assigned only to the pre-checks.

## Open points for the founder review

- **Both voices are in the catalogue (frontend round, design note D-B B1).** `neutral` and
  `neutral_variants` carry the neutral form (task centre, parent tool result, completion message,
  Settings test); a chat form with no first person serves both voices.
- **W17 to W21 are wired (frontend round).** W17 `transcript_save_failed`, W18
  `goal_state_unavailable`, W19 `external_cli_invalid_config` (`no_program`), W20/W21
  `tool_assembly_duplicate` (`mcp` / the code sentence).
- **W12 is not wired.** `provider_error` uses the generic sentence; W12 needs `{provider}` and
  `{host}` facts that are a later work package.
- **W8, W9, W10, W15, W16** belong to later work packages; nothing here uses them.
- **W14 form 1 is wired (OpenCode fixes, 2026-11-23).** `delegation_not_resumable` carries the
  resume-unavailable chat and neutral sentences and the no-fact chat form (variant `no_cli`). Its
  neutral no-fact form (`neutral_variants.no_cli`, "This delegation could not continue: its previous
  conversation can't be resumed. Start a new delegation.") applies W14's clause drop to the neutral
  sentence and is approved (Prometheus, 2026-11-23). W14 form 2 (workspace changed,
  B16b) is not wired.

## Known limits (chief, 2026-11-18; not lost, revisited after the founder's W answer)

- **`agent_request_limit` is retryable (founder chose W11 A, 2026-11-18).** It follows W11 ("I can continue in {minutes} minutes").
- **Live rate-limit frame carries no code.** `RateLimitFrame` has no code field, so the live frame does not say `agent_request_limit`; a contract change is needed. The error frame and the saved chat record carry the code. An issue is filed only if the founder's W11 answer makes the live frame matter.
- **F4 — B3 saved record text (closed by the #1857 fix round, X1 fix A).** The saved record of an LLM-call limit denial now holds the W11 sentence with `{minutes}` filled (the limiter's wait rounded up to whole minutes), under code `agent_request_limit` — the same sentence the live reply shows; `loop.go::recordRateLimitDenial` and the live reply build it from one helper (`cli_failure_kinds.go::agentRequestLimitLLMError`). Pinned by `TestRecordRateLimitDenial_LLMCall_SavesFilledW11Sentence_1857`. Only a denial with no wait (0 s) still saves the generic sentence (no fact for the slot). A reload is therefore expected to show W11 too; that is not yet confirmed hands-on. Tool-call denials keep their old text. Replaces the earlier reload limit (chief, 2026-11-18: after a reload the generic sentence showed).
- **F6 — W7 reused for an invalid turn cap.** "The program could not be launched ... program path" is misleading for B17b (chief ruling Q1 A). Frontend round: the missing executor config (B16 pre-checks, no program known) now uses W19 (`no_program`); B17b and a driver-construction failure with a known program keep W7.
- **F8 — unchecked `fmt.Sscan`** for the turn cap in `cli_failure_kinds.go::classifyCLIFailure`. The regex guarantees digits; worst case a 0 that falls back to the generic sentence.
- **F9 — C8 on task surfaces** still shows the curated refusal text (Omnipus's own text, existing r17 behaviour) rather than the catalogue sentence; a slight break of §13.8.4 rule 1.
- **W17–W21 (approved 2026-11-18) are wired in the frontend round:** W17 `transcript_save_failed`, W18 `goal_state_unavailable`, W19 `external_cli_invalid_config` (`no_program`), W20/W21 the two `tool_assembly_duplicate` forms (`mcp` / the code sentence). See the rows above.
