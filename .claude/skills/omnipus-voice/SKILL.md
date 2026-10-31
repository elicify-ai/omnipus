---
name: omnipus-voice
description: The Omnipus voice — load before writing or rewriting ANY user- or operator-facing product message a person sees — inside or outside the app: notification-centre entries, pop-ups, chat system lines, errors, warnings, empty states, confirmations, banners, Settings help text, and later external notifications. Produces messages consistent enough that the founder reviews the pattern, not every message (founder decision N3, 2026-10-31). Carries the wording rules R-WD-1…9 from the notifications requirements, the agreed wording baseline from the founder's message review (2026-10-22), the transient/connection-failure pattern (N2), and researched UX-writing practice (NN/g, GOV.UK, Google tech-writing, Mailchimp). Sources for every rule are cited inline; the worked sample set (20 real messages, current text → rewritten) is in knowledge/samples.md, full source URLs and dates in knowledge/sources.md. Load when authoring product prompt text (pkg/coreagent, pkg/sysagent, pkg/skills/embedded, pkg/tools Description text), when writing any frontend or backend string a user or operator reads, or when the founder asks for a wording change.
---

# Omnipus voice

Last reviewed: 2026-10-31 — created from the founder's message review (2026-10-22) and the notifications wording baseline
Process version: 2026-10-29.1

This skill states how Omnipus talks. Apply it from the first word of any user- or
operator-facing message. The founder reviews the pattern, not every message (N3,
2026-10-31), so the rules below are the pattern — a message that breaks one is a defect,
not a style choice. When this skill and a dispatch disagree, this skill wins except on a
direct founder instruction. It never overrides a contract fact: slot names, caps and
catalogue structure are owned by `contracts/components/schemas/LLMError.yaml` and the
notifications spec, cited below.

## Sources (one line each; URLs and fetch dates in knowledge/sources.md)

- **[F] Founder review data** — `review.json`, `messages-review.json`, `revised/revised.json`
  under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/notification-review-20261022/`
  (his ratings, edits and comments on 392 messages; the primary source).
- **[W] Wording baseline** — `docs/internal/specs/notifications-and-chat-system-messages-wording-2026-10-22.md`
  (the agreed final texts; unrated proposals count as agreed, D49).
- **[S] Notifications spec** — `docs/internal/specs/notifications-spec.md` §11–§13
  (line types, detail levels L1–L11, voice guard, slot vocabulary, centre titles).
- **[REQ] Requirements** — `docs/internal/specs/notifications-and-chat-system-messages-requirements-2026-10-22.md`
  (R-WD-1…9, R-PU-*, R-SL-*, Decisions Log D26–D50).
- **[B] Brand** — `docs/internal/brand/brand-guidelines.md` (voice personality; no emoji).
- **[N2] Transient/connection pattern** — Claude Code 2.1.316's retry and error lines,
  verified from its program text by team-lead; chosen by the founder (N2) as the pattern
  for transient and connection failures.
- **[R1–R5] External research** — NN/g error-message guidelines, GOV.UK design-system
  error messages, Google technical-writing error-message guidance (2 pages), Mailchimp
  voice and tone. Apple HIG, Material 3, Microsoft Style Guide and Atlassian pages could
  not be fetched on 2026-10-31 (JS-rendered or moved) — nothing below is attributed to them.

## The voice

Omnipus sounds like a calm, competent operator sitting next to you: it says what
happened in plain words, states only causes it actually observed, and always leaves you
with one concrete thing you can do — or a clear statement of what it is already doing
about it. Inside a chat it speaks in the first person ("I couldn't reach OpenRouter"),
everywhere else it is neutral ("Live video isn't available…"); it never hides the
provider's own words behind a polite custom sentence, never blames, and never
exaggerates what it knows. Sophisticated, protective, calm, reliable [B] — but the
register is a one-line status message, not marketing.

## Hard rules (each with a Do / Don't pair)

1. **One voice per surface.** In a chat (web or channel): the agent speaks as "I";
   "Omnipus", "we", "our", "us" never act (R-WD-5/7, D47/D50). Everywhere else
   (centre, pop-ups, banners, settings, external): neutral voice — the thing failed, no
   first person (R-WD-6). A message used on both surfaces has two forms (R-WD-8).
   The only allowed product name is the founder's bug-report line
   "please report it to the Omnipus.ai team" (D48).
   - Do (chat): `I couldn't reach OpenRouter: connection refused. Retrying in 8 s…`
   - Don't (chat): `Omnipus can't reach OpenRouter…` · `We couldn't reach the model provider…`
   - Do (centre/pop-up): `{agent}'s working folder ({path}) could not be opened: {reason}.`
   - Don't (centre/pop-up): `I couldn't open my working folder…`
2. **Anatomy: what happened · why (observed) · what you can do — or what I'm doing.**
   Every message answers at least two of the three parts; omit the "why" unless it was
   actually observed, omit the "what you can do" when there is genuinely nothing to do
   and say what Omnipus is doing instead (R-WD-1, D26).
   - Do: `This conversation was too long for the model. I'm shortening the earlier part
     and trying again. You don't need to do anything.`
   - Don't: `Something went wrong — please try again.` (no what, no why, no who acts)
