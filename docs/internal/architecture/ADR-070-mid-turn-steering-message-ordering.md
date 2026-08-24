# ADR-070: Mid-turn steering — assistant reply must not render above the message that triggered it

- Status: Accepted (2026-08-24, HEAD `eaa7b131`; ratified by operator — see §5 resolutions)
- Deciders: operator (Daniel Piatkowski)
- Related: mid-turn steering feature ("bugfixes3" — `src/store/chat.mid-turn-send.test.ts`,
  `src/components/chat/ChatScreen.mid-turn-send.test.tsx`); ADR-057 (session identity);
  `pkg/agent/steering.go`, `pkg/agent/loop.go` (steering queue, injection)

## 1. Problem

The SPA lets a user submit a new message while the agent is still mid-turn ("mid-turn steering" /
"steer"). The gateway already supports this: the queued message is injected into the running turn
between LLM calls (`pkg/agent/loop.go`, the `iteration > 1` / `dequeueSteeringMessagesForScope`
branch), and the injected message is persisted to the session transcript at the moment it's
dequeued (`ts.agent.Sessions.AddFullMessage`), genuinely interleaved in time between the pre-steer
and post-steer LLM calls.

Reported symptom: after sending a follow-up mid-turn, the agent's reply to that follow-up renders
**above** the follow-up message in the chat, not below it.

### 1.1 Root cause — live rendering

`src/store/chat.ts`'s `sendMessage` (`isStreaming` branch) appends the new user message to the END
of `messageOrder`, but does **not** close out the assistant bubble that was already streaming. That
bubble is left `isStreaming: true`, deliberately, per the code's own comment:

