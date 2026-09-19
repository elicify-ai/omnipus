# Independent Opus closure review

> Decision update (2026-09-18): the founder superseded M-3's owner-session-only/proposal-only resolution. Ava may apply user-confirmed changes in a delegated run, with the user’s answer relayed through Jim. There is no Ava-specific session-type write gate. The current ADR and configuration specification govern; the original review below is retained as historical evidence.

Executed through Claude Code with `--model opus`, high effort and read-only tools; actual primary model `claude-opus-5`, 17 turns, 183,811 ms, successful completion and no permission denials. No runtime tests. Original output follows unchanged. Its opening count of “11 critical and major” is an arithmetic error: C-1/C-2 plus M-1–M-11 totals 13. All eight minor follow-up findings were subsequently corrected; see the [dispositions](ADR-090-built-in-agents-skills-and-visual-reading-opus-dispositions.md).

---

# ADR-090 closure review (follow-up to the Opus review)

I read the six documents you listed and checked five claims against the source. I wrote no files and ran no shell commands or tests. The skill normally writes a review file; I didn't, because you said not to.

**Summary:** All 11 critical and major findings (C-1, C-2, M-1 to M-11) are closed. Three of the original findings (m-8, m-9 and M-10) have small minor leftovers. I also found 8 new minor defects and no new critical or major ones. **Engineering verdict: PASS.** L1 (the document-skill license) is still **OPEN**.

## 1. Original findings