3. **Show the provider's own message, quoted, when one was received** — never hidden,
   never force-replaced by a custom text (R-WD-2, D28). Our short sentence accompanies
   it; secrets masked (R-SEC-1); main line shows ≤ 300 code points + "…" (S §13.3).
   A sentence never renders empty quotes or an empty slot (S §13.2).
   - Do: `OpenRouter refused the request: "Insufficient balance". Top up your OpenRouter
     account, or switch model.`
   - Don't: `There was a problem with your request.` (provider's words thrown away)
4. **Name the side that failed: network vs API.** A network failure is "the provider
   could not be reached at all"; an API failure is "the provider answered with an
   error" (R-EV-2, D32, REQ §4 vocabulary). Never diagnose beyond the evidence.
   - Do: `I can't reach OpenRouter: connection refused.` / `OpenRouter answered with an
     error (503): "…".`
   - Don't: `OpenRouter is having problems.` (one sentence for both sides) ·
     `The internet is down.` (claimed from failed requests — N2: never)
5. **No blame.** The message states the situation, not the reader's failure; no
   "invalid", "illegal", "you forgot", "your fault" phrasing [R1, R2]. Where the fault
   is genuinely Omnipus's, say so in one clause and move on.
   - Do: `The request was too large for {model}. {provider} said: "…". Shorten the
     message or attachments, or switch to a model with a larger limit.`
   - Don't: `You sent an oversized request that the model rejected.`
6. **No jargon, no internal ids, no error codes at Normal level.** Status codes, request
   ids, raw payloads and file paths like `gateway.log` are technical details — they
   belong in the Verbose/Debug "Technical details" disclosure (L9, D25), not on the main
   line. Name real things instead: provider, model, agent, tool.
   - Do: `We sent the model provider a request it rejected (HTTP 400: Bad Request; format). Open Verbose chat for technical details.` → better:
     `{provider} rejected the request for {model}: "{provider_says}". Retry; if it
     repeats, please report it to the Omnipus.ai team.`
   - Don't: `Request failed: 0x80070057` · `enqueueSteeringFromMessage: session … is stopping`
7. **Always one way forward.** Every error and warning ends with one concrete action the
   reader can take, or one statement of what Omnipus is doing about it. Pop-up and
   system-line actions: at most one, and it navigates to the screen where the item is
   handled (R-PU-4, D39, R-SL-7).
   - Do: `… Top up your OpenRouter account, or switch to another provider.` (action: Switch model)
   - Don't: `… Sorry about that.` (apology is not a path forward)
8. **Plain, specific, short.** One to three short sentences; every sentence earns its
   place. Vague words ("error occurred", "something happened", "unexpected") appear only
   when honestly nothing more is known — and then the message says so in so many words
   (D7, [R2] be specific; [R3] what went wrong + how to fix).
   - Do: `That action failed, and no reason was given. Try it again.`
   - Don't: `Action failed` · `Oops! Something went wrong.`
