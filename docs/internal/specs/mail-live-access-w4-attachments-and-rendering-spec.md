# Implementation Specification: Mail live access W4 — attachments, agent tools, styling, reply and dates

**Created**: 2026-10-02
**Status**: Proposed — awaiting the prescribed grill rounds. Design is settled: [ADR-20261001 — Mail live access: pooled connections, folder discovery and a bounded cache](../architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md) (correction round applied), its review `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-report.md` (findings I-01–I-06, M-01–M-02 applied), and founder answers **Q4=A / Q5=A** recorded 2026-10-02 in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/mail-feature-decisions.md`.
**Work package**: the "sender, attachments and rendering" half of the Mail live-access feature — design work packages **W7–W10** plus the F1–F6 feature rules in the ADR's "Sender, attachments and rendering" section. Closes #1170 (Save/Download), #1171 (agent attachment tools), #1172 (styling), #1173 (Reply all), #1174 (preview half — Open without saving), #1175 (date fallback).
**Out of this spec (other package)**: pooled sockets, folder discovery, the bounded cache and panel refresh (W1/W2/W3/W4-core — the ADR's Phase 1/2 decisions); the list paperclip indicator and its `has_attachments` metadata fetch (W2/W3); contracts themselves (W0 — this spec names shapes, never edits them).
**CSS authority**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/css-allowlist.md` (security-lead, 2026-10-02) — the 73-property allow-list, deny-list with named attacks, container analysis and proof obligations P1–P9 are adopted as-is; this spec does not widen them.
**Certainty labels**: **Verified** = read in this checkout during spec writing; **Inferred** = design consequence, not tested; **Unknown** = open evidence gap. No test, build or browser run was performed by this spec.

---

## Bottom line

This package makes six things real, all designed already and none implemented today: a user can **Open** a mail attachment and read it in the Library viewer without a file being written anywhere; **Save to Library** lands it as a real workspace file (with browser Download staying a separate, unchanged action); an agent can list and read attachments freely and save with the ordinary `ask` permission; email styling survives with a parsed, allow-listed CSS sanitizer instead of today's strip-everything behaviour; **Reply all** stops dropping the other recipients and quotes the original; and a message without a Date header shows its received date or "No date" — never "1 Jan 1".

Two grill findings shape the hardest parts: **I-04** (sender-authored markup in a temporary preview must not be able to fetch same-gateway resources — every reused renderer gets an explicit source-scoped resource policy) and **I-05** (the capped preview and the unlimited browser Download must be two distinct server-enforced byte resources, or one of the two promises breaks). Both are specified as testable contracts below, not good intentions.

---

## 1. Summary and scope

### 1.1 What this package delivers

| # | Feature | Issue | Design section | One-line outcome |
|---|---|---|---|---|
| 1 | Open an attachment without saving (temporary Library preview) | #1174 | F1 | View any supported attachment in the Library viewer; nothing is written to disk; Back returns to mail. |
| 2 | Save to Library, keep browser Download | #1170 | F2 | Save lands `mail/<mailbox>/<year-month>/<sanitized-name>` as a real Library file with audit + "Open in Library"; Download stays a browser download. |
| 3 | Agent attachment tools | #1171 | F3 | `list_email_attachments` / `read_email_attachment` shipped `allow`; `download_email_attachment` shipped `ask` with ordinary Auto-approve; `read_message` gains the attachment list. |
| 4 | Email styling (parsed CSS sanitizer) | #1172 | F4 | Safe colour/font/table/background/media-query styling renders; scripts, remote loads, `@import`, `@font-face`, `position` never do. |
| 5 | Reply all + quoted original | #1173 | F5 | Reply all keeps original To/Cc (minus self, de-duplicated); the compose draft quotes the original; one shared recipient rule for panel and agent. |
| 6 | Message-date fallback | #1175 | F6 | Valid Date → server internal date → "No date". Never a zero date. |

### 1.2 Explicitly in scope

- The temporary content-source seam in the Library viewer (stored entry vs temporary mail attachment) and its read-only capability rules.
- The capped preview-purpose byte resource and the distinct browser-Download streaming path (grill I-05), including late-failure ordering.
- The temporary-Mail resource policy passed to every reused renderer (grill I-04).
- The Mail→Library handoff interaction contract: focus, announcements, keyboard, reflow (grill I-06).
- The shared attachment transfer/save service used by panel and agent alike, its save-operation-token reconciliation for a lost response (grill M-02), its audit event, and its refusal cases.
- The saved mail-derived HTML profile (founder Q5=A): original bytes, persisted mail-derived marker, scripts off by default, per-file scripts checkbox, marker provenance across move/copy/rename/restore.
- The parsed CSS policy: allow-list from the security artefact, `<style>`-block pass, `url()` pin-then-rewrite extension, and the two data:-URI paths where the sanitizer is the only gate.
- The extracted transport-neutral reply-recipient helper and the gateway reply-context operation.
- The nullable effective message date across transport, contract and display.
- Registration/policy wiring for the three new tools at every Hard Constraint #6 touch point.

### 1.3 Explicitly out of scope (non-goals — detail in §10)

No pooled sockets, no folder discovery, no header cache, no panel refresh timers (W1–W4 core). No new Office-document renderer. No attachment type blocklist, antivirus scanner, or attachment-specific approval layer. No outbound-mail or stored-signature policy widening. No byte cache of previews, bodies or parts — not even an encrypted one. No change to browser Download's destination or disposition. No JMAP identity work beyond consuming the first-phase `message_ref` that the read-runtime package issues.