| ID | Status | Evidence |
|---|---|---|
| C-1 | Closed | ADR §3 limits helpers to `jim` and `worker` through an explicit self-edge, with mode and depth caps. Only those self-edges skip cycle detection. Config FR-006 requires the gateway validator and `buildDelegationDenyChecker` to change together. Tests: BDD-15, D18, D25, D32, I4, plus the self-edge regression tests. (There's a small trace gap; see N-4.) |
| C-2 | Closed | ADR §5.0 and config FR-003 enforce the binding when tools are offered and again right before execution, including tool definitions that were already loaded. An absent binding means no access. Omitted `tools` means all of that server's tools and `[]` means none. Tests: BDD-20, D26, I1, SC-010. (One stale ADR sentence remains; see N-1.) |
| M-1 | Closed | ADR §2.0 and config FR-001 give matching identity tables: Admin is `core` and chat-able, General Purpose is `worker`, and Ray, Max and Explorer aren't seeded. Tests: D31, U1. |
| M-2 | Closed | FR-008 changes the fresh global `add_mcp_server` ceiling to Allow. Admin ships with Allow; every other built-in and every new custom agent ships with an explicit Deny. Operator Ask/Deny is kept. The supersession is recorded in ADR §14. Tests: BDD-23, D29, I3. This matches your confirmed choice. |
| M-3 | Closed | ADR §3 and FR-007 route Ava through `switch_agent`. A delegated Ava returns a proposal and makes zero writes, and parent approval doesn't count as user confirmation. Tests: BDD-21, D27, E1. |
| M-4 | Closed | FR-007 requires one opaque `revision` on every mutation, and sending `updated_at` gets a 400. It also requires one lock covering the entity and soul (instruction) file, staging both files before replacing either, entity replaced first, and an explicit partial-save result. Tests: BDD-22, D28, I2. |
| M-5 | Closed | Ordinary agents store sparse overrides. Every dash in the ADR matrix means an effective Deny, with the whole catalog classified. The dedicated tools PUT takes an `override_names` list. Tests: D33, U1. (One race remains; see N-8.) |
| M-6 | Closed | The Judge's evidence scope is the reviewed workspace: ADR §2.0 and §2.4, config FR-009, and visual FR-004 and row B10. Its exact tool list matches in the ADR and FR-009. ADR §14 records that this supersedes JUDGE-FR-059. |
| M-7 | Closed | Plan Supervisor now has `define-goal` in ADR §6.1, FR-009 and §14. |
| M-8 | Closed | FR-013 gives all 15 role skills an embedded path, required sections, a real-workflow acceptance row, and lint for tool references and permissions. Tests: BDD-24, D30, U2, E2. |
| M-9 | Closed (package part depends on L1) | ADR §14 records the exception and root CLAUDE.md/AGENTS.md line 66 annotates it. FR-011 defines the shared install location, worker read and execute access, sandbox allow-paths, PATH handling and the probe command. It honestly says install commands can't be certified until L1 is decided. |
| M-10 | Closed, with a minor leftover (N-7) | Visual FR-005 and FR-012, design items 3 and 8, and BDD-15/B14 keep image bytes only for the live turn. Durable history gets a text marker saying "not retained; re-read to view". |
| M-11 | Closed | BDD-20 to BDD-25, datasets D26 to D33, and tests U1, U2, I1 to I4, E1 and E2 were added. |
| m-1 | Closed | FR-002 sets the status codes: 403 for protected fields, 400 for malformed or unknown input, and existing 422s stay. |
| m-2 | Closed | ADR §5 and FR-008 add explicit Ask overrides for `send_email` and `reply`, and exclude `send_message`, `message_parent` and handback. |
| m-3 | Closed | FR-002 classifies `memory_enabled`, `default`, `voice`, `executor`, heartbeat, `timeout_seconds` and `rate_limits`. |
| m-4 | Closed | FR-007 and ADR §5 make fresh global and Ava defaults Allow for `delete_agent`, `install_skill` and `remove_skill`, with Deny for other roles. The source confirms the current values: `delete_agent` and `remove_skill` are Ask; `create_*`, `update_*` and `edit_skill` are Allow. |
| m-5 | Closed | FR-008 says the preview tier becomes `serve_web` only, and the uncompressed mode is defined. The two pinned visibility tests are in the regression list. |
| m-6 | Closed | CLAUDE.md and AGENTS.md line 66 carry the planned-change note. FR-008 requires updating the MCP-install explanation when the change ships. |
| m-7 | Closed | The visual spec lists every adapter: five native serializer families including Bedrock, and the Codex and Copilot CLIs treated as unable to carry images. `ClaudeProvider` is correctly classed as the Anthropic SDK adapter; the source imports `anthropic-sdk-go`. |
| m-8 | Closed, with a minor leftover (N-6) | Design item 2 and the constraints accept regular files only. |
| m-9 | Closed, with a minor leftover (N-3) | FR-007 defines the REST response fields for persistence and activation status. |
| m-10 | Closed | FR-004 names the permission checks for the management scope and for `get_agent_tools`. |
| m-11 | Closed | D24 now has literal boundary values. |
| m-12 | Closed | ADR §14 records what it supersedes: the self-edge ban, the MCP install Deny, JUDGE-FR-059 and the dependency exception. |
| o-1 | Closed | Only `get_agent_tools` returns the effective tool catalog (FR-004). |
| o-2 | Closed | The note before visual Dataset B covers the memory cost and says to run those tests one at a time. |
| o-3 | Closed | No change needed. |

## 2. New defects (all minor)

| ID | Where | Defect | Smallest fix |
|---|---|---|---|
| N-1 | ADR §5, paragraph before the matrix (line 149) | "Where connector omission inherits access, use the existing explicit empty/disabled binding form…" is left over from the old design. It contradicts §5.0 and FR-003, where an absent binding already means no access. | Delete the sentence, or replace it with a pointer to §5.0. |
| N-2 | ADR §5 matrix, "Installed connector discovery and assignment" row, Admin column | It says Admin: Allow, but Admin has "—" for capability configuration (no `update_agent`), and §2.1 makes Ava the one who assigns. | Change the cell to "Discovery/management only; no assignment". |
| N-3 | Config FR-005 (`activation: "failed"`) vs FR-007 (`activation_status`) | The same field has two names. | Use `activation_status: failed` in FR-005. |
| N-4 | Config §8 traceability matrix, FR-006 row | FR-006 holds the self-edge rules (the C-1 fix), but its row doesn't list BDD-15, D32 or I4. I4 (`TestADR090_SelfHelperGraphAndDepth`) isn't in the matrix at all. | Add BDD-15 and I4 to the FR-006 row. |
| N-5 | ADR §3 ("global/per-edge depth caps (global default 3)") vs FR-006 ("minimum of the edge, configured global depth and 3") | The spec makes 3 a hard cap even when an operator sets a higher global depth. The ADR only calls 3 the default. | Choose one: drop "and 3" from FR-006, or say "hard maximum 3" in ADR §3. |
| N-6 | Visual US-3 AS3 (line 53) and BDD-09 (line 216) | They still mention "a source blocked when the turn is cancelled… unblocks the source and leaves no leaked read worker". Design item 2 has removed that machinery. | Reword both to "cancellation checked between bounded regular-file reads returns the interrupted outcome". |
| N-7 | Visual US-2 AS3, BDD-06, design item 3, B13 | A permission re-check before a later retry or fallback request in the same turn is kept. It doesn't say which component runs the check or what replaces the image in the tool result when access is denied. | Simplest: drop the same-turn re-check, since the image came from an authorized snapshot and later turns re-read anyway. Otherwise, name the request builder as the component and say the image part becomes the access-refusal text. |
| N-8 | Config FR-003, dedicated tools PUT (unlisted echoes "must equal the current global ceiling"; a mismatch returns 400) | The agent `revision` doesn't cover the global ceiling. If an operator changes a ceiling between a Settings read and its autosave, the save gets a validation 400 instead of a conflict, so the client can't recover by re-reading. | Return 409 CONFLICT on an echo mismatch, or include the relevant ceiling values in the revision. |

Two more notes that aren't defects:
- **Custom-agent seed shape:** FR-008 covers the seed shape for ordinary and hidden roles only. The source seed `NewCustomAgentToolsCfg` uses the full deny-all-then-override map. One sentence saying customs keep that full map would make "explicit Deny for customs" clearly true.
- **Missing edge in ADR §3:** Jim → Admin (dependency handover) isn't in the who-talks-to-whom list, although ADR §6.5 and FR-011 rely on it.

## 3. Traceability and consistency

- **Config spec:** Every user story and acceptance scenario, including US-2 AS6 and US-4 AS5–6, maps to a BDD scenario, test and success criterion. BDD-20 to BDD-25 are all in the matrix. The only gap is N-4. Holdouts are correctly left out.
- **Visual spec:** FR-001 to FR-013 each map to BDD scenarios and tests, and every success criterion maps to BDD scenarios. Rows B10, B13 and B14 now match the corrected Judge scope and retention rules.
- **Across the documents:**
  - The roster tables match (ADR §2.0 and FR-001).
  - The Judge and Supervisor tool lists match (ADR §2.0 and FR-009).
  - The 37 upfront tool names match (ADR §5.4 and FR-008).
  - The Ava confirmation route matches (ADR §3 and FR-007).
  - The dependency exception matches (ADR §14, FR-011 and CLAUDE.md).
- **Your confirmed choices** are all respected: Admin install Allow with role restrictions and operator overrides; connectors need assignment; user changes survive; fixed prompts with editable capabilities; the inverse for hidden agents; no worktree isolation engine.

## 4. Verdicts

| Document | Verdict |
|---|---|
| ADR-090 | **PASS**, with minors N-1, N-2 and N-5 |
| Agent configuration and skills spec | **PASS**, with minors N-3, N-4 and N-8 |
| Visual file reading spec | **PASS**, with minors N-6 and N-7 |

None of the new defects blocks implementation. They are one-line edits worth making before tasks are split out.

## 5. L1 status (separate from the engineering verdict)

**Still open.** Both the ADR and config FR-010 say plainly that the upstream docx, xlsx, pptx and pdf skills are license-restricted at the pinned commit. They say direct bundling is blocked until you choose independent or permissively licensed equivalents, or separate permission is shown.

Because of that, the document-skill package isn't implementation-ready: FR-010, the install commands in FR-011, and BDD-16, BDD-19, SC-007 and SC-008. Nothing in the documents claims otherwise. Everything else can go ahead independently: reader and provider work, agent configuration, the role skills, and the dependency-install location and probe design.

---

## Final narrow verification

Claude Code, actual primary model `claude-opus-5`, 9 turns, 55092 ms. Successful read-only completion.

# ADR-090 final narrow closure (N-1 to N-8)

The `/grill-spec` command isn't installed in this session, so I did the review by hand. I used all 8 read/search calls. I wrote nothing and ran no shell commands or tests.

## Follow-ups

| ID | Status | Exact reason |
|---|---|---|
| N-1 | **Closed** | The ADR no longer contains "inherits access" or "explicit empty/disabled". Config FR-003 (line 166) says "An absent server binding means no access for every role, including custom agents". |
| N-2 | **Closed** | In ADR line 170, the connector row now reads "Discovery/management only; no agent assignment" in Admin's column. Ava's column reads "assignment after confirmed proposal". |
| N-3 | **Closed** | FR-005 (line 182) uses `activation_status: "failed"`, which matches the FR-007 state envelope (line 202). The old field name `activation` no longer appears anywhere. |
| N-4 | **Closed** | The FR-006 row in the traceability matrix (line 577) lists BDD-15 and "SelfHelperGraphAndDepth (I4, D32)". I4 is defined at line 473 and D32 at line 517. |
| N-5 | **Closed** | FR-006 (line 188) now says "the minimum of the edge and configured global depth". It sets 3 only on fresh self-edges, or the lower configured ceiling. So 3 is a starting value, not a hard cap, which matches the ADR. D32 tests caps of 2, 3 and 4. |
| N-6 | **Closed** | US-3 AS3 (line 53) and BDD-09 (line 216) now say that cancellation is checked between bounded regular-file reads. BDD-09 also states that "no asynchronous blocked-source cancellation worker is introduced". B15 still says "no leaked reader worker", but only about refusing FIFO files, not cancellation, so it doesn't conflict. |
| N-7 | **Closed on behaviour; one detail not seen** | US-2 AS3 (line 40), BDD-06 (line 192) and B13 (line 381) define the result. An allowed later request uses only the original authorized snapshot. A newly denied request gets the existing access refusal. The dispositions say the provider request builder is named as the component doing the check. That text is probably on design line 306, which was too long for the search to display, so I couldn't read it within the budget. This is a gap in what I checked, not a defect. |
| N-8 | **Closed** | D33 (line 518) now says: "ceiling changed after GET so unlisted echo differs → 409 CONFLICT and re-read current ceiling", covering BDD-04 and BDD-25. The FR-003 body text (line 164) was also too long to display, so the conflict rule rests on D33 and the dispositions. |

## The two notes

| Note | Status | Reason |
|---|---|---|
| Jim → Admin edge | **Closed** | ADR line 110 adds "Jim → Admin (handover: missing document dependencies)". Line 302 limits it so General Purpose gets no delegation to Admin. |
| Custom-agent seed map | **Partly seen** | ADR line 154 keeps explicit Deny defaults for new custom agents. I couldn't see the spec sentence about keeping the full default map within the budget. It was never a defect, so this doesn't affect the verdict. |

## Verdict

All eight corrections are adequate, and I found no new defects.

| Document | Verdict |
|---|---|
| ADR-090 | **PASS** |
| Agent configuration and skills spec | **PASS** |
| Visual file reading spec | **PASS** |
| Dispositions register | Consistent with the corrected text |

- **L1 (document-skill license):** still open, as intended. The document-skill package stays blocked on it.
- **Tests:** these are spec-quality verdicts only. No implementation or tests exist or are claimed.

---

## Completion of previously truncated evidence

Claude Code, actual primary model `claude-opus-5`, 2 turns, 30234 ms. Successful read-only completion.

# Closure check: N-7, N-8 and the custom-agent note

I read the grill-spec skill first and checked the three paragraphs you supplied against its review principles. I didn't search the source, write anything or run anything. L1 licensing stays open, and nothing here claims the runtime is built.

## Results

| Item | What had to close | Status | Evidence in the text |
|---|---|---|---|
| **N-7** | A named owner for the check, and a denial that swaps the image for correlated refusal text | **CLOSED** | "The provider request builder performs the existing access-policy check immediately before each later candidate/retry request… On denial it removes the inspection image part from that request and substitutes the existing access-refusal text in the correlated tool result; it never transmits the cached bytes for that request." BDD-06/B13 checks this by capturing the outgoing request. |
| **N-8** | A 409 when an echoed ceiling is stale, followed by a re-read | **CLOSED** | "Unlisted values are inherited echoes and must equal the current global ceiling; mismatch returns 409/CONFLICT so Settings re-reads the changed ceiling and revises the proposal if needed." Listed entries stay explicit, equality never implies removal, and duplicate or unknown names return 400. |
| **Custom-agent note** | New custom agents keep their full default map | **CLOSED** | "New custom agents retain their existing complete explicitly authored default map, including installer Deny; this is distinct from ordinary built-in sparse seeding." This also holds when a custom agent is edited: GET returns `override_names` from stored keys, so the full map carries over. |

## What's left

None of the three has a defect that blocks closure. There's one minor wording problem:

- **MINOR (ambiguity), N-8 paragraph, first sentence:** "alongside the complete `builtin.policies` map and revision names exactly which entries to persist" can be read two ways. "revision" could be a request field, or part of the subject of "names". An implementer could leave the revision token out of the PUT body.
  - **Fix:** split it into two sentences. For example: "The request carries the complete `builtin.policies` map, `revision`, and `override_names: string[]`. `override_names` lists exactly which entries to persist as local overrides."

Author closure: the final wording ambiguity was corrected using the exact recommended two-sentence request-field definition in FR-003. The narrow review’s initially truncated N-7/N-8/custom-map evidence was supplied in full and verified in the excerpt follow-up above. No engineering findings remain open; L1 remains open.
