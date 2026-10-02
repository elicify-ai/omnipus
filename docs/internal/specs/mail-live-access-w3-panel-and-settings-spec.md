# Feature Specification: Mail live access — W3 Mail panel and settings surface

**Created**: 2026-10-02
**Status**: Proposed — draft for the grill. This is the W3 implementation specification for the
founder-approved design in `docs/internal/architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md`
(cited below as "the ADR"). It has not been grilled, gated or approved for implementation.
**Input**: the ADR (one prescribed grill round applied — findings I-01…I-06, M-01, M-02; founder answers
Q1=A, Q2=B, Q3=A, Q4=A, Q5=A recorded 2026-10-02) and its review
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-report.md`.
**Work package**: W3 — panel/settings (frontend-lead), per the ADR's "Affected components and parallel
work packages" table. This spec covers design work package W3, the frontend half of W4's gateway-facing
request shapes, and the frontend parts of the ADR's feature sections F1, F2 and F7.
**Sibling specs this one must not edit**: W8's Library-viewer spec (renderer/resource-policy
implementation), W0's contract change set, W1/W2 backend read-runtime specs, W7's Save-service spec.
Interfaces this spec publishes to, and consumes from, those packages are frozen in §3.

**Certainty convention used throughout**: **Verified** = the file/symbol was read in THIS checkout
(worktree `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail`, branch
`docs/adr-mail-live-access`). **Inferred** = an architectural consequence or recommendation, not a tested
result. **Unknown** = a genuine evidence gap; no number, provider or behaviour is invented to fill one.
GitNexus MCP tools were not exposed in this session (same gap the ADR and its grill record); direct
Read/Grep sweeps ground every impact row, which are therefore labelled Inferred.

---

## 1. Summary and scope

Mail today makes the user wait for a live IMAP round trip before a single row renders, re-fetches on a
30-second timer that the new design retires, shows at most one 20-row page with no way forward, renders an
unknown folder count as a bare `0`, and offers no attachment action except a browser download. This spec
defines the panel half of the founder-approved fix: the Mail panel shows labelled cached rows immediately,
validates them with **at most one** explicit live-refresh request per eligible event, distinguishes the
four data sources honestly, pages 25 rows at a time to a 200-row ceiling with a reachable search path
beyond it, exposes the existing per-mailbox Sent/Drafts folder-name settings, tells the gateway when a
panel is open through authenticated presence frames, and hands attachments to the Library viewer as
temporary, read-only, resource-constrained previews with a fully specified keyboard and screen-reader
contract.

In scope (all frontend, in the ADR's W3-owned files unless a boundary says otherwise):

- Cache-first display and event-driven freshness for the folder rail, the message list and the reading
  pane — replacing the D25 30-second panel refetch (the ADR's Authority table supersedes it).
- Freshness labelling and semantics for the four sources (`live`, `memory`, `encrypted_disk`, `none`),
  each with a nullable last-validated time and a stale flag; unknown count never renders as zero.
- Paging: 25 rows per page, Load more adds 25, a hard ceiling of 200 rows per folder per view, and a
  reachable folder-scoped search path beyond it.
- The visible folder-name override surface, reusing the existing `sent_folder_name` / `drafts_folder_name`
  configuration fields, with "automatic" spelled out on screen.
- Panel-presence frames over the authenticated gateway WebSocket: open/close, lifecycle (tab close,
  navigation, logout, socket loss) and several tabs.
- The attachment row changes (paperclip indicator in list rows, Open/Save in the reading pane) and the
  Mail-to-Library handoff: context bar, focus, announcements, keyboard and narrow-screen behaviour (grill
  finding I-06).
- The temporary-source resource policy the handoff carries (grill finding I-04) and the scripts-off
  default with per-file checkbox for saved mail-derived HTML (founder Q5=A) — as requirements and
  acceptance criteria; the renderer-side implementation belongs to W8's owned files.
- Every user-visible state of the above with its exact text.

Out of scope for this spec (owned elsewhere; boundaries in §3):

- Everything behind the gateway: pooling, budgets, the cache itself, discovery, the publication revision,
  the preview byte endpoint, the Save service (W1/W2/W4/W7).
- The Library viewer's internal temporary-source implementation, renderer adapters and resource resolver
  (W8) — this spec publishes the handoff descriptor and the interaction contract both sides honour.
- Contract YAML edits and regeneration (W0, Hard Constraint #8) — this spec names the generated types W3
  consumes and the ones W0 must add, and consumes nothing until regenerated.
- Phase 2 JMAP transport selection UI, the Connectors transport-preference control (a later wave), the
  removal/cleanup-pending UX, and the summary screen (unchanged this phase except where the ADR says it
  stays independent).
- Any layout, brand or visual redesign — "Sovereign Deep" stays as shipped; the ADR forbids one and so
  does this spec.

---
