# Omnipus voice — worked samples

Last reviewed: 2026-10-31

Twenty real messages from the founder's review data (2026-10-22), spanning the kinds the
review covered: provider unreachable, retry, rate limit, quota, approval needed, task
failed, Stop outcome, model fallback, resource low, connector errors, media/attachment,
unknown failure, offline banner, vague pop-up, own-action save failure, compaction.
Each sample: current text → rewritten text → one-line why.

Source tags: **[F]** founder rating/edit/comment (file and id given); **[W]** wording
baseline (`notifications-and-chat-system-messages-wording-2026-10-22.md`, §1 ADR-051
catalogue / §2 other messages); **[S]** notifications spec §13; **[N2]** the
transient/connection pattern. "Rewritten" always quotes the agreed final wording —
nothing here is newly invented except where marked.

---

### 1. Provider unreachable — retrying (chat, warning, in progress)
Current: `OpenRouter is unreachable — retrying in 8 s (attempt 2 of 5)`
Rewritten: `I can't reach OpenRouter: connection refused. Retrying in 8 s (attempt 2 of 5)…`
Why: voice ("I", in chat), and the observed cause named instead of only "unreachable" —
founder ruled this line renders as an amber warning [F: review.json lineTypes `progress`
comment, "Orange warning in this example" → D45]. The attempt counter stays on every
retry line (founder decision 2026-10-31, Q1 A; `{attempt}/{max}` slots exist in the
closed vocabulary, [S] §13.2 — the countdown-only form in [S] §11.1 is superseded by
the decision).