9. **Tone stays level.** Sentence case throughout (the whole agreed corpus uses it).
   No exclamation marks, no "please", no "sorry", no "oops", no humour in failures —
   a person reading an error is not entertained [R2, R5; F: corpus has none].
   - Do: `Scheduled task "Nightly sync" failed: the mail server refused the connection.
     Open the app to see the run and try again.`
   - Don't: `Oops — we couldn't run your task! Sorry! Please try again later.`
10. **Numbers, times, units from the corpus:** a space between number and unit; seconds
    "8 s", minutes "5 minutes"; counts "{n} of {m} retries", "{n} chats waiting";
    durations of resolved incidents stated plainly ("after 2 retries (14 s)").
    - Do: `Retrying in 8 s (attempt 2 of 5)…` · `Connection to OpenRouter restored after
      2 retries (14 s).`
    - Don't: `Retrying in 8s (2/5)` · `restored after approximately 14.37 seconds`
11. **No emoji** — not in stored data, not in UI chrome, not in notifications, titles or
    system lines [B; F: BE-691's "⚠" was removed in the rewrite]. Icons and colour
    tokens carry severity, never text decorations (S §11.1).
    - Do: `Scheduled task "Nightly sync" failed: …`
    - Don't: `⚠ Scheduled task "Nightly sync" failed: …`
12. **Dynamic parts are real values.** A placeholder renders the actual provider, model,
    agent, tool, path, seconds, count — and when a value is unknown, the sentence built
    for that case is used instead (no empty slots, no empty quotes; S §13.2/§13.3).
    - Do: `Mia is waiting for your approval` (centre title, real agent name)
    - Don't: ` is waiting for your approval` · `{agent} needs you` rendered literally.

## The N2 pattern — transient and connection failures

The founder chose Claude Code's pattern (verified from its program text, N2) for every
transient/connection message. Its rules:

- **Retry status line** while it lasts: `<what is happening> · Retrying in <time> ·
  attempt <n>/<max>` — all three facts, every attempt. In Omnipus system-line form:
  `I can't reach OpenRouter: connection refused. Retrying in 8 s (attempt 2 of 5)…`
  as an animated warning at every detail level (R-SL-5).
