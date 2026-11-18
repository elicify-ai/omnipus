# #1857 failure kinds: pending wording (proposed, founder review pending)

Last reviewed: 2026-11-18

Status: **proposed, founder review pending** (chief ruling D1 A, founder decision E4 A).

This file lists every user-facing sentence the #1857 developer round (notifications spec §13.8,
WP-0 contract and the wording-independent behaviour) put into the LLMError catalogue. No sentence
here is new wording: each one is copied verbatim from the founder review page
(`/workspace/logs/1857/founder-examples.md`, rows W1 to W16), or is the existing generic `unknown`
sentence for a code that has no W row. Every row stays **proposed, founder review pending** until
the founder answers the E4 review; exact-copy tests are written only after that (the
`TODO(#1857 E4)` markers in the test files).

Where the catalogue holds the sentence: `contracts/components/schemas/LLMError.yaml` (and its three
hand-kept copies), generated into `pkg/api/generated/llm_error_messages.gen.go`
(`LLMErrorUserMessages`, `LLMErrorMessageVariants`) and `src/lib/api/generated/llm-error-messages.ts`.

Slots: `{cli}` is filled only from the closed display-name map (`claude-code`/`claude` to
"Claude Code", `codex` to "Codex", `opencode` to "OpenCode"); `{model}` is the model string Omnipus
passed; `{n}` and `{minutes}` are numbers. A sentence whose slot has no observed fact is replaced by
the generic `unknown` sentence, never shown with a literal slot.

## Rows

16 rows: 13 codes, 3 variants.

| # | Code (variant) | Source | Form used | Sentence | Tag |
|---|---|---|---|---|---|
| 1 | `external_cli_invalid_args` | W1 | chat | I couldn't start {cli}: it rejected its own command line, so nothing ran. Check the worker's Additional CLI arguments setting. | proposed, founder review pending |
| 2 | `external_cli_model_unavailable` | W2 | chat | I couldn't run the task in {cli}: it couldn't use the model {model}. Set this worker's model to one {cli} supports, in the agent's settings. | proposed, founder review pending |
| 3 | `external_cli_needs_signin` | W3 | chat (= neutral) | {cli} is not signed in on the machine it runs on, so the run stopped. Sign in to {cli} there, then retry the task. | proposed, founder review pending |
| 4 | `external_cli_failed` | W4 | chat | I couldn't finish the run in {cli}: it stopped with an error I couldn't identify. Retry the task; if it keeps happening, check that {cli} works on its own. | proposed, founder review pending |
| 5 | `external_cli_failed` (`stream_read`) | W6 | chat | I couldn't finish the run in {cli}: reading its output failed. Retry the task; if it keeps happening, check that {cli} works on its own. | proposed, founder review pending |
| 6 | `external_cli_failed` (`start`) | W7 | chat | I couldn't start {cli}: the program could not be launched. Check the CLI worker's program path in Settings. | proposed, founder review pending |
| 7 | `external_cli_turn_limit` | W5 | chat | I stopped the run in {cli}: it reached its limit of {n} turns for this task. Split the work into smaller steps, or raise the limit in the agent's settings. | proposed, founder review pending |
| 8 | `external_cli_upstream_refused` | W13 (limit form) | chat (= neutral) | The model {cli} uses is limiting requests right now. Try the task again in a moment. | proposed, founder review pending |
| 9 | `external_cli_upstream_refused` (`out_of_credit`) | W13 (out-of-credit form) | chat (= neutral) | {cli}'s model provider account is out of credit. Top up that account to run tasks with {cli}. | proposed, founder review pending |
| 10 | `agent_request_limit` | W11 | chat | I've reached my limit of model requests for this hour. I can continue in {minutes} minutes, or you can raise the limit in Settings. | proposed, founder review pending |
| 11 | `provider_error` | generic (W12 not wired) | existing `unknown` sentence | This turn didn’t finish, and we can’t tell why. Retry — if it keeps happening, open Verbose chat for details, or try a different model. | proposed, founder review pending |
| 12 | `tool_assembly_duplicate` | generic | existing `unknown` sentence | This turn didn’t finish, and we can’t tell why. Retry — if it keeps happening, open Verbose chat for details, or try a different model. | proposed, founder review pending |
| 13 | `transcript_save_failed` | generic | existing `unknown` sentence | This turn didn’t finish, and we can’t tell why. Retry — if it keeps happening, open Verbose chat for details, or try a different model. | proposed, founder review pending |
| 14 | `goal_state_unavailable` | generic | existing `unknown` sentence | This turn didn’t finish, and we can’t tell why. Retry — if it keeps happening, open Verbose chat for details, or try a different model. | proposed, founder review pending |
| 15 | `external_cli_not_found` (C8 program-path refusal) | W7 (reused) | chat | I couldn't start {cli}: the program could not be launched. Check the CLI worker's program path in Settings. | proposed, founder review pending. W7 reused by chief ruling Q1 A (2026-11-18); the founder may give these codes their own sentences. |
| 16 | `external_cli_invalid_config` (B16 config gap, B17b invalid turn cap) | W7 (reused) | chat | I couldn't start {cli}: the program could not be launched. Check the CLI worker's program path in Settings. | proposed, founder review pending. W7 reused by chief ruling Q1 A (2026-11-18); the founder may give these codes their own sentences. |

## Codes added by chief ruling Q1 A (2026-11-18)

`external_cli_not_found` and `external_cli_invalid_config` are attributed `config`, and neither has a W row. Under rule D1 A they would get the generic `unknown` sentence. The existing copy guard `TestUserMessages_OurFaultsDoNotSendUsersModelShopping` forbids that sentence ("Retry", "try a different model") on a config code, so the chief ruled Q1 A: both codes reuse W7 verbatim.

Two cases still fall back to the generic sentence:
- **No executor config.** The two B16 pre-checks ("missing executor config" and an empty `executor.cli`) refuse before any CLI is known, so `{cli}` has no fact.
- **CLI not in the display-name map.** The same fallback applies to any CLI outside the closed map.

The spec's "no-fact form drops the CLI clause" needs a sentence that does not exist yet.

## Open points for the founder review

- **Only the chat form is in the catalogue.** The neutral forms (task centre, parent tool result,
  Settings test) are not carried by the contract yet; those surfaces show the chat form for now.
  Carrying both voices needs a contract shape decision (architect).
- **The generic sentence is not accurate for `transcript_save_failed` and `goal_state_unavailable`.**
  "This turn didn't finish" is shown even when the turn finished (settlement) or when no turn ran
  (a background goal check). W15 is the founder's candidate for `transcript_save_failed`.
- **W12 is not wired.** `provider_error` uses the generic sentence; W12 needs `{provider}` and
  `{host}` facts that are a later work package.
- **W8, W9, W10, W14, W15, W16** belong to later work packages; nothing here uses them.
