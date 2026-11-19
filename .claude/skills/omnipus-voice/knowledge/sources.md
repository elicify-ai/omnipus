# Omnipus voice — sources and citations

Last reviewed: 2026-10-31 — all web sources fetched 2026-10-31 via WebFetch.

## Founder data (primary)

| Source | Path | What it gave |
|---|---|---|
| Event/pop-up review | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/notification-review-20261022/review.json` (saved 2026-10-22 08:50Z) | Open questions with his comments (O2 own-action results stay in the UI; O4 network-vs-API split is mandatory; O8 ADR-051 messages are the ones to change), the event catalogue defaults, line-type samples rated "fine", toast comment ("actions like review need to navigate to the right screens"), decisions D1–D29 |
| Message ratings | `.../messages-review.json` (saved 2026-10-22 11:34Z) | Rating scheme (proposal / mine / good / suggestion); his own rewrites for `request_too_large`, `provider_request_rejected`, `unknown`, BE-003, BE-006, BE-008, BE-014; questions on `tool_args` and `agent_not_configured` |
| Full corpus with finals | `.../revised/revised.json` (392 entries) | Current text, proposal, rating, founder text/comment, final wording per message — the source of every sample in knowledge/samples.md |
| ADR-051 catalogue | `.../messages/data/adr051.json` | The 29 current catalogue sentences with `when`/`when_plain`/proposal per code |

## Repo baselines (read in this worktree, 2026-10-31)

| Source | Path | What it gave |
|---|---|---|
| Wording baseline [W] | `docs/internal/specs/notifications-and-chat-system-messages-wording-2026-10-22.md` | The agreed final texts (ADR-051 §1 in full, §2 voice-rule and founder-edit changes); the voice rule R-WD-5…8 statement |
| Requirements [REQ] | `docs/internal/specs/notifications-and-chat-system-messages-requirements-2026-10-22.md` | R-WD-1…9, R-PU-*, R-SL-*, R-DL-*, R-SEC-*, R-EV-2 (network vs API), the pop-up audit (2.1), Decisions Log D1–D62 |
| Notifications spec [S] | `docs/internal/specs/notifications-spec.md` §11–§14 | Line types and colours (§11.1), carrier mapping (§11.2), connection lines (§11.3), detail levels L1–L11 (§12.2), wording rules and catalogue (§13.1–§13.3), voice guard (§13.4), centre titles (§13.6), closed slot vocabulary |
| Brand [B] | `docs/internal/brand/brand-guidelines.md` | Voice personality (sophisticated, protective, calm, reliable), tone spectrum, "no emoji in UI chrome" (root CLAUDE.md brand line; guidelines §6 voice) |
| Transient pattern [N2] | Claude Code 2.1.316, verified from its program text by team-lead; founder's choice (mandate N3, 2026-10-31) | Retry status line `<what is happening> · Retrying in <time> · attempt <n>/<max>`; generic cause first, real cause named after ~3 attempts or when network/certificate is the cause; error lines name host + observed cause and end `Fix: <one concrete action>`; never claims the internet is down from failed requests; proxy-buffering hint for streaming stalls |

## External research (all fetched 2026-10-31)

| Tag | Source | URL | Date | What it contributed |
|---|---|---|---|---|
| R1 | Nielsen Norman Group, "Error-Message Guidelines" (updated May 14, 2023) | https://www.nngroup.com/articles/error-message-guidelines/ | 2026-10-31 | Plain human-readable language, hide jargon and error codes from the main text, describe precisely (no "an error occurred"), constructive advice on fixing, positive non-judgmental voice (avoid "invalid/illegal/incorrect"), avoid humor on repeated errors, show near where the error occurred, never color alone |
| R2 | GOV.UK Design System, "Error message" component (change history through Jan 2024) | https://design-system.service.gov.uk/components/error-message/ | 2026-10-31 | Explain what went wrong and how to fix it in plain English; avoid jargon, blame words ("you forgot"), "please" (implies a choice), "sorry" (doesn't help), "valid/invalid" (adds nothing), "oops"; be specific per failure shape; identical wording across summary and field; read the message aloud as a clarity test |
| R3 | Google Technical Writing, "Error messages" (last updated 2025-03-31 UTC) | https://developers.google.com/tech-writing/error-messages | 2026-10-31 | The two questions (what went wrong, how to fix); identify cause, invalid inputs, constraints; give the fix and examples; be concise; no double negatives; write for the target audience; consistent terminology; neutral tone |
| R4 | Google Technical Writing, "Format error messages to enhance readability" (last updated 2025-03-31 UTC) | https://developers.google.com/tech-writing/error-messages/format-for-readability | 2026-10-31 | Proximity (message near the problem), link out to detail docs instead of long inline text, progressive disclosure, never rely on color alone |
| R5 | Mailchimp Content Style Guide, "Voice and tone" (© 2023) | https://styleguide.mailchimp.com/voice-and-tone/ | 2026-10-31 | Plainspoken, clarity above all; active voice; no slang or jargon; positive phrasing; and the humor rule this skill adopts: never joke with someone who is scared or upset — errors get a straight face |

### Could not be fetched (2026-10-31) — nothing in the skill is attributed to them

| Source | Attempted URL | Result |
|---|---|---|
| Apple Human Interface Guidelines, "Writing" | https://developer.apple.com/design/human-interface-guidelines/writing | Page renders only via JavaScript; fetch returned a title and no body content (two fetchers tried) |
| Material Design 3, "Style guide" | https://m3.material.io/foundations/content-design/style-guide | Same JS-only body; only the title returned |
| Microsoft Writing Style Guide | https://learn.microsoft.com/en-us/style-guide/top-10-tips-style-voice-tone (and /error-messages) | HTTP 404 — pages moved or restructured |
| Atlassian Design, "Voice and tone" | https://atlassian.design/foundations/voice-and-tone/ (and /content/voice-and-tone/) | HTTP 404 on both paths |
| Shopify Polaris, "Error messages" | https://polaris.shopify.com/content/error-messages | 301 to the Polaris API reference; content guidance not reachable at that path |

Per the mandate: no citation was invented for these four/five. If the founder wants
Apple/Microsoft/Material/Atlassian guidance folded in, re-run their fetch (or accept the
summary of these guides' well-known positions only after a successful fetch) and extend
the rule set in SKILL.md.