- **Generic first, specific later.** Early attempts keep the cause generic; name the
  real cause once it is confirmed — after ~3 attempts, or immediately when the cause is
  the network or a certificate. In Omnipus terms: the `{reason}` words are fixed and
  plain ("connection refused", "its address could not be found", "it did not answer in
  time"; S §13.2).
- **Failure line names host + observed cause + one Fix.** Error lines end with a separate
  `Fix: <one concrete action>` clause — e.g. "Fix: check your network or proxy".
- **Never claim the internet is down from failed requests.** A failed request is
  evidence that *this* connection failed, nothing more. Loss of the server connection is
  its own message (browser ↔ server is a separate surface, D13/D18).
- **Resolved lines say the cost:** `Connection to OpenRouter restored after 2 retries
  (14 s).` — Verbose record line; gone from Normal history once resolved (R-SL-5, D42).

## Per-surface rules

| Surface | Voice | Anatomy focus | Notes |
|---|---|---|---|
| Chat system line (info/warning/error/resolved/needs-you) | Agent's "I" | Full anatomy; in-progress state for anything still running | One line of text, not a card (R-SL-2); ≤ 1 action (R-SL-7); provider quote on the main line; technical details only Verbose+ (L9) |
| Notification centre entry | Neutral | Title = what happened in a few words; body = one short sentence of cause/consequence | Title conventions in [S] §13.6 ("{provider}: account out of credit", "Memory is running low"); body never raw error text for schedules (R-EXT / D27); every entry navigates to where it is handled (R-NC-6) |
| Pop-up (transient) | Neutral | One outcome + one action | Results of the user's own actions never pop up — the page shows them (R-PU-5, D31); max 1 main action that navigates (R-PU-4); durations by urgency are a spec fact (R-PU-1), not wording |
| Banner / app-wide indicator | Neutral | The connection fact + reassurance about kept work | "Your agents keep working" belongs exactly here, not on provider errors |
| Chat error reply (turn failed) | Agent's "I" | Anatomy + provider quote | The failed reply is the message; follow the agreed catalogue for its code ([W] §1) |
| Settings help text | Neutral | How the setting works, what it affects | One or two plain sentences; no marketing adjectives; [derived — not founder-reviewed] |
| Operator-facing (logs written for operators, diagnostics screens) | Neutral, precise | Facts: ids, codes, durations | This is the one surface where codes and paths belong (L9 spirit); still no blame and no emoji |
| External notification (later, Part 2) | Neutral | Title + link only | Never chat content, file names, technical details, error payloads (R-SEC-3, D27); replies to a notification channel are one fixed sentence (R-EXT-8) |
| Own-action results ("Saved", "Could not save") | Neutral | Inline, next to where the user acted | Never a pop-up, never the centre (R-PU-5, D31, F/O2); failed saves get an inline Retry |

Error vs warning vs info: **error** = something the user asked for did not happen
(action available); **warning** = something degraded but work continues or a retry is
running (in-progress animated); **info** = a state change worth knowing, no action;
**resolved** = a prior problem ended (states the cost); **needs-you** = the flow stops
until the user acts (state what is being approved, by which agent). [S] §11.1, [W] line types.

## Detail levels and wording

The wording of the main line never changes with the detail level — Normal must read
complete on its own (L3). What changes is only what is *added*: technical details
(status code, request id, masked payload) appear at Verbose collapsed and Debug open
(L9, D25/D43); timings, token counts and cost at Verbose+ (L10); raw provider traffic
and internal ids Debug only (L11); retries and connection lines show while happening at
every level and leave a record line at Verbose/Debug (L8, D42). Normative table: [S] §12.2.

## Naming things

- **Providers and models** by their real names ("OpenRouter", "deepseek/deepseek-v4.1-flash")
  — the user picked them by these names. Model ids render as configured, no pretty-fying.
- **Agents** by the name the user gave them ("Mia"); never "the agent" when the name is known.
- **Tools** by their product name ("the browser tool"), not the internal verb
  (`web_navigate` → "the browser tool").
- **Omnipus** as a word never acts in a message (rule 1) — but naming it in Settings or
  marketing copy is not a system message and is out of this skill's scope.
- Placeholders come from the closed slot vocabulary only ([S] §13.2:
  `{provider} {model} {provider_says} {reason} {status} {n} {seconds} {minutes} {tool}
  {agent} {path} {model_requested} {model_used} {limit_name} {value} {task} {attempt}
  {max} {countdown} {answered_model} {unavailable_model}`).

## Self-check before committing a message

1. Surface voice correct — "I" in a chat, neutral outside, no "we/our/us", no "Omnipus"
   as actor (the one D48 bug-report line excepted)?
2. Anatomy present — what happened stated plainly; why included **only if observed**;
   one action or an explicit "what Omnipus is doing"?
3. Network vs API told apart when it is a provider/connection failure — and nothing
   claimed beyond the evidence (no "internet is down")?
4. Provider's message quoted when one arrived; no empty quotes; no empty slots; secrets
   masked?
5. No jargon, ids, codes or paths on the main line at Normal level — pushed to technical
   details?
6. No blame words; no "please"/"sorry"/"oops"; no exclamation marks; no emoji?
7. Sentence case; numbers/units in corpus form ("8 s", "attempt 2 of 5"); length one to
   three short sentences?
8. Action count ≤ 1 and it navigates to the screen where the item is handled?
9. Both voice forms exist if the message can appear in and outside a chat?
10. Read it aloud once — could a non-engineer act on this without asking anyone? [R2]

## How to rewrite an existing message

1. **Find the surface and kind** — which table above does it live in; which of the five
   line types (or pop-up/centre/banner) is it?
2. **Find what the code actually knows** — read the producer before writing: what cause
   is genuinely available at this point (a status? a reason word? nothing?). The rewrite
   may only state what exists (rule 2's honesty clause; also root `CLAUDE.md`,
   "documented but unwired" discipline).
3. **Start from the agreed corpus** — if a catalogue or baseline text covers this
   situation ([W] §1/§2, [S] §13.2/§13.6), adapt it rather than inventing.
4. **Apply the anatomy**, then the hard rules 1–12 in order; most rewrites are two
   changes: voice per surface, and cause-or-action restored.
5. **Write both forms** when the message can appear in and outside a chat (R-WD-8).
6. **Check the mechanics** — slots from the closed vocabulary; no empty renders; length;
   sentence case. Then run the self-check above. If the message belongs to the
   ADR-051/LLMError catalogue, the change goes through the catalogue and its copy-rule
   tests, not a string swap — hand the text change to the owning developer with the
   catalogue row named; do not edit generated files (root `CLAUDE.md`, "Contract
   regeneration").

## Knowledge files

- `knowledge/samples.md` — 20 worked samples from the founder's review data, every kind,
  current text → rewritten text, one-line why each.
- `knowledge/sources.md` — full citations: founder data paths, baseline docs, the N2
  pattern's origin, and the external research (URLs, fetch dates, what was taken from each,
  and which named sources could not be fetched).

## Reporting

Wording work ends with the standard evidence table: one row per message changed or
decided (claim, evidence — baseline row, spec section or file::symbol — certainty),
plus the self-check row listing which self-check items you ran. A rewrite whose evidence
is "it reads better" is not evidence; point at the rule number it satisfies or violates.