### 2. Provider unreachable — retries exhausted (chat, error)
Current: `We couldn't reach the model provider. Check your internet connection and retry.`
Rewritten: `I couldn't reach OpenRouter: connection refused. Retry, or switch model if it repeats.`
Why: the old text was one message for both network and API failures — the founder made
the split mandatory ([F: review.json O4 comment: "we need be able to clearly
differentiate network connection issues or api issues"]; D32) and banned "we" (D50).
The exact sentence is the spec's proposed "network, retries exhausted" line [S §13.2];
the split's origin is [W §1 `network`].

### 3. Provider answered with an API error (chat, error)
Current: (same old `network` text as sample 2 — one line for both sides)
Rewritten: `OpenRouter answered with an error (503): "Upstream connect error". Retry, or switch model if it repeats.`
Why: the other half of the network/API split — the provider was reached and refused;
status belongs on the main line here because the provider's own words carry the cause
[W §1; S §13.2 "API, final" row].

### 4. Rate limit (chat, error/warning)
Current: `The model provider is temporarily overloaded. Wait a moment, then retry.`
Rewritten: `{provider} is limiting requests: "{provider_says}". Retry in a moment, or switch model.`
Why: real provider name, its own words quoted, concrete action — founder-rated
proposal [F: messages-review.json `rate_limited` rating "proposal"; W §1].

### 5. Quota / billing (chat, error)
Current: `The model provider says your account is out of credit.`
Rewritten: `{provider} says your account is out of credit: "{provider_says}". Top up your {provider} account, or switch to another provider.`
Why: names the provider, quotes its message, one action with a real alternative —
founder-rated [F: rating "proposal"; W §1]. Note the two actions map to one main action
(Switch model) in the system line, R-PU-4/R-SL-7.

### 6. Attachment the model can't read (chat, error)
Current: `This model can't use that kind of attachment. Try a different file format, or switch to a model that supports it.`
Rewritten: `{model} can't read this attachment. {provider} said: "{provider_says}". Send the file in another format, or switch to a model that reads it.`
Why: the founder's explicit "tht looks fine" on this shape — actor, quote, two concrete
options [F: `media_unsupported` comment; W §1].

### 7. Request too large (chat, error)
Current: `We built a request that was too large for this model to accept. Try shortening your message, or switch to a model with a larger limit.`
Rewritten: `The request was too large for {model}. {provider} said: "{provider_says}". Shorten the message or attachments, or switch to a model with a larger limit.`
Why: the founder's own edit — dropped the "we built" framing for a plain statement of
the situation [F: `request_too_large` rating "mine"; W §1, "dangling dash tidied"].

### 8. Unknown failure (chat, error)
Current: `This turn didn't finish, and we can't tell why. Retry — if it keeps happening, open Verbose chat for details, or try a different model.`
Rewritten: `This reply failed with an error. {provider} said: "{provider_says}". Retry, or switch model if it repeats.`
Why: the founder's own edit — honesty rule in its positive form: when a provider message
*was* received, show it even under the unknown code; "we" removed [F: `unknown` rating
"mine"; W §1 "full stop added after 'error'"].

### 9. Approval needed (chat, needs-you / centre title)
Current (chat line sample): `Mia needs your approval to send an email` — founder: "fine".
Rewritten: unchanged for the chat line; centre title follows the same pattern:
`Mia is waiting for your approval`, body `{tool}: {short summary} in {chat}`.
Why: state what is being approved, by which agent, where — the anatomy applied to a
needs-you message [F: lineTypes `needs` comment "fine"; S §13.6]. Actions like Review
must navigate to the right screen [F: review.json toastComment].

### 10. Scheduled task failed (channel chat, error)
Current: `⚠ Scheduled task {name} failed: {run error text}`
Rewritten: `Scheduled task "{name}" failed: {cause}. Open the app to see the run and try again.`
Why: emoji removed (hard rule 11), raw run error replaced by a plain cause (external
messages never carry raw error text, D27/R-SEC-3 spirit), one action that navigates
[D39; W §2 BE-691].

### 11. Stop outcome (chat, info)
Current: `This turn was stopped before it finished.`
Rewritten: `You stopped this reply.` (shutdown variant: `This reply stopped because I was shut down.`)
Why: lines about the user's own action keep their voice ("You…"); the neutral shutdown
variant states the real cause — and Omnipus makes no promise it cannot keep [W §1
`turn_canceled`, voice rule; S §11.2 maps it to an info line, not an error].

### 12. Model fallback (chat, info)
Current: `Answered by the Fallback model ({text}) because {text2} was unavailable.`
Rewritten: unchanged — founder-rated "good".
Why: a positive exemplar: plain statement of what happened + the observed why, no
blame, no action needed [F: BE-004 rating "good"]. Keep texts like this when they
already satisfy the rules; rewriting for its own sake is drift.

### 13. Resources low (centre title / pop-up) — new text, built with the anatomy
Current: (none — new event kind from the review; today nothing notifies this)
Rewritten: title `Memory is running low` (or `Disk space is running low`), body
`Fewer agents run at once until it recovers.` (memory variant).
Why: title states the fact, body states the consequence the user will notice — no
"system resources low (memory, disk)" jargon dump on the title [S §13.6 proposed titles,
founded on the review's event catalogue; F: review.json events `resources_low` On/On].

### 14. Provider stalled (chat, error)
Current: `The model provider stopped responding: nothing arrived for 5 minutes (or the silence limit set for this provider), so the call was ended. Retry — if it keeps happening, open Verbose chat for details.`
Rewritten: `{provider} stopped sending data for 5 minutes, so I ended the call. Retry; if it happens again, try another model.`
Why: one plain cause with the real number, voice, action; the parenthetical
config detail ("or the silence limit set for this provider") is operator knowledge, not
chat content [W §1 `provider_stalled`, voice rule].

### 15. Message delivery failed (channel chat, error)
Current: `[message delivery failed]`
Rewritten: `I couldn't deliver a message to this chat. It isn't queued. Send it again if you still need it.`
Why: bracket-skeleton text replaced by anatomy: what happened, the honest consequence
(not queued — the user must resend), one action [W §2 BE-037].

### 16. Message dropped — channel overloaded (channel chat, error)
Current: `[message dropped: channel is overloaded, please retry]`
Rewritten: `I dropped a message because this channel was overloaded. Send it again.`
Why: "please" removed (hard rule 9), cause named, the word "dropped" kept because it is
the honest verb — Omnipus did drop it [W §2 BE-038; attachment variant BE-039:
"I dropped an attachment…"].

### 17. Browser can't reach the server (banner, warning)
Current: `Can't reach Omnipus right now. Your agents keep working.`
Rewritten: `Can't reach the server right now. Your agents keep working.`
Why: two connections are separate facts (D13) — this one is the browser↔server
connection, so the product name is wrong here; the reassurance "Your agents keep
working" is honest and stays [W §2 FE-076; N2: never claim more than the evidence shows].

### 18. Device offline (banner, warning)
Current: `Your device isn't connected to the internet. Omnipus reconnects on its own when it's back — your messages and your agents' work are kept.`
Rewritten: `Your device isn't connected to the internet. This page reconnects on its own when it's back — your messages and your agents' work are kept.`
Why: the page reconnects, not the product — the actor named is the thing that actually
reconnects (N2's precision rule) [W §2 FE-077].

### 19. Vague pop-up error (pop-up, error)
Current: `Action failed`
Rewritten: `That action failed, and no reason was given. Try it again.`
Why: the vague-message rule in its honest form — when no reason exists, say exactly
that in so many words instead of a two-word shrug, and give the action [W §2 FE-098;
D7; R2: precise, not vague].

### 20. Own-action save failure (chat, error)
Current: `Sorry, I couldn't save your message — please try again later.`
Rewritten: `I couldn't save your message, so it wasn't sent. Try again. If it keeps failing, check that the data folder has free space.`
Why: "sorry"/"please" removed, the real consequence stated ("so it wasn't sent" — the
user's message did NOT go through), escalation path added [W §2 BE-036].

---

## Counts and coverage

| Kind (mandate list) | Samples |
|---|---|
| Provider unreachable | 1, 2, 3 |
| Retry | 1 (attempt counter per founder decision 2026-10-31) |
| Rate limit | 4 |
| Quota/billing | 5 |
| Approval needed | 9 |
| Task failed | 10 |
| Stop outcome | 11 |
| Model fallback | 12 |
| Resource low | 13 |
| Connector errors | 15, 16 |
| Browser↔server banner | 17 |
| Media/attachment | 6 |
| Unknown failure | 8 |
| Own-action result | 20 |
| Compaction / other system lines | 7, 14, 18, 19 |

All 20 texts quoted above are the agreed wording-baseline sentences ([W]) or the
founder's own edits ([F]); samples 3 and 13 are spec-proposed texts ([S]) and sample 1's
attempt counter carries founder decision 2026-10-31 (the underlying pattern is N2) —
each is marked in place. No sample text in this file is invented beyond what a cited
source states.
