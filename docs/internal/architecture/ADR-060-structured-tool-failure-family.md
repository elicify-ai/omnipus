# ADR-060: Structured tool-failure family — membership and enforcement

- **Status:** Accepted (operator, 2026-08-11). This documents a decision already being executed on `fix/615-617-618-hardening`; it is written **before the branch lands**, at the ruling of the architect review that assessed the change.
- **Date:** 2026-08-11
- **Related:** [#618](https://github.com/elicify-ai/omnipus/issues/618) (which closes by asking, verbatim, *"whether the membership rule belongs in a short ADR or an ADR-058 amendment"* — this ADR is the answer); [ADR-058](ADR-058-tool-denial-semantics.md) §3 last bullet and §7 item 4 (**superseded in part** — see §10); [ADR-059](ADR-059-delegation-observability.md) D4/W5 and §7 (**subsumed** — see §10); [ADR-059 review](ADR-059-delegation-observability-review.md) finding **C-04** (which demanded the contract-impact section this ADR supplies in §7); [ADR-034](ADR-034-agent-create-discriminated-union.md) (the oapi-codegen `oneOf` constraint behind D5); CLAUDE.md **Constraint #8**.
- **Deciders:** Operator (Daniel Piatkowski)
- **Evidence level:** 1. Every code claim below was read in-session on `fix/615-617-618-hardening` @ `fd2edaea` and is cited as `file::symbol`, not `file:line` — `pkg/agent/loop.go` and `turn.go` are ~11k-line files whose line numbers go stale within days (CLAUDE.md says so explicitly, and ADR-059's own review caught two mis-attributed line citations). Claims that are *absences* are cited as searches, not line ranges, for the same reason the ADR-059 review gave (finding m-03): a line range cannot evidence a negative. Three corrections to the briefing this ADR was written from are recorded inline and tagged **[CORRECTION]**.

> **Scope note.** This ADR decides **which payloads belong to the structured tool-failure family, what a member must carry, and what the lint and tests actually enforce** — nothing about *when* a tool should refuse, which is ADR-058's subject. It adds no runtime behaviour. It exists because the branch it documents reverses a recorded scoping decision, generalises a one-off, and introduces a standing convention with new enforcement; none of those are the "mechanical fix executing an already-ratified design" the design-first carve-out covers.

---

## 1. Context

### 1.1 What the family is, in one paragraph

Some tool calls do not fail — they are **refused**, for a reason the calling model needs to act on differently from a crash. Rather than hand the model a sentence and hope it parses the prose, the codebase emits a small JSON object whose first field is a fixed word (`"error": "file_exists"`) naming the refusal kind. The gateway recognises that word, hands the SPA a typed object instead of a blob, and the SPA renders a card instead of raw JSON. That set of payloads is the **structured tool-failure family**. It has four members today.

### 1.2 The family grew twice while its exception stood

`delegation_denied` and `file_exists` arrived governed: contract schema, gateway allow-list entry, SPA renderer. Two later payloads copied the *convention* and none of the *governance*.

Issue #618 states the position at the time it was filed, and its table is reproduced here because it is the whole motivation:

| Discriminator | Schema | Gateway allow-list | SPA renderer |
|---|---|---|---|
| `delegation_denied` | yes | yes | yes |
| `file_exists` | yes | yes | yes |
| `permission_denied` | **no** | **no** | **no** |

A fourth, `tool_assembly_duplicate` (`pkg/agent/loop.go`'s `checkToolDedupInvariant` guard), was not even in that table — it was found while fixing the third.

Both ungoverned members were built with `fmt.Sprintf`'s `%q` verb. **`%q` is Go-string quoting, not JSON quoting.** It emits `\xNN` for a control byte outside `\n\t\r`, and `\x` is not a legal JSON escape, so `json.Unmarshal` rejects the whole document. A filesystem path is attacker- and environment-influenced and may legally contain invalid UTF-8 on Linux, so this was reachable, not theoretical. Neither payload had a length budget either, and both were subject to a downstream 2000-rune truncation that severs JSON into an unparseable fragment.

**The consequence for a user was the same in both cases: a raw JSON blob in the chat where a sentence belonged.** The consequence for a model was worse: a payload it could not parse at all.

### 1.3 Why this needs an ADR and not an amendment

The change on this branch does four things that no ratified decision covers.

1. **It reverses a recorded scoping decision.** ADR-058 §7 item 4 states that `pkg/tools/fserrors.go::PermissionDeniedResult` *"emits a plain string with no schema and is unaffected either way"*, and §3's last bullet deliberately scoped the whole `*ToolResult` refusal family **out**, pricing the extension as *"not free"*. This branch admits `permission_denied` to the family, schema and all. That is a reversal, not an execution.
2. **No general rule was ever ratified.** ADR-059 W5 admitted `file_exists` as a one-off; its §7 scope note says only that the two ADRs *"should be read together on this point rather than as if the line were never crossed."* Read together, they still contain no rule.
3. **It establishes a new standing convention plus new enforcement** — Rule 4 of `scripts/check-no-handwritten-wire-types.sh`, with a deliberate, documented scope boundary that must be ratified rather than left as a script comment.
4. **It makes two modelling decisions with no precedent in this codebase**: one schema shared by two producers on two *different* delivery channels (D3), and a payload that never crosses as a tool result declared a member of the tool-result union anyway (D2, D6).

Neither ADR-058 nor ADR-059 can own this: it supersedes text in the first and subsumes the one-off in the second.

---

## 2. Decisions

### D1 — Membership is an invariant, stated as a checklist

A payload is a **member of the structured tool-failure family** if and only if it carries all of:

| # | Requirement | Where it lives today |
|---|---|---|
| 1 | An **inline schema** in `contracts/asyncapi.yaml` under `components.schemas`, `additionalProperties: false`, with `error` as a `const` discriminator | `DelegationFailure`, `FileExistsRefusal`, `PermissionDenied`, `ToolAssemblyDuplicate` |
| 2 | An **exported `*Code` constant** naming the discriminator | `pkg/tools/result.go` — `DelegationDeniedCode`, `FileExistsRefusalCode`, `PermissionDeniedCode`, `ToolAssemblyDuplicateCode` |
| 3 | A **single producer**, routed through `marshalWithinBudget` | `pkg/tools/result.go::DelegationDeniedResult`, `::FileExistsRefusalResult`, `::PermissionDeniedPayload`, `::ToolAssemblyDuplicatePayload` |
| 4 | An entry in the **family register** — the lint script's known-discriminator set | `scripts/check-no-handwritten-wire-types.sh::KNOWN_STRUCTURED_FAILURE_DISCRIMINATORS` |
| 5 | *(**`toolResult`-channel members only** — see D2)* an **allow-list entry**, a **`oneOf` entry**, and an **SPA detector** | `pkg/gateway/tool_result_store.go::structuredFailureDiscriminators`; `ToolCallResultFrame.result`'s `oneOf`; `src/components/chat/tools/GenericToolCall.tsx::isDelegationFailure` / `::isFileExistsRefusal` / `::isPermissionDenied` |

**Requirement 3 is the one that closes #618's actual defect.** `marshalWithinBudget` (`pkg/tools/result.go`) encodes with `encoding/json` — which escapes every control byte as `\u00NN`, always valid JSON — and then *measures the encoded result*, halving the caller-nominated shrinkable fields until it fits under 1900 runes. Measuring the encoded size rather than clamping the input is load-bearing: `encoding/json` HTML-escapes `<`, `>` and `&` to six runes each and doubles `"` and `\`, all legal filename characters, so arithmetic on input runes understates the encoded size. A first version of this project's budgets made exactly that mistake and a path holding ~67 such characters overflowed a budget that looked safe.

Requirement 3 says **a single producer**, not a single caller. `permission_denied` has one producer and two callers — see D3.

### D2 — The delivery-channel taxonomy

The family splits by **how the bytes reach a reader**, and the split governs which requirements apply.

| Channel | How it travels | Who reads it | Requirement 5 applies? |
|---|---|---|---|
| **`toolResult`** | The tool's `ToolResult.ForLLM` becomes `ToolExecEndPayload.Result`, which the gateway parses (`pkg/gateway/tool_result_store.go::parseStructuredToolFailure`) into `ToolCallResultFrame.result`, plus the frame's `error` field lifted from the payload's `reason` | The calling model **and** the SPA | **Yes** |
| **`message`** | Appended to the session as a `providers.Message` and sent to the LLM as conversation content; the loop `continue`s or `return`s **before** any tool-execution event is emitted, so no `tool_call_result` frame is ever produced | The calling model **only** | **No** |

`parseStructuredToolFailure` has exactly **two production callers** — `pkg/gateway/websocket.go` (live) and `pkg/gateway/replay.go::applyPersistedFailureReason` (reload). Both operate on tool-result strings. A payload that never becomes a tool-result string can never reach either, whatever its shape.

**Classification of the four current members:**

| Discriminator | Producer | Channel |
|---|---|---|
| `delegation_denied` | `pkg/tools/result.go::DelegationDeniedResult`, returned as `*ToolResult` from `pkg/tools/delegate.go` and `pkg/tools/task.go` | `toolResult` |
| `file_exists` | `pkg/tools/result.go::FileExistsRefusalResult`, returned as `*ToolResult` from `pkg/tools/filesystem.go` | `toolResult` |
| `permission_denied` | **two callers, two channels** — see below | **both** |
| `tool_assembly_duplicate` | `pkg/tools/result.go::ToolAssemblyDuplicatePayload`, emitted by `pkg/agent/loop.go`'s `checkToolDedupInvariant` branch | `message` |

**`tool_assembly_duplicate` is `message`-channel, verified.** The emit site wraps the payload in `providers.Message{Role: "system", Content: denyMsg}` with **no `ToolCallID`**, appends it to the session, and returns a `turnResult` carrying `dedupErr`. It emits no `ToolExecStart`/`ToolExecEnd`, so `parseStructuredToolFailure` is unreachable for it by construction. Its `turnResult.finalContent` is also set to the payload, which *looks* like a second, user-facing escape route — but `Process` returns at `if err != nil` **before** the `opts.SendResponse` publish and before the response log, and `dedupErr` is always non-nil on this path, so `finalContent` is dead here. **[CORRECTION]** the briefing did not mention `finalContent`; it is checked and closed, not an open surface.

### D3 — One schema, two producers, two channels — accepted, and the consequence named

`permission_denied` is the first member whose one schema serves two enforcement layers on two different delivery channels:

- **`pkg/tools/fserrors.go::PermissionDeniedResult`** — filesystem-scope denial (`ResolvePath` returning `ErrOutsideScope` / `ErrCarveOut` / `ErrPathInvalid`). Returned as a `*ToolResult` from ~9 call sites across `filesystem.go`, `edit.go`, `web_serve.go`, `send_file.go`, `browser/tools.go`. **`toolResult`-channel.** Always `permanent: true` — a scope denial is a property of the path and the scope, both fixed for the turn.
- **`pkg/agent/tool_denial.go::denialPayloadJSON`** — tool-policy and approval-outcome denial (ADR-058's classification: `user` / `timeout` / `saturated` / `policy_denied` / `no_approver_configured` / the headless auto-deny / …). Called from three sites in `pkg/agent/loop.go`, each appending `providers.Message{Role: "tool", ToolCallID: tc.ID}` and then `continue`-ing **before** the tool-execution path. **`message`-channel.**

**Both are admitted to the family; only one of them reaches the SPA.**

**This must be said plainly, because leaving it implied misrepresents what shipped: issue #618's "no SPA renderer" is fixed for ONE of the two producers.** The filesystem-scope producer now renders as a `PermissionDenied` card via `isPermissionDenied`. The `pkg/agent` producer does not, and cannot, on either path:

- **Live** — the denial branch `continue`s before `EventKindToolExecStart`, so no `tool_call_result` frame exists to parse.
- **Replay** — `applyPersistedFailureReason` installs a parsed object only when `tc.Error != ""` **and** `tc.Result == nil`. For an ask-denial, `pkg/agent/approval_transcript.go::settleAskToolCallTranscript` writes a **different, unschema'd** shape into `Result` — `{"error": true, "text": …, "reason": …, "permanent": …}`, where `error` is a **bool**, not the family's string discriminator — and never sets `Error` at all. So `tc.Error == ""` fails the first condition and `tc.Result != nil` fails the second. **Neither holds. Both were verified in source.**

That is not a defect introduced here — the transcript shape predates this branch and ADR-058 §7 item 2 deliberately chose it (`ToolCall` is `additionalProperties: false`; `Result` is `additionalProperties: true`, so the settled shape needs no contract change). It is recorded because the alternative is an ADR that lets a reader believe #618's rendering gap closed for both producers.

**Why one schema rather than two.** The two producers describe the same thing to the same reader — *this call was refused on permission grounds, here is why, and here is whether retrying can help*. A model should not have to learn two discriminators for one concept because the enforcement happened in a different Go package. The cost is exactly the asymmetry above: a schema whose SPA renderer covers a subset of its producers. Accepted.

### D4 — Placement: asyncapi variants inline, openapi variants one-file-per-schema

Constraint #8's step 1 says *"add schema to `contracts/components/schemas/<TypeName>.yaml`"*. Read literally, all four family schemas violate it: they are **inline** in `contracts/asyncapi.yaml`.

**The rule, ratified:** a schema that participates in a `oneOf` **must be hosted inline in the spec that owns the union**; the one-file-per-schema layout applies to everything else.

**Why, so the rule survives a literal reading of Constraint #8.** This is ADR-034's finding, restated: **oapi-codegen inlines external file refs that appear inside a `oneOf` as anonymous structs, and then emits `As*` accessor methods that do not compile.** The precedent is `AgentCreateRequest`, whose `oneOf` + `discriminator` wrapper is hosted inline in `openapi.yaml` over internal `#/components/schemas/…` refs for exactly this reason. CLAUDE.md already records the exception for `openapi.yaml`; this ADR extends the same reasoning to `asyncapi.yaml`, where `ToolCallResultFrame.result`'s `oneOf` lives.

The practical form: variant schemas are defined under `components.schemas` **in the same file** as the union, and the union refs them with internal `#/components/schemas/…` pointers. That is what the four members do today.

### D5 — Enforcement: what is genuinely enforced, and the four ways a fifth member still gets in

Two mechanisms exist. Both are real. Neither is a closed gate, and the ADR is worth less if it pretends otherwise.

**Enforced today — mechanism 1: the coverage test.**
`pkg/gateway/structured_failure_discriminator_coverage_test.go::TestStructuredFailureDiscriminators_HaveSchemaAndBudgetBoundedProducer` iterates the gateway allow-list and, for each member, requires a fixture in a **deliberately independent** registry (a literal key-for-key duplicate, not a derived transform, so the completeness assertion cannot trivially agree with itself). Per member it asserts: the producer returns without error; the encoded payload is **≤ the 2000-rune downstream truncation cap**; the payload's own `error` field equals the map key; and the payload **validates against the named `contracts/asyncapi.yaml` schema**, compiled from the real spec file. Every fixture is built from `hugeEscapable` — 300 repetitions of `R&D <agent> "name"\path/`, the adversarial JSON-escaping shape that broke the first budget.

**So: allow-list → schema + budget + fixture is a genuine, tested link.** A discriminator added to `structuredFailureDiscriminators` without all three fails immediately.

**Enforced today — mechanism 2: Rule 4 of the lint.**
`scripts/check-no-handwritten-wire-types.sh` (run by `make` and by `.github/workflows/pr.yml`) scans `pkg/gateway/`, `pkg/agent/` and `pkg/tools/` for a Go string literal opening `{"error":"<bare-identifier>"` — raw or escaped-interpreted form — and flags any discriminator not in `KNOWN_STRUCTURED_FAILURE_DISCRIMINATORS`. Prose error fields (`{"error":"failed to serialize response: %s"}`) cannot match the bare-identifier capture at all. This is the guard that would have caught `tool_assembly_duplicate` before it shipped.

**So: literal → family register is a genuine link, for one syntactic form, in three packages.**

**Rule 1's struct scan is deliberately NOT widened to `pkg/agent`/`pkg/tools`, and that boundary is ratified here rather than left as a script comment.** A structural drift audit against Rule 1's json-tag-count heuristic surfaced **77 hits** in those two packages — internal hook-RPC, external-CLI event parsing and inbound-parsing structs that never cross the gateway/SPA boundary. Rule 4 targets the actual defect mechanism (hand-built JSON via string formatting) precisely, with zero tree-wide triage. Widening Rule 1 is a separate, much larger audit. **The scope boundary is accepted; the residual exposure it leaves is §3.**

**The four ways a fifth member still gets in.** Named, not softened:

| # | Gap | Why neither mechanism catches it |
|---|---|---|
| **(a)** | **A struct-based producer** — build a `type myFailure struct{…}` in `pkg/tools`, `json.Marshal` it, return it as `ForLLM` | This is **the very idiom this change blesses** (requirement 3 mandates `encoding/json`, not `Sprintf`). There is no `{"error":"…"` literal, so Rule 4 sees nothing; Rule 1's struct scan does not reach `pkg/tools`. The coverage test only runs over members already in the allow-list. |
| **(b)** | **A nested `"error": {…}` shape** | Rule 4's regex requires a quote immediately after the colon. A `{` there does not match. Both residual producers in §3 have exactly this shape. |
| **(c)** | **A producer outside `pkg/gateway`/`pkg/agent`/`pkg/tools`** | The scan loop enumerates those three directories literally. `pkg/sysagent` — 41 `system.*` tools — is not scanned. |
| **(d)** | **The family register drifts from the Go allow-list** | `KNOWN_STRUCTURED_FAILURE_DISCRIMINATORS` is a **hand-maintained copy** inside the shell script. A repo-wide search finds the identifier only in that one file: **nothing asserts lockstep** between it and `structuredFailureDiscriminators`. Its own comment says the two "must stay in lockstep"; that is a request, not a control. |

Gap (d) is the one worth flagging to a future author as an inversion of this project's own catalogued defect class: a comment asserting a property that no mechanism provides. It is left as-is here (W3 tracks closing it), but it is **recorded**, not implied.

### D6 — The `oneOf` is documentary in both generated artifacts today

`ToolCallResultFrame.result` in `contracts/asyncapi.yaml` lists a permissive any-JSON branch plus seven `$ref`s, four of them the family members. **Neither generator turns that union into a check:**

- **TypeScript** — `src/lib/api/generated/schemas.ts` generates `result: z.unknown()` for the `tool_call_result` frame. `z.unknown()` accepts every value.
- **Go** — `pkg/api/generated/asyncapi_types.gen.go` generates `Result any` on `ToolCallResultFrame`.

**Decision: this does not change here.** The union stays as *documentation of the shapes a reader may encounter*, and the real client-side check is the hand-written detector (`isPermissionDenied` and friends), which structurally tests the fields it is about to render.

**And the corollary that must be stated, because the branch currently asserts otherwise.** Comments added on this branch — `pkg/tools/result.go::PermissionDeniedPayload` (*"A payload violating that is schema-invalid and the SPA drops it entirely"*) and `pkg/api/generated/permission_denied_contract_test.go` (*"…is dropped at the SPA edge"*) — describe a protection that **does not exist for this payload**. With `result: z.unknown()`, a schema-invalid `PermissionDenied` object is **not** dropped: it passes frame validation, fails the detector's structural test, falls through to `plainResult`, and **renders as a raw JSON blob** — which is #618's original user-visible defect, not a safe failure.

The producers' defaulting behaviour (filling empty `message`/`tool`/`reason` rather than forwarding an empty value) is **still correct and still worth keeping** — it just protects against the blob, not against a drop. Asserting a protection that is not there is precisely the *"mechanism not property"* defect class this project has already catalogued, and the comments are corrected as **W1** rather than left to be believed. **[CORRECTION]** the briefing said "two places"; the wording appears in **four** — three comments in `pkg/tools/result.go` (two of which predate this branch, on the `file_exists` producer) and one in `pkg/api/generated/permission_denied_contract_test.go`. W1 covers all four.

---

## 3. Residual risks — two named in-tree producers, ratified out of scope

Both are real, both are reached as **tool results**, both are `{"error": {…}}`-nested, and **neither is caught by anything this branch adds**. They are recorded here so that D5's Rule-1 scope boundary is a ratified decision rather than an implicit one.

**R-1 — `pkg/tools/metadata_guard.go::metadataGuardError`.**
Reached via `tools.ErrorResult(metadataGuardError(...))` from three call sites in `pkg/tools/filesystem.go`, so it crosses as a `toolResult`. Emits `{"error":{"code":"USE_METADATA_TOOL","message":…,"suggestion":…}}`. No schema, no allow-list entry, **no length budget**, and invisible to Rule 4 (gap (b) — the value after `"error":` is `{`). Because `parseStructuredToolFailure` reads `parsed["error"].(string)` and gets an object, the payload is never recognised and **renders as a raw JSON blob in chat**.

**[CORRECTION] — the briefing's characterisation is half right and the half that is wrong matters.** The **primary** path uses `json.Marshal`, which is correct escaping. The unescaped string concatenation exists **only in the `if err != nil` fallback**, and `json.Marshal` on a `map[string]any` of plain strings cannot realistically fail (only channels, funcs and NaN do). So R-1 is **not** a live instance of #618's escaping defect. What is live: **no length budget** (the message embeds an agent ID and a path; `suggestion` embeds a derived file key) and **no rendering governance**.

**R-2 — `pkg/sysagent/tools/deps.go::errorJSON`.**
**163 non-test call sites** across `pkg/sysagent/tools/`, returned as `tools.ErrorResult(errorJSON(...))` — so, again, a `toolResult`. Emits `{"success":false,"error":{"code":…,"message":…,"suggestion":…}}` via `json.MarshalIndent`. Invisible to Rule 4 twice over: gap (b) (nested shape) **and** gap (c) (`pkg/sysagent` is outside the scan entirely). Same **[CORRECTION]** as R-1 — the concatenation is in the marshal-failure fallback, not the primary path. Live exposure: **no budget** (and `MarshalIndent` produces *larger* payloads than `Marshal`, so the 2000-rune truncation is nearer), no schema, no renderer, 163 sites.

**Decision: both are accepted as residual risk for this branch and tracked as follow-up issues (W4).** Neither is a security defect and neither is an escaping defect; both are un-governed structured payloads of the same species, and admitting them is a schema-and-renderer project on the order of #618 itself. **Naming them is what converts D5's scope boundary from implicit to ratified.** They must not be discovered later as evidence that this ADR overclaimed.

---

## 4. Consequences

### Gained

- The family has a **stated membership rule** for the first time. Before this, it had three precedents and no rule, and grew twice by copying the convention and skipping the governance.
- **A fifth member cannot arrive by the mechanism the fourth one used** — a `Sprintf`-built discriminator literal in `pkg/gateway`/`pkg/agent`/`pkg/tools` now fails CI.
- `permission_denied` and `tool_assembly_duplicate` are **escaping-safe and length-bounded**, which is the substantive fix behind #618: no `%q`, no unbounded field, encoded size measured rather than input size clamped.
- The **filesystem-scope** permission denial renders as a card, not a blob.
- The **`toolResult` vs `message`** distinction is written down, so a future author knows why `tool_assembly_duplicate` has no SPA detector and does not "fix" its absence.

### Lost / changed

- **ADR-058's scoping is reversed**, and the `*ToolResult` refusal family is now explicitly in scope for wire governance. The "not free" price ADR-058 quoted was correct and has now been paid: schema, generated artifacts, allow-list, detector, tests.
- **A four-place lockstep** — schema, `*Code` constant, gateway allow-list, lint register — of which only three links are machine-checked (D5 gap (d)).
- **One schema now has a partially-covered renderer** (D3): a `permission_denied` from `pkg/agent` reaches no SPA surface at all.
- Four comments in the tree currently claim an SPA-edge drop that does not happen (D6); W1 corrects them.
- Two in-tree producers of the same species remain ungoverned (§3).

---

## 5. Out of scope — named, not deferred silently

- **Widening Rule 1's struct scan to `pkg/agent`/`pkg/tools`** (77 hits to triage). D5 ratifies the current boundary; it does not close it.
- **Making the `oneOf` executable** — generating a real discriminated union for `ToolCallResultFrame.result` in either artifact. D6 keeps it documentary.
- **Governing R-1/R-2** (§3).
- **Whether a `message`-channel member should reach the SPA at all.** A `tool_assembly_duplicate` aborts the turn and the user sees only the turn's failure. Whether that is enough is a UX question, not decided here.
- **Anything about denial *semantics*** — permanence, retry budgets, quarantine. That is ADR-058 and is unchanged.

---

## 6. Work items

| ID | Decision | Change |
|---|---|---|
| **W1** | D6 | Correct all four "the SPA drops it" comments — three in `pkg/tools/result.go`, one in `pkg/api/generated/permission_denied_contract_test.go` — to say what actually happens: with `result: z.unknown()` a schema-invalid payload is **not** dropped, it fails the SPA detector and renders as a raw JSON blob. Keep the producers' defaulting behaviour; only the stated justification changes. |
| **W2** | D1, D2 | Record the membership checklist and the channel taxonomy where an implementer will meet them — as a doc comment on `pkg/tools/result.go`'s `*Code` constant block, pointing at this ADR. One place, not four. |
| **W3** | D5 gap (d) | Close the register drift: a test (or a `--self-test` assertion) that fails when `KNOWN_STRUCTURED_FAILURE_DISCRIMINATORS` and `structuredFailureDiscriminators` disagree. Until this lands, the script's "must stay in lockstep" comment is a request, not a control. |
| **W4** | §3 | File one issue per residual producer — `pkg/tools/metadata_guard.go::metadataGuardError` and `pkg/sysagent/tools/deps.go::errorJSON` — each stating: no schema, no budget, invisible to Rule 4 (and, for the second, outside its scan), renders as a blob. Link both to this ADR §3 and to #618. |
| **W5** | D5 gap (c) | Decide whether `pkg/sysagent` joins Rule 4's scan. It hosts 41 tools whose results cross the same boundary; excluding it is currently an accident of the original issue's scope, not a decision. |

---

## 7. Contract impact (Constraint #8) — investigated, not assumed

*Modelled on ADR-058 §7. ADR-059's own review demanded this section (finding C-04) and ADR-059 never got one; supplying it here is part of what "subsumes" means in §10.*

**Conclusion: the 5-step pipeline WAS required and WAS run. Two new schemas, both inline, both with committed generated artifacts. `make verify-contracts` must be clean before this branch lands.**

Five surfaces were traced individually.

1. **The two new schemas.** `PermissionDenied` and `ToolAssemblyDuplicate` are defined inline under `contracts/asyncapi.yaml`'s `components.schemas`, both `additionalProperties: false`, both with `error` as a `const`. Generated into `pkg/api/generated/asyncapi_types.gen.go` (Go), `src/lib/api/generated/asyncapi-types.ts` (TS types) and both Zod artifacts (`schemas.ts`, `_asyncapi-zod-schemas.generated.ts`). Hand-written Go wire structs are forbidden by Constraint #8, so this was not optional. **Contract change: yes, done.**

2. **`ToolCallResultFrame.result`'s `oneOf`.** Both new schemas were added to the union. Per **D6** this is documentary in both artifacts today (`z.unknown()` / `any`), so the edit changes no generated behaviour — it is a spec-level statement of the shapes a consumer may encounter. Adding them anyway is deliberate: when the union is ever made executable, a member missing from it would be the silent-drop failure ADR-058 §7 item 4 warned about, and the cheapest time to add it is now. **Contract change: yes, done; behaviourally inert today.**

3. **`ToolAssemblyDuplicate` in the union is the second novel modelling decision, and it is a deliberate anomaly.** It is a **`message`-channel** payload (D2) that can never appear in a `ToolCallResultFrame.result`. It nonetheless carries a `oneOf` entry **and** a gateway allow-list entry. Both are defensive over-provisioning: the allow-list entry is what enrols it in the coverage test's schema-and-budget assertions (D5 mechanism 1 iterates the allow-list), and the `oneOf` entry keeps the union a complete catalogue of the family. It has **no SPA detector**, correctly, because it has no SPA surface. **The invariant in D1 is therefore stated as a floor, not an exact fit: a `message`-channel member MAY carry requirement-5 artifacts defensively, but MUST NOT be assumed to have an SPA renderer.** **[CORRECTION]** the briefing described `message`-channel as implying "no `oneOf` entry"; the branch does the opposite, and this paragraph is the ratification of what actually shipped.

4. **`session.ToolCall.Result` and `.Error`.** Unchanged by this ADR. `ToolCall.yaml` is `additionalProperties: false` at the top level but `Result` is `additionalProperties: true`, which is why ADR-058 §7 item 2's settled ask-denial map (`{"error": true, "text", "reason", "permanent"}`) needs no schema change — and why it is **not** a family member (its `error` is a bool, so `parseStructuredToolFailure`'s `.(string)` assertion rejects it). `Error` is a plain string with no `additionalProperties` constraint. **No contract change.**

5. **The SPA edge.** `result` validates as `z.unknown()`, so no incoming payload is rejected on account of these schemas (D6). The generated Zod objects for the four members exist and are exercised by the Go-side coverage test against the real spec file, not by the SPA at runtime. **No contract change; no new drop risk; no removal of an existing one, because there was none.**

**Regeneration status.** `pkg/api/generated/` and `src/lib/api/generated/` diffs are committed alongside the spec change on this branch, per step 4 of the 5-step process. `make verify-contracts` is a release gate for this branch.

---

## 8. Acceptance criteria

**Verification bar, inherited from ADR-058 §8 and ADR-059 §8 and non-negotiable here: a green test that does not exercise a production caller does not satisfy this ADR.**

| ID | Criterion |
|---|---|
| **AC-01** | **Membership completeness.** For every entry in `structuredFailureDiscriminators`, a fixture built by the **real producer** against adversarial input validates against its named `contracts/asyncapi.yaml` schema and fits the 2000-rune cap. Satisfied by `pkg/gateway/structured_failure_discriminator_coverage_test.go`. A discriminator added to the allow-list with no fixture must fail, not skip. |
| **AC-02** | **Register lockstep (W3).** `KNOWN_STRUCTURED_FAILURE_DISCRIMINATORS` and `structuredFailureDiscriminators` disagreeing fails a check. **Not satisfied today** — this AC is the definition of done for W3, and until it lands D5 gap (d) is open. |
| **AC-03** | **Rule 4 catches its target form.** A synthetic `{"error":"not_a_known_code"` literal in each of `pkg/gateway`, `pkg/agent`, `pkg/tools` is flagged; a prose `{"error":"failed to X: %s"}` is not; a `// not-wire-format` line with ≥40 characters of justification is not. Satisfied by the script's `--self-test`. |
| **AC-04** | **Rule 4's blind spots are asserted, not assumed.** A self-test fixture with a nested `{"error":{"code":…}}` shape produces **no** finding, and the test asserts that absence **explicitly with a comment naming D5 gap (b)** — so a future author who "fixes" the regex sees why the current behaviour was chosen, and a future author who widens the scan has a fixture to invert. |
| **AC-05** | **The escaping fix, at the producer.** Drive `PermissionDeniedPayload` and `ToolAssemblyDuplicatePayload` with input containing invalid UTF-8 and a C0 control byte outside `\n\t\r`, and assert the output round-trips through `json.Unmarshal`. This is the literal defect #618 names; a test on ASCII input does not satisfy it. |
| **AC-06** | **The `toolResult` path, end to end.** A real filesystem-scope denial (`ResolvePath` → `PermissionDeniedResult`) drives a real tool execution; assert the emitted `ToolCallResultFrame.result` is a parsed **object** (not a string), that `error` carries the payload's `reason`, and that `isPermissionDenied` returns true for it. Live and replay both. |
| **AC-07** | **POSITIVE LOWER BOUND — the `message` path is asserted to be absent, deliberately.** Drive a real ask-denial through `pkg/agent`'s producer and assert that **no** `tool_call_result` frame is emitted for it and that `applyPersistedFailureReason` installs nothing (because `tc.Error == ""` and `tc.Result != nil`). Without this, D3's asymmetry is an undocumented gap rather than a ratified decision, and a later "fix" that starts emitting a frame here would pass every other criterion. |
| **AC-08** | **Contract non-regression.** `make verify-contracts` clean; `bash scripts/check-no-handwritten-wire-types.sh` exit 0; the coverage test green on a spec compiled from the real `contracts/asyncapi.yaml`, not a fixture copy. |

---

## 9. Open questions

1. **Does `pkg/sysagent` join Rule 4's scan?** (W5.) 41 tools, 163 `errorJSON` sites, same boundary crossing. Excluded today by the original issue's scope, not by a decision.
2. **Should the `oneOf` ever become executable?** (D6.) Doing so would make a missing member a real drop — a stronger guarantee and a sharper failure mode. Not decided.
3. **Should a `message`-channel member be required to stay OUT of the `oneOf`?** §7 item 3 ratifies the current over-provisioning, but the opposite rule (union = exactly the `toolResult` members) is defensible and would make the union self-describing.
4. **Is `{"error": {…}}` a second family, or an anti-pattern to converge?** R-1 and R-2 share a nested shape that predates the flat one. Converging them is W4's real question and is not answered here.

---

## 10. Relationship to ADR-058 and ADR-059

Recorded explicitly because neither prior ADR can own this decision.

**ADR-058 — superseded in part.** Two passages are overtaken:

- **§3, last bullet** — *"Nothing here touches non-approval refusal classes … Extending the marker to them is a deliberate, separately-scoped choice — see §7 for why it is not free."* That separately-scoped choice is **made here, affirmatively**: `PermissionDeniedResult` is admitted to the family.
- **§7 item 4, final sentence** — *"`pkg/tools/fserrors.go::PermissionDeniedResult` emits a plain string with no schema and is unaffected either way."* **No longer true.** It routes through `PermissionDeniedPayload`, emits the generated `PermissionDenied` shape, and carries an allow-list entry, a `oneOf` entry and an SPA detector.

Everything else in ADR-058 stands, including §7's other three items and the whole of §2's denial classification, which this ADR does not touch.

**ADR-059 — subsumed, not superseded.** W5 admitted `file_exists` as a one-off and §7's scope note observed that ADR-058's line had been crossed without saying what the new line was. This ADR states the rule that `file_exists` was the first instance of, and supplies the Constraint #8 contract-impact section that ADR-059's own review demanded (finding **C-04**) and that ADR-059 never got. ADR-059's D1–D5 are untouched.
