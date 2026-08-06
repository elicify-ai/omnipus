# SPA Gaps — Playwright Test Requirements

This file tracks features referenced by E2E tests that are not yet implemented in the SPA
(`src/`). Each item maps to one or more `test.fixme` calls in the spec files.

---

## Missing Features

- [ ] **Dev-mode banner** (`auth.spec.ts (c)`)
  When `gateway.dev_mode_bypass = true` the SPA does not render a persistent red banner.
  AppShell only shows a `connectionError` banner — not a dev-mode warning.

- [ ] **Version-drift toast + `/api/v1/version` polling** (`version-drift.spec.ts`)
  The SPA does not poll `/api/v1/version` on window focus and does not display a
  "New version available" toast when the build hash changes.

- [ ] **Core-agent locked-field indicator** (`agents.spec.ts (d)`)
  AgentProfile hides the Identity accordion for locked agents (`canEdit` guard at
  `AgentProfile.tsx:353`) — the name input is never rendered, so there is nothing to
  assert as readOnly. The "read-only" badge exists but is insufficient for the test intent.

- [x] **"Agent removed" banner on deleted-agent session** (`agents.spec.ts (g)`)
  RESOLVED: the backend's `getSession` handler (`pkg/gateway/rest.go`) sets `agent_removed`
  on `SessionDetail` when the session's agent no longer resolves; `ChatScreen` renders
  `data-testid="agent-removed-banner"` and disables the composer. Issue #103 closed.

- [x] **Deleted-agent not-found affordance** (`agents.spec.ts (e)`)
  RESOLVED, in a different shape than originally specced: there is no dedicated branded
  404 route — the transient `/_app/agents/$agentId` route opens the AgentProfile
  slide-over and replaces the URL back to `/agents`. The slide-over itself renders a real
  "Agent not found" state with a "Back to Agents" button (`AgentProfile.tsx`'s `isNotFound`
  branch) — the test now asserts that affordance instead of a standalone page. Issue #427
  closed as "different design, not a gap".

- [x] **Offline send queue** (`chat.spec.ts (f)`)
  RESOLVED: the store-level queue (`useChatStore`'s `outboundQueue`/`pendingDrainQueue`/
  `enqueueOutboundMessage`/`drainOutboundQueue`) was already fully implemented and unit-tested
  — the actual gap was `ChatScreen.tsx`'s `inputEnabled` gate requiring strict `isConnected`,
  which silently blocked the only real-UI path into the queue during the
  `reconnecting`/`slow` retry window. Fixed: the composer stays usable while reconnecting
  (matching the pre-existing `composerPlaceholder`/banner/aria-label affordances that already
  assumed this), so messages typed during `context.setOffline(true)` are genuinely queued and
  auto-sent in order on reconnect. Issue #105 closed.

- [x] **Subagent collapsed block UI** (`handoff.spec.ts (b)`, `subagent.spec.ts`)
  RESOLVED by Sprint H (H1+H2): `SubagentBlock.tsx` implements `data-testid="subagent-collapsed"`
  (collapsed header) and `data-testid="subagent-expanded"` (expanded body). The backend now emits
  `subagent_start` / `subagent_end` WS frames; the frontend chat reducer groups frames by span.
  REMAINING GAP: `ToolCallBadge.tsx` lacks `data-testid="tool-call-badge"` on its root div —
  nested badges inside the expanded block are not selectable by testid. frontend-lead must add
  `data-testid="tool-call-badge"` to fix this. Tracks: BDD Scenario 4, FR-H-008.

- [ ] **Agent handoff transcript labels in DOM** (`handoff.spec.ts (a)`)
  AssistantMessage does not annotate each message with the handoff-chain agent's name
  in a way discoverable without `data-testid="messages-list"`.

- [ ] **Handoff depth policy test** (`handoff.spec.ts (c)`)
  Driving 5 real LLM handoffs deterministically in CI is impractical without a mock
  tool that auto-triggers handoffs on a signal.

- [ ] **Skill hash-mismatch error UI** (`skills.spec.ts (b)`)
  SkillBrowser does not expose a file input on the `/skills` route itself. The
  hash-mismatch error dialog is not reachable via a file input on the main page.

- [ ] **Download test via mock media tool** (`media.spec.ts (b)`)
  A deterministic file-download test requires a mock gateway tool that returns a
  non-image media frame. Real LLM instruction is non-deterministic.

---

## Missing `data-testid` attributes (nice-to-have for test stability)

- [ ] Chat composer textarea → `data-testid="chat-input"` (currently uses `aria-label="Message input"`)
- [ ] Send button → `data-testid="chat-send"` (currently uses `aria-label="Send message"`)
- [ ] Stop generation button → `data-testid="stop-btn"` (currently uses `aria-label="Stop generation"`)
- [ ] Assistant messages → `data-testid="assistant-message"` (currently uses `[data-message-role="assistant"]`)
- [ ] User messages → `data-testid="user-message"` (currently uses `[data-message-role="user"]`)
- [ ] Settings tabs → `data-testid="settings-tab-{providers|security|about}"` (currently uses `button[role="tab"]` with text match)
- [ ] Provider "Connected" badge → `data-testid="connected-badge"` (currently uses text match)
- [ ] Login error display → `data-testid="login-error"` (currently uses inline style match)
- [ ] Onboarding error display → `data-testid="onboarding-error"` (currently uses inline style match)
- [ ] Agent card → `data-testid="agent-card-{slug}"` (currently uses `button[aria-label^="View agent"]`)
- [ ] Messages list wrapper → `data-testid="messages-list"`
- [ ] Approval modal → `data-testid="approval-modal"`
- [ ] Agent-removed banner → `data-testid="agent-removed-banner"`
- [x] Subagent collapsed block → `data-testid="subagent-collapsed"` (RESOLVED by Sprint H / H2)
- [x] Subagent expanded body → `data-testid="subagent-expanded"` (RESOLVED by Sprint H / H2)
- [ ] Nested ToolCallBadge inside SubagentBlock → `data-testid="tool-call-badge"` (NEEDED for BDD Scenario 4, E2E rows 20-22)
- [ ] Dev-mode banner → `data-testid="dev-mode-banner"`
- [ ] Version toast → `data-testid="version-toast"`
- [ ] Always-allow toggle → `data-testid="always-allow-toggle"`
- [ ] Build version display → `data-testid="build-version"`
- [ ] Build commit display → `data-testid="build-commit"`

---

- [ ] **LLM chat tests require valid OpenRouter API key** (`chat.spec.ts (a)(b)(d)(e)`, `media.spec.ts (a)`)
  The local test gateway instance returns 401 from OpenRouter ("Missing Authentication header").
  No valid OPENROUTER_API_KEY is configured in the local gateway started for Playwright tests.
  These tests require OPENROUTER_API_KEY_CI set in the CI environment so global-setup can
  configure the provider with a real key. Until then, tests that wait for LLM responses always
  time out. Confirmed from gateway log: "LLM call failed: API request failed: Status: 401".

---

## Routing Gaps

- [ ] `/about` is NOT a separate SPA route — it maps to `/settings?tab=about`.
  `accessibility.spec.ts` uses `/settings?tab=about` (correct) but the original spec
  used `/about` which would 404.

- [ ] `/chat` is NOT a separate route — the chat screen is at `/` (root).
  Tests must use `page.goto('/')` for the chat screen, not `/chat`.