> "The existing streaming assistant bubble ... stays live and keeps receiving content for THAT SAME
> turn ... leaves `isStreaming` untouched (already true; resetting it here would race the in-flight
> turn's own done/error frame)."

The live `token` frame handler (`case 'token'`) locates its target bubble via
`findLastAssistantMessageId(order, messagesById)` — an unconditional **backward scan for the last
message with `role === 'assistant'`**, blind to any user message that has since been appended after
it. Because the pre-steer assistant bubble is still `isStreaming: true` and sits before the user's
new message in `messageOrder`, every subsequent token — including the model's reply to the just-
injected follow-up — is appended into that earlier bubble. Visually: the reply to your follow-up
renders in a bubble positioned above your follow-up.

A second, compounding defect in `src/components/chat/ChatScreen.tsx`
(`VirtualizedMessageListInner`): the mechanism that pins the live/streaming bubble to the very
bottom of the DOM (`hasStreamingMessage = isStreaming && messages[messages.length - 1]?.isStreaming`)
keys off "is the LAST array item streaming." Once the steer's user message becomes the array's last
item, this check goes false, the pinned-to-bottom rendering silently disengages, and the
still-streaming assistant bubble instead renders at its ordinary (pre-steer) array position via the
historical row path — reinforcing the same wrong visual order.

Empirically confirmed (2026-08-24, HEAD `eaa7b131`):
- `src/store/chat.mid-turn-send.test.ts` (existing, currently green) *asserts* this ordering as
  intended behavior — e.g. `'a tool call BEFORE the steer and one AFTER both keep correct
  owner/offset on the SAME bubble'` deliberately merges pre- and post-steer content into one bubble
  positioned before the steer message. This is the bug, encoded as a passing test.
- A throwaway reproduction (rendering `ChatScreen` with `[user, streaming-assistant, user-steer]`
  seeded) confirmed `[data-testid="streaming-message-anchor"]` is **absent** in this configuration —
  proving the "pin to bottom" mechanism is off exactly when steering is in play. (Test file was not
  committed; disposable, deleted after the run.)

### 1.2 Root cause — reload / reconnect (replay)

The transcript IS persisted with correct chronology (§1, backend). But the SPA's own
`replay_message` frame handler (`src/store/chat.ts`, the same-turn merge block) reconstructs history
from persisted entries using a merge rule that is **equally blind** to intervening messages:

```
const sameTurn = !!replayTurnId && candidate.turnId === replayTurnId
const compatibleProducer = !replayAgentId || !candidate.agentId || candidate.agentId === replayAgentId
if (sameTurn && compatibleProducer && !alreadyMerged) { /* merge into candidate */ }
```

`candidate` is resolved via the same `findLastAssistantMessageId` backward scan (used both for the
"coalesce into an empty tool-call placeholder" branch and this general merge branch). Since a
steering injection does not start a new turn (`turn_id` is unchanged) and does not change the
producing agent, this rule merges the pre- and post-steer transcript entries into ONE bubble on
reload — reproducing the exact same wrong order after a refresh, independently of whatever fix is
applied to the live path. **A live-only fix does not survive a page reload.**

### 1.3 What is NOT broken — CORRECTED after code review (scope of the backend claim was overstated)

- The backend's persisted transcript ordering **for the tool-call-interleaved case only**:
  `pkg/agent/turn.go::appendIntermediateAssistantTranscript` persists the narration segment
  BEFORE the tool_call entries whenever a response carries both text and tool calls
  (`pkg/agent/loop.go:9639`), and `pkg/gateway/replay.go`'s `streamReplay` emits one frame per
  persisted entry in that same order — no re-ordering on the wire. This is what §2.2's replay-path
  fix (the raw-tail guard) actually protects.
- Tool-call ownership/results (`toolCallOwnerMessageId`, `toolCalls` map) — both are keyed by
  `call_id`, not by message array position, so they are unaffected by where a bubble boundary falls.
- Turn cancellation correlation (`findAssistantMessageIdByTurnId`) — already backward-scans for the
  LAST message carrying a given `turnId`, which is exactly correct if a turn ends up split across
  two bubbles sharing one `turnId` (see §2.3).

**Correction (code review, 2026-08-24):** the ORIGINAL wording of this section claimed the
persisted transcript ordering was "confirmed via read" without qualification — that was only ever
checked against the tool-call path. Tracing the OTHER steering-continuation path
(`pkg/agent/loop.go:9454-9459`, inside `if len(response.ToolCalls) == 0 || gracefulTerminal`, the
branch taken when the LLM answers with plain text and a steer is already queued) found it does
**not** call `appendIntermediateAssistantTranscript`, does **not** append the assistant's own
response to the in-memory `messages` conversation array, and does **not** call
`ts.agent.Sessions.AddFullMessage` for it — the only call site of
`appendIntermediateAssistantTranscript` in the whole file is the tool-call branch
(`pkg/agent/loop.go:9639`). The pre-steer text streams live (via `streamer.Update`, so the user
does see it happen in real time) but is never independently persisted before the next LLM call's
streamer supersedes it — and `markLastStreamerTranscriptPersisted`'s own doc comment
(`pkg/agent/turn.go:1230-1234`) confirms a superseded streamer's content "are never finalized." A
backend integration test's own naming corroborates this is closer to deliberate design than
oversight — `TestAgentLoop_Steering_DirectResponseContinuesWithQueuedMessage`
(`pkg/agent/steering_test.go:1027`) calls the pre-steer text `"stale direct response"` and the
post-steer text `"fresh response after steering"`, and asserts only the final response — it does
not assert anything about what a page reload would show.

**Practical consequence:** §2.2's replay-side fix (the raw-tail guard on the same-turn merge) is
still correct and still necessary for the shape it protects — a plain-text steer with a
subsequent, unrelated turn also happens to interleave correctly enough in practice for the
common "type more text, then more text" shape that its assistant/user/assistant entries generally
land in transcript order regardless of tool calls; if that ordering assumption is ever wrong for
a given turn, the raw-tail guard is what prevents a wrong merge, not what could ever CAUSE one.
What this correction removes is only the ADR's blanket claim that a mid-turn steer's PRE-steer
narration is always durably persisted as its own reload-visible entry — for a turn with no tool
calls, it may not be. Whether that pre-steer text should be persisted (matching what was shown
live) or is intentionally treated as a superseded draft is a backend design question, genuinely
out of scope for this SPA-only ADR (§5.3) — filed as
[elicify-ai/omnipus#652](https://github.com/elicify-ai/omnipus/issues/652), not addressed here.

No wire-protocol change is required for the SPA-side fix in §2: it is confined to how the SPA's
own store interprets frames it already receives correctly, independent of the backend persistence
question above.

## 2. Decision (proposed)

Apply one rule, symmetrically, in both places that currently answer "can I keep appending to this
bubble" via a backward role-scan:

> An assistant bubble may only receive further content if it is still genuinely the open segment —
> nothing else has been appended to the transcript since it was last written to. The instant
> something else lands after it, it is closed; new content starts a new bubble.

### 2.1 Live path (`src/store/chat.ts`, `sendMessage`'s `isStreaming` branch)

When a mid-turn steer is sent, close out the currently-open assistant bubble in the SAME store
update that appends the user's message: `isStreaming: false`, and stamp `closedBySteer: true` (§2.4
— status stays `'done'`). No new bubble is minted at this point (unchanged) — the NEXT `token` (or
`tool_call_start`) frame naturally opens a fresh bubble, because:
- `case 'token'`'s existing "abandon a non-streaming last-assistant bubble, open a new placeholder"
  branch already does the right thing once `isStreaming` is false — **no change needed there.**
- `case 'tool_call_start'`'s existing raw-tail-then-`isStreaming`-gated fallback already refuses to
  reuse a non-streaming bubble — **no change needed there either.**

This is deliberately the smallest lever: flipping one flag at steer-time, rather than rewriting the
frame handlers, because those handlers' existing "is this bubble still open" logic is otherwise
correct — it was only ever fed a wrong `isStreaming` value in this one scenario.

Side effect (also a fix, for free): once the new bubble is created it is again the true last item in
`messageOrder`, so `ChatScreen.tsx`'s `hasStreamingMessage` check starts working again unmodified —
**no change to `ChatScreen.tsx` is required.**

**Grill-spec round 1, F2 — two more live handlers share the unguarded scan (REVISE, addressed):**
`case 'subagent_start'` and all three `case 'media'` branches also resolve their target bubble via a
*bare* `findLastAssistantMessageId` call with **no** raw-tail/`isStreaming` gate at all (unlike
`token`/`tool_call_start`, which already have one). Left unfixed, a delegation span or a media
attachment arriving as the first live frame after a steer — before any `token`/`tool_call_start` has
opened the post-steer bubble — would reattach to the closed, pre-steer bubble, reproducing the exact
ordering bug for delegation/media content instead of narration text. Fix: extract the eligibility
check `tool_call_start` already implements into a shared helper and apply it at all four sites:

```ts
/**
 * The assistant bubble still eligible to receive more live content: the raw
 * tail of messageOrder if it's an assistant message (the common case), or —
 * when something else (a mid-turn steer) has landed after it — the last
 * assistant message ONLY if it is still genuinely open (isStreaming). Null
 * means no bubble is eligible; the caller must open a new one.
 */
function findOpenAssistantMessageId(
  order: readonly string[],
  messagesById: Record<string, ChatMessage>,
): string | null {
  const tailId = order[order.length - 1]
  const tail = tailId ? messagesById[tailId] : undefined
  if (tail?.role === 'assistant') return tailId ?? null
  const candidateId = findLastAssistantMessageId(order, messagesById)
  const candidate = candidateId ? messagesById[candidateId] : undefined
  return candidate?.isStreaming ? candidateId : null
}
```

- `subagent_start`: replace its bare `findLastAssistantMessageId` call with this helper. Today, when
  it resolves to `null` it silently drops the span (`if (!lastMsgId) return`) — mirror
  `tool_call_start`'s "open a new placeholder" behavior instead, since silently dropping a delegation
  span makes real agent activity invisible, which is a worse outcome than the mis-ordering bug itself.
- `media`'s two error-notice branches (invalid/zero parts): replace the bare scan with the helper; if
  it resolves to `null`, drop the best-effort notice exactly as today's `if (lastMsgId)` guard already
  does for a bucket with no assistant message at all — these are cosmetic, rare-path notices, not
  worth mining a new bubble for.
- `media`'s real-attachment branch already carries a downstream eligibility refinement
  (`canAttach = msg.isStreaming || (msg.content ?? '') === ''`) for a case unrelated to steering
  (attaching to a freshly-created, not-yet-streaming placeholder). Keep that refinement as-is, but
  feed it a candidate resolved via the helper instead of the bare scan, so it can never even consider
  a bubble sitting before an intervening steer message.

`token` and `tool_call_start` are unchanged — confirmed above they are already correct once
`isStreaming`/raw-tail reflect the steer-time closure.

### 2.2 Replay path (`src/store/chat.ts`, the `replay_message` same-turn merge block)

Add a "candidate is still the raw tail of `messageOrder`" condition alongside the existing
`sameTurn && compatibleProducer` test, in both places `lastMsgId` is used to decide "reuse this
bubble":
1. The "coalesce text into an empty tool-call placeholder" branch.
2. The general same-turn merge branch.

If anything (in practice: the steer's persisted user entry) was appended to `messageOrder` after the
candidate bubble since it was created, refuse the merge and fall through to creating a new bubble —
mirroring §2.1 exactly, expressed via array-tail position instead of `isStreaming` (replay bubbles
are finalized the instant they're created, so `isStreaming` cannot serve as the signal there — this
is why the two paths need parallel-but-differently-expressed versions of the same rule).

### 2.3 Turn-id / cancellation impact

`findAssistantMessageIdByTurnId`'s doc comment already anticipates multiple assistant bubbles
sharing one `turnId` ("async delegation can interleave ... frames between an assistant entry and its
later cancellation") and backward-scans for the LAST match. Splitting a steered turn into two bubbles
sharing one `turnId` is exactly the shape this function already handles correctly — **no change
needed.**

### 2.4 Status naming for the closed pre-steer bubble — REVISED after grill-spec F1

Operator asked for the pre-steer bubble to be internally distinguishable from an ordinarily-finished
reply, for future debugging — with **no stated requirement that it look or behave any differently in
the UI** (§5.1). The original design for that ("add `'steered'` to the `AssistantMessage.status`
union") is **rejected** — grill-spec round 1 (F1, CRITICAL) found the design's own safety argument
was false:

> §2.4 (original) claimed "the compiler's exhaustiveness check surfaces every such site as a type
> error... mechanically-verifiable, not a manual audit." **This is false.** `src/lib/api.ts`'s
> "TypeScript exhausts the union" comment describes exhaustiveness over the `role` discriminant, not
> over `status` values — no code anywhere does an exhaustive switch on `AssistantMessage.status`.
> Every real consumer (`ChatScreen.tsx:2881,2991,730,871,1143,1447`; `MessageItem.tsx:245,248,264`;
> `src/lib/omnipus-runtime.ts:196-201`; several in `chat.ts` itself) is a plain `===`/`!==` equality
> check — the compiler gives zero signal when a new status value silently fails to match any of them.
> Concretely, `omnipus-runtime.ts`'s `buildMessageStatus` (used by `ActionBarPrimitive.Copy`'s
> visibility) falls through to `{ type: "complete", reason: "stop" }` for any status it doesn't
> recognize as `error`/`interrupted` — so a `'steered'` bubble would be Copy-able exactly like
> `'done'` anyway, silently, with no build-time signal — directly undermining the "not simply an
> alias for `'done'`" framing the original design rested on.

**Decision:** keep `status: 'done'` for the closed pre-steer bubble (identical to any ordinarily-
finished reply, so every existing UI/ARIA/Copy site keeps working, unmodified, with its current,
correct-for-`'done'` behavior — zero new call sites to touch, zero risk of a silent behavioral miss).
Add a separate, purely-internal boolean field instead:

```ts
// ChatMessage (src/store/chat.ts) — internal-only, never rendered directly.
/** True when this assistant bubble was closed because a mid-turn steer
 *  landed after it, rather than because the reply/turn fully finished.
 *  Debugging/bookkeeping only — deliberately does not participate in any
 *  status union or UI branch; see ADR-070 §2.4 for why a status-union
 *  member was rejected. */
closedBySteer?: boolean
```

This achieves the operator's actual stated goal (distinguishable in future debugging) with none of
the blast radius the union-member design had — visible behavior (Copy button, ARIA announcement,
`ModelFooter`, etc.) is byte-for-byte identical to today's `'done'` handling, because it *is*
`'done'`. The flag is consulted in exactly two places, both internal bookkeeping, not rendering:
- §2.6 below (the C8 error-sweep must not re-stamp a `closedBySteer` bubble).
- Test assertions that need to distinguish "closed by steer" from "closed because the turn ended,"
  per §4's required test additions.

### 2.6 C8 error-sweep must not re-stamp a `closedBySteer` bubble — grill-spec F5

`case 'error'`'s C8 sweep (`chat.ts`, ~line 3820) computes:

```ts
const lastContentEmpty = !!lastMsg && (!lastMsg.content || !lastMsg.content.trim())
const lastIsThisTurn = !!lastMsg && (
  lastMsg.isStreaming || lastMsg.status === 'streaming' || lastContentEmpty
)
```

`lastContentEmpty` alone can make `lastIsThisTurn` true for a bubble that is **already closed** —
independent of `isStreaming`/`status` — whenever its content happens to be empty. This is reachable
today: a steer sent before the agent has produced any visible text yet (the placeholder is still
`content: ''` when `sendMessage` closes it per §2.1) leaves exactly such a bubble. If the turn then
errors, this bubble would be wrongly pulled into the sweep and re-stamped with the error, silently
overwriting the correct `closedBySteer`/`'done'` state. Fix: exclude it explicitly, mirroring how
`lastIsTerminalError` already excludes `'error'`:

```ts
const lastIsThisTurn = !!lastMsg && !lastMsg.closedBySteer && (
  lastMsg.isStreaming || lastMsg.status === 'streaming' || lastContentEmpty
)
```

### 2.7 Two more read-side consumers of the unguarded scan — grill-spec round 2 (spec review)

§2.1/F2 covered every handler that *writes* content via an unguarded
`findLastAssistantMessageId` scan. A second grill-spec pass (round 2, run against the
implementation spec derived from this ADR) asked the complementary question — which consumers
*read* "the last assistant message" the same unguarded way, where correctness depends on
*when* that resolution changes, not just *which* message it resolves to — and found two more,
neither previously enumerated:

**`markLastMessageInterrupted` (`chat.ts:1837-1878`, scan at `chat.ts:1840`).** Invoked
unconditionally by Stop/Escape/`/cancel` (`cancelStream`). In the window between a steer
closing the pre-steer bubble (§2.1) and the next frame opening a new one, this bare scan
resolves to the just-closed, already-`'done'`, `closedBySteer` bubble — the only assistant
message present — and cancelling in that window overwrites it to `status: 'interrupted'`,
mislabeling an already-finished, already-correct reply segment. Reachable via the single most
ordinary "changed my mind right after steering" interaction; not obscure. Fix: route this
resolution through `findOpenAssistantMessageId` (§2.1) instead of the bare scan; when it
resolves to `null` (steer closed the last bubble, nothing has reopened yet), fall through to
the SAME "no assistant message exists yet → create an interrupted placeholder" branch this
function already has (`chat.ts:1841-1866`) for the pre-existing "cancel fired before the first
token frame" case — no new branch needed, just a broader trigger condition on the existing one.

**`lastAssistantMessageId` (`chat.ts:1539`, inside `bucketToForeground`).** Also a bare,
unguarded scan, exposed as a derived store field. Its only consumer anywhere in the codebase
(confirmed by search — no other call site exists) is `ChatScreen.tsx`'s ARIA live-region "New
response from {agent}" screen-reader announcement (`ChatScreen.tsx:2871-2883, 2990-2993`),
which fires whenever this field's resolved message transitions to `status: 'done'` and hasn't
been announced yet. Traced by hand: once a steer closes the pre-steer bubble in place
(`status: 'done'`), this field immediately resolves to it and the announcement fires — at the
exact moment the user sent their own follow-up, before the agent has said anything further.
When the true post-steer bubble later finishes, it fires again — correctly, but as a second,
spurious-preceded announcement. A genuine accessibility regression in the feature's primary
happy path. Fix: since this field's only real consumer's own intent ("announce when the most
recent reply is genuinely done") is best served by the same "still open counts, otherwise the
raw tail" rule as everywhere else, route it through `findOpenAssistantMessageId` as well —
traced by hand this correctly suppresses the premature announcement during the steer gap (the
helper returns `null` there) without changing behavior for the ordinary non-steer case (the
final bubble is always the raw tail once it's genuinely done, so the helper returns it exactly
as the bare scan already did).

Both are additions to the §2.1 guarded-call-site list, not new mechanisms — same helper, same
rule, two more places it must be applied. Test coverage: a "steer → cancel, no intervening
frame" scenario (the one ordering the existing "steer → new bubble opens → cancel" test does
not cover) and an "ARIA announcement fires exactly once per steered turn, not once per segment"
scenario, both added to the implementation spec.

### 2.8 Implementation-time correction: the bubble must close on SEND SUCCESS, not on send attempt

Caught by running the actual test suite during implementation (not by static review): the first
implementation of §2.1 closed the pre-steer bubble unconditionally, in the SAME `withBucket`
update that appends the steer's user message — before the WS `connection.send()` call. This
broke a pre-existing, correct test (`chat.mid-turn-send.test.ts`, `'a failed mid-turn send marks
only the steering message as error...'`): when the steer's send genuinely fails, the backend
never received it and keeps writing into the ORIGINAL segment exactly as before — closing the
bubble in that case is actively wrong, not merely unnecessary. Fixed by splitting into two
separate `withBucket` calls: the user message is appended unconditionally (unchanged from
before); the bubble is closed in a second, later update that only runs once
`connection.send(steerPayload)` has returned `true`. The failure branch (`!steerSent`) is
untouched — it still only marks the user bubble `'error'`, exactly as before. This is the kind
of gap the "actual UAT executed via Playwright" and "run the real test suite" steps in this
project's workflow exist to catch — recorded here because it changes §2.1's precise trigger
condition (send SUCCESS, not send ATTEMPT) in a way worth citing if this code is touched again.

## 3. Alternatives considered

**A. Keep one continuous bubble; fix only the visual position.** Reject: still glues the reply to
your follow-up onto a paragraph that started rendering before you asked it — reads wrong even with
correct DOM position — and does not touch the replay-path bug at all (replay has no "pin to bottom"
concept to patch).

**B. Backend emits an explicit "segment boundary" marker on the wire (new `TokenFrame` field or a new
frame type).** More explicit in principle, but requires a contract-first change (Constraint #8:
`contracts/asyncapi.yaml`, codegen, generated-type churn) for information the SPA already has in
full — it is the one that sent the steer. Reject as unnecessary surface area for a purely
client-side bookkeeping defect; the client-only fix in §2 is provably sufficient (traced against
every current consumer of `messageOrder`/`turnId`/`isStreaming` above).

## 4. Consequences

- `src/store/chat.mid-turn-send.test.ts` currently encodes the bug as intended behavior in at least
  two tests (`'steer → tool_call_start → token → done: exactly one assistant bubble...'`, `'a tool
  call BEFORE the steer and one AFTER both keep correct owner/offset on the same bubble'`). These
  must be rewritten to assert TWO bubbles, split at the steer, tool calls attributed to whichever
  bubble was open when each call started. This is required test churn, not a regression.
- One existing test (`'steer → done (no tool calls): unchanged — single assistant bubble, fully
  finalized'`) is expected to keep passing unmodified — traced by hand against the new design: the
  bubble closes at steer-time either way, and since no further token arrives before `done`, no
  second bubble is ever created, so the final assertion (`length === 1`, same content, `status:
  'done'`) is unaffected (it never asserted anything about `closedBySteer`, so the new field's
  presence doesn't touch it either).
- New tests are needed for the replay-path fix (no existing coverage seeds a steered session's
  `replay_message` sequence today).
- **Grill-spec F3 (required):** add a `ChatScreen`/`VirtualizedMessageListInner`-level DOM test that
  renders `[user, streaming-assistant, user-steer, token]` and asserts
  `[data-testid="streaming-message-anchor"]` is present, pinned to the NEW (post-steer) bubble. This
  is the one claim in this ADR (the `hasStreamingMessage` self-heal in §2.1) that, before this
  revision, existed only as a deleted throwaway repro — it must land as committed coverage.
- **Grill-spec F4 (required):** add a test for a tool call that **starts before** a steer and
  **resolves after** it (`tool_call_start` → steer → `tool_call_result`). Reading the code suggests
  this already works by construction (`toolCallOwnerMessageId`/`toolCalls` are keyed by `call_id`,
  independent of bubble position — §1.3), but that is inference, not verified behavior, and the
  existing test at `chat.mid-turn-send.test.ts:511` resolves its pre-steer call fully before the
  steer fires, leaving the in-flight-across-the-boundary case unexercised.
- **Grill-spec F6 (informational):** the §2.2 raw-tail guard, once added, incidentally tightens a
  pre-existing, steering-unrelated looseness in the "coalesce into an empty tool-call placeholder"
  replay branch — today it coalesces into *any* assistant bubble with empty content regardless of
  `turnId`/`agentId`, so two unrelated turns separated only by non-assistant traffic could
  theoretically merge on reload if the earlier one legitimately ended with empty narration (a
  tool-only turn). Documented here as a beneficial side effect, not separately scoped work.
- **Grill-spec F7 (confirmed non-issues, no action):** two edge cases raised for stress-testing were
  checked against the actual code and ruled out: (a) the offline outbound queue (`maybeDrainNext`) is
  gated on `!isStreaming`, so a buffered message can never dispatch as a mid-turn steer — only as an
  ordinary fresh-turn message once streaming has stopped, by construction; (b) ring-buffer eviction
  treats each bubble in the new two-bubble shape independently and `findAssistantMessageIdByTurnId`
  already tolerates a no-match miss, so no new failure mode from trimming.
- No backend, wire-protocol, or contract changes.

## 5. Operator resolutions (2026-08-24)

1. **Status naming (§2.4):** operator asked for a distinguishable-for-debugging marker, not a
   visibly-different status. RESOLVED as a purely-internal `closedBySteer` boolean rather than a new
   `status` union member — see §2.4 for why the union-member design was reverted after grill-spec F1
   found it had an unstated, silently-wrong UI consequence (Copy-button eligibility via
   `buildMessageStatus`) that the operator was never asked about. The visible behavior the operator
   will see is unchanged either way; only the safer implementation vehicle changed.
2. **UAT method:** RESOLVED — live provider key, if one is configured in this environment. To be
   confirmed at UAT time (§ implementation plan); fall back to scripted WS frames only if no key is
   available, and say so explicitly rather than silently substituting.
3. **Scope confirmation:** RESOLVED — fix all three mechanisms in §1 (live ordering, its automatic
   `hasStreamingMessage` side-effect fix, and the replay/reload merge bug), plus grill-spec F2's two
   additional live handlers (`subagent_start`, `media`) that share the same unguarded-scan defect —
   these are not new scope, they are the same named mechanism (#1, live ordering) implemented
   completely instead of partially. Nothing else in the mid-turn-steering feature is in scope.

## 6. Grill-spec review log

- **Round 1** (2026-08-24, background `/grill-spec` run against this ADR + current source): verdict
  **REVISE**. 2 CRITICAL/MAJOR-adjacent structural findings (F1: false exhaustiveness-safety claim;
  F2: two more live handlers share the root-cause defect pattern), 3 MAJOR (F3/F4: missing required
  tests; also F2), 1 MINOR (F5: C8 sweep gap), 2 OBSERVATIONS (F6, F7: confirmed non-issues). All
  addressed above (§2.1, §2.4, §2.6, §4). Full findings preserved in the grill-spec agent's output;
  not re-transcribed here beyond the citations above.
- **ADR round 1 was the only grilling round run directly against this document.** Per the
  goal's sequencing (ADR → grill once → fix → plan-spec → grill the SPEC twice → implement),
  the two further required rounds were run against the derived implementation spec
  (`docs/internal/specs/mid-turn-steering-message-ordering-spec-2026-08-24.md`), not this ADR
  directly. Spec round 2 fed one more fix back into this ADR: §2.7 (two additional read-side
  consumers of the unguarded scan — `markLastMessageInterrupted`, `lastAssistantMessageId` —
  that neither ADR round 1 nor spec round 1 had enumerated). Full spec-review logs (both
  rounds) are preserved at
  `docs/internal/specs/mid-turn-steering-message-ordering-spec-2026-08-24-review.md`.

## 7. Code review log (implementation)

- **`/codereview` pass (2026-08-24), against the actual implementation diff**, with mutation
  testing (reverting each of the seven guarded call sites independently and re-running the suite):
  found that only the live `sendMessage` closure (§2.1) was verified by any test at implementation
  time — the other five sites (both `replay_message` branches, the C8 exclusion,
  `markLastMessageInterrupted`, `lastAssistantMessageId`, and all three `media` branches plus
  `subagent_start`) reverted clean with zero test failures. Also confirmed the two spec-required
  tests (F3, F4) had not yet landed. **Addressed**: a full test suite was added covering every one
  of the seven call sites plus the replay-path guard and both F3/F4 scenarios — see
  `src/store/chat.mid-turn-send.test.ts` (8 new describe blocks), `src/store/chat.tool-call-offset.test.ts`
  (2 new tests in the "WS-replay same-turn merge" block), and
  `src/components/chat/ChatScreen.virtualization.test.tsx` (2 new tests). Re-run after adding
  coverage: `npm run typecheck` clean, `npx eslint` clean on all touched files, full
  `src/store/`+`src/components/chat/` sweep green.
- **Important finding, backend scope**: tracing the reviewer's concern about the replay-path fix's
  premise led to confirming a genuine, pre-existing backend gap — see §1.3's correction above and
  [elicify-ai/omnipus#652](https://github.com/elicify-ai/omnipus/issues/652). Not fixed here
  (explicitly out of this ADR's SPA-only scope, §5.3) — filed and cross-referenced instead.
- **Two small consistency fixes applied** (not blocking, but real): the two `media` error-notice
  branches now mint a fresh bubble when there is no eligible open one, matching the real-attachment
  branch's own fallback, instead of silently dropping the notice (a genuine UX asymmetry the
  reviewer caught: the success path gained a bubble, the failure path lost its message). The
  live steer-close now also clears `pendingTextBoundary`, matching every other close site.
  `subagent_start`'s placeholder `isStreaming: true` (flagged as inconsistent with
  `tool_call_start`'s `isReplaying` guard) was checked and found to already match
  `tool_call_start`'s own placeholder-creation branch exactly (neither gates the PLACEHOLDER's own
  `isStreaming` on `isReplaying` — only the bucket-level flag is gated) — left unchanged as a
  non-issue, not a missed fix.

## 8a. Test-plan-and-write mutation self-verification (2026-08-24)

Per the `test-plan-and-write` skill's mandatory gate ("mutate your own code and confirm the test
dies"), every one of the ten guarded points this fix touches was independently reverted, one at a
time, with the corresponding test(s) re-run to confirm a RED result, then restored
(`git checkout -- src/store/chat.ts`) before the next:

| # | Site | Result |
|---|------|--------|
| 1 | `sendMessage` steer-close | 12/23 tests died |
| 2 | `subagent_start` guard | 1/1 died |
| 3 | `media` invalid-parts guard | 1/1 died |
| 4 | `media` zero-attachments guard | 1/1 died |
| 5 | `media` real-attachment guard (non-empty pre-steer content) | **survived — false negative found** |
| 5b | `media` real-attachment guard (empty pre-steer content, new test) | 1/1 died |
| 6 | `replay_message` general merge guard | 1/1 died |
| 7 | `replay_message` empty-placeholder coalesce guard | **survived — zero coverage found, new test added** |
| 8 | C8 sweep `closedBySteer` exclusion | 1/1 died |
| 9 | `markLastMessageInterrupted` guard | 1/1 died |
| 10 | `lastAssistantMessageId` (ARIA) guard | 1/1 died |

Two real gaps surfaced by this process, not by review:

- **#5**: the original test used a non-empty pre-steer bubble, which the branch's own downstream
  `canAttach = msg.isStreaming || (msg.content ?? '') === ''` check already rejects independently
  of the guard — the test passed whether or not the guard existed, meaning it verified nothing
  about that specific line. Fixed by adding an empty-pre-steer-bubble variant, which the OR-empty
  clause of `canAttach` would otherwise wrongly accept — that's the actual case the guard exists
  to prevent, and only that case makes the mutation lethal.
- **#7**: the empty-placeholder coalesce branch (distinct from the general merge branch tested by
  the original replay test) had **zero** test coverage of any kind before this pass — not a weak
  test, an absent one. Added
  `'a mid-turn-steer user entry replayed BETWEEN an empty tool-call placeholder and its text
  prevents coalescing'` (`chat.tool-call-offset.test.ts`), confirmed lethal against the same
  mutation that left #6 (the general branch) caught.

All ten sites are now confirmed genuinely load-bearing, not merely covered. Also incidentally
confirmed the live-UAT ambiguity noted in §8 below (the cancel-race scenario) is a live-network
timing artifact, not a logic defect: mutation #9, run against the exact same code path the live
click exercises, killed the corresponding unit test cleanly and deterministically.

## 8. Live UAT (2026-08-24)

Built the SPA + Go binary from this exact branch (release/v0.1.1, with the fix applied) and ran
it as the real embedded-SPA gateway (`CGO_ENABLED=0 go build -tags goolm,stdjson`, per this
project's documented E2E procedure — not the Vite dev server). Onboarded a real account through
the actual UI and connected a live OpenRouter provider key (operator resolution, ADR §5.2) —
connection verified live ("Connected successfully", model `z-ai/glm-5.2`). Driven via the
Playwright MCP browser tools, not scripted WS frames — the live-provider path was available, so
the scripted-frame fallback was not needed.

**Primary scenario — live ordering (the reported symptom):** sent a long-form request, and while
the reply was genuinely still streaming (`stop-btn` present), sent a mid-turn follow-up via the
composer's real "Send into the running response" affordance. Observed live, via DOM snapshot and
screenshot: `[user question, finished pre-steer reply (Copy button enabled — status done),
follow-up message, new streaming reply continuing on-topic from the follow-up]` — the reply to
the follow-up rendered below it, not above it. Zero console errors throughout. Screenshot
evidence retained locally (not committed — UAT artifact, not source).

**Reload/replay scenario:** immediately after the above turn finished, forced a genuine full page
reload (`window.location.reload()`, not a client-side route change) and re-inspected the DOM.
Result: `[user, user-steer, assistant(final reply only)]` — the pre-steer segment that was shown
live is absent after reload, not merged into the wrong position. This is live, first-hand
confirmation of §1.3's correction and issue #652: the SPA's replay-path fix (§2.2) is doing
exactly what it should with what the backend persists — there is no wrong merge, no bubble
straddling the steer in the wrong order — the gap is that the backend does not persist the
pre-steer narration at all for a no-tool-call steer, a separate, already-filed, already-scoped-out
issue. This live run is independent confirmation that the backend investigation (§1.3) was
correct, not merely theoretical.

**Secondary scenario — cancel immediately after a steer (§2.7/NEW-001):** attempted a live
reproduction (steer, then click Stop with no intervening frame, fired back-to-back via a single
synchronous script). The resulting DOM was ambiguous — consistent with a genuine race between the
live async turn-completion and the deliberately-rapid-fire test script (the request may have
already been finishing server-side before the steer/cancel pair landed), not clearly reproducing
the exact "steer closes bubble, then cancel with truly nothing else having happened" precondition
the unit test isolates deterministically. Rather than over-interpret an inconclusive live result,
this scenario's verification rests on the deterministic, passing unit test (`chat.mid-turn-send.test.ts`,
`'cancelling immediately after a steer does not mislabel the closed pre-steer bubble...'`), which
exercises the exact code path directly without live-network timing confounds. Recorded honestly
here rather than claimed as live-verified — it wasn't, clearly, either way.
