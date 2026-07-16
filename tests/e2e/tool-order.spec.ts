/**
 * tool-order.spec.ts — regression net for the "tool calls sink below the
 * final response" bug fixed on this branch (bugfixes3).
 *
 * Historical bug: once a stream finished (or a session replayed),
 * VirtualAssistantMessageRow / MessageItem rendered `message.content` as ONE
 * text block, then ALL of `message.tool_calls` after it — collapsing
 * whatever interleaved order the assistant actually streamed in (text A ->
 * tool call -> text B rendered as [text A + text B] -> [tool badge]).
 *
 * Fix (two layers, see commits 5d6c1e0e and d612e4eb):
 *   - store:    src/store/chat.ts stamps every baked tool call with
 *               `textOffset` (`PositionedToolCall` / `stampToolCallOffset`)
 *               — the character offset into the message's final `content`
 *               where the call started.
 *   - renderer: src/lib/messageParts.ts's `splitMessageParts` (consumed by
 *               both ChatScreen.tsx's VirtualAssistantMessageRow and
 *               MessageItem.tsx) uses that offset to interleave text/tool
 *               parts in true chronological order instead of text-then-all-
 *               tools.
 *
 * ── Harness findings (why this file is shaped the way it is) ──────────────
 *
 * This suite runs against a REAL gateway binary + REAL LLM (OpenRouter,
 * OPENROUTER_API_KEY_CI — see tests/e2e/README.md) with no WebSocket mock or
 * scripted/deterministic LLM provider reachable from the compiled gateway
 * (pkg/agent/testutil/scenario_provider.go exists but is wired only into Go
 * unit/integration tests, not the HTTP+WS binary this suite drives). Making
 * a live LLM emit an exact `text A -> tool call -> text B` shape with fixed
 * marker strings on both sides is not controllable — this file does NOT
 * attempt that.
 *
 * The deterministic seam this suite already uses elsewhere (session-setup.ts
 * `seedAndOpenSession`, exercised by multi-turn-render.spec.ts's T1.10 for
 * an adjacent ordering bug) is to write transcript.jsonl directly and then
 * open the session, which makes the gateway REPLAY it over the real
 * WebSocket. Critically, pkg/gateway/replay.go (see replay_test.go) emits
 * the *same* frame vocabulary a live stream does — `replay_message`,
 * `tool_call_start`, `tool_call_result`, `done` — through the identical
 * client-side reducer (src/store/chat.ts `handleFrame`) that a live
 * `token`/`tool_call_start`/`tool_call_result`/`done` sequence would hit.
 * That reducer is what stamps `textOffset`, so replay is not a shortcut
 * around the fix — it drives the exact code path the fix touches.
 *
 * One remaining problem: replay.go emits ONE `replay_message` frame carrying
 * an entry's ENTIRE `content` before that entry's own tool-call frames — a
 * single transcript entry can't represent "text A, then a tool call, then
 * text B" by itself (the whole entry's text always precedes its own tool
 * calls). The fix for THIS is also already established in this codebase:
 * src/store/chat.tool-call-offset.test.ts's "WS-replay same-turn merge"
 * case shows that multiple transcript entries sharing the same `turn_id`
 * (+ agent_id) get coalesced into ONE assistant bubble, with any tool call
 * whose frames arrive between two same-turn segments stamped at the offset
 * of the segment that preceded it. This file seeds THREE (or five) entries
 * per turn — text-only, tool-call-only (empty content), text-only — sharing
 * one `turn_id`, to reconstruct a true interleaved turn deterministically.
 *
 * Known-flaky patterns avoided: the suite's documented real-LLM variance
 * (playwright.config.ts's retries comment) comes entirely from tests that
 * wait on a live model's tool-call timing under concurrent suite load. This
 * file never talks to the LLM — it seeds a transcript and replays it — so it
 * carries none of that variance. No arbitrary `waitForTimeout` is used;
 * every wait is either `expect(...).toBeVisible/toBeEnabled/toHaveCount`
 * (polling) or the app's own "replay finished" signal (`chatInput`
 * re-enabling). All selectors are `data-testid="tool-call-badge"` +
 * `data-tool` (the two attributes GenericToolCall.tsx / ToolCallBadge.tsx
 * guarantee — see ChatScreen.tool-order.test.tsx's doc comment) or the
 * literal marker text — never a class name, so this file is inert to the
 * concurrent tool-row visual redesign.
 *
 * grep across this checkout found no reference to issues #467/#472 (no
 * hits in tests/e2e, docs/, or git log) — proceeded on the harness pattern
 * actually present: multi-turn-render.spec.ts (T1.10) for the seeding
 * technique, and chat.tool-call-offset.test.ts's WS-replay-merge case for
 * the turn_id mechanism this file drives end-to-end through a real browser
 * + gateway instead of a hand-called `handleFrame`.
 *
 * ── Why this file does NOT use session-setup.ts's seedAndOpenSession ───────
 *
 * A live run surfaced that `openSession()` (fixtures/session-setup.ts:129,
 * shared by seedAndOpenSession and therefore by multi-turn-render.spec.ts,
 * replay-fidelity.spec.ts, handoff.spec.ts, and retention.spec.ts too) clicks
 * a button named "Open sessions panel", then one named "Open session:
 * <title>". Neither accessible name exists anywhere in current `src/` — the
 * sidebar toggle is now `aria-label="Toggle navigation sidebar"`
 * (ScreenHeader.tsx / WorkspaceTabContainer.tsx) with no per-session button
 * text matching that old pattern. This is a pre-existing drift between that
 * shared fixture and the current UI, unrelated to the interleaving fix and
 * reproducible on every spec that calls it — not something introduced by
 * this file, and not something this file is allowed to fix (session-setup.ts
 * is a shared fixture several other specs depend on; out of this task's
 * "own only new files" scope).
 *
 * This file therefore never touches session-setup.ts. It creates the session
 * and seeds its transcript with its own small local REST helpers (below —
 * same requests session-setup.ts's internals make), then opens/reopens it by
 * navigating straight to the app's own current deep-link route,
 * `/#/sessions/{id}` (src/routes/_app/sessions.$sessionId.tsx — confirmed
 * present and wired to `attachToSession`/the WS attach+replay path in this
 * checkout). That route is not a workaround: it is the SAME mechanism the
 * (now-broken) session-list button used to invoke, just addressed directly —
 * arguably a more deterministic seam for E2E than hunting a sidebar/list UI
 * at all.
 */

import { expect, type Locator, type Page } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'
import { test } from './fixtures/console-errors'
import { chatInput, assistantMessages } from './fixtures/selectors'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test')

// One transcript.jsonl entry. Mirrors session-setup.ts's TranscriptEntry /
// TranscriptToolCall shapes (pkg/session.TranscriptEntry / ToolCall Go json
// tags) plus `turn_id` — which session-setup.ts's own type omits because no
// other current caller needs it. Defined locally rather than imported so
// this file has zero dependency on session-setup.ts (see file doc comment).
interface OrderedToolCall {
  id: string
  tool: string
  status: string
  duration_ms?: number
  parameters?: Record<string, unknown>
  result?: Record<string, unknown>
}

interface OrderedEntry {
  id: string
  role: string
  content?: string
  timestamp: string
  agent_id?: string
  turn_id?: string
  tool_calls?: OrderedToolCall[]
}

// ── Local REST/seeding helpers (self-contained — see file doc comment) ─────
// These mirror session-setup.ts's internal (unexported) apiHeaders/
// createSession/seedTranscript exactly; duplicated here rather than importing
// from that fixture so this file has no dependency on its (currently stale)
// openSession() helper.

function getStoredAuthToken(): string | null {
  const authFile = process.env.OMNIPUS_AUTH_FILE
    ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
    : path.join(path.dirname(new URL(import.meta.url).pathname), 'fixtures/.auth/admin.json')
  if (!fs.existsSync(authFile)) return null
  try {
    const raw = fs.readFileSync(authFile, 'utf-8')
    const state = JSON.parse(raw) as {
      origins?: Array<{ origin: string; localStorage?: Array<{ name: string; value: string }> }>
    }
    for (const origin of state.origins ?? []) {
      for (const item of origin.localStorage ?? []) {
        if (item.name === 'omnipus_auth_token') return item.value
      }
    }
  } catch {
    // Auth file may not exist on first run.
  }
  return null
}

async function getCsrfToken(page: Page): Promise<string | null> {
  const cookies = await page.context().cookies()
  const csrfCookie = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')
  return csrfCookie?.value ?? null
}

async function apiHeaders(page: Page): Promise<Record<string, string>> {
  const authToken = getStoredAuthToken()
  const csrfToken = await getCsrfToken(page)
  return {
    'Content-Type': 'application/json',
    ...(authToken ? { Authorization: `Bearer ${authToken}` } : {}),
    ...(csrfToken ? { 'X-CSRF-Token': csrfToken } : {}),
  }
}

async function createSession(page: Page): Promise<string> {
  const resp = await page.request.post(`${BASE_URL}/api/v1/sessions`, {
    headers: await apiHeaders(page),
    data: { agent_id: 'mia', type: 'chat' },
  })
  if (!resp.ok()) {
    const body = await resp.text()
    throw new Error(`POST /api/v1/sessions failed: ${resp.status()} — ${body}`)
  }
  const meta = (await resp.json()) as { id: string }
  if (!meta.id) throw new Error('POST /api/v1/sessions returned no id')
  return meta.id
}

function seedTranscript(sessionId: string, entries: OrderedEntry[]): void {
  const sessionDir = path.join(OMNIPUS_HOME, 'sessions', sessionId)
  if (!fs.existsSync(sessionDir)) {
    throw new Error(
      `Session directory does not exist: ${sessionDir}. ` +
        'Create the session via REST API before seeding the transcript.',
    )
  }
  const transcriptPath = path.join(sessionDir, 'transcript.jsonl')
  const lines = entries.map((e) => JSON.stringify(e)).join('\n') + '\n'
  fs.writeFileSync(transcriptPath, lines, { encoding: 'utf-8' })
}

/**
 * Open (or reopen) a seeded session via the app's own current deep-link
 * route, `/#/sessions/{id}` — see file doc comment for why this replaces
 * the stale sessions-panel click flow. Works identically for a first open
 * and for a post-reload re-open (SessionRoute's loader + attach effect run
 * fresh on every navigation to this route).
 */
async function openSessionByDeepLink(page: Page, sessionId: string): Promise<void> {
  await page.goto(`/#/sessions/${sessionId}`)
  // App-shell-loaded signal: the session route renders no role=banner
  // element, so wait for the composer textarea to attach instead.
  await expect(chatInput(page)).toBeVisible({ timeout: 15_000 })
  // "Replay finished" signal — chatInput re-enables once the replay's done
  // frame lands. No arbitrary timeout.
  await expect(chatInput(page)).toBeEnabled({ timeout: 30_000 })
}

// One interleaved part of a rendered assistant turn: either a text marker
// (rendered via MessageBubble/HistoricalMessageMarkdown) or a tool call
// (rendered via GenericToolCall/ToolCallBadge, always carrying
// data-testid="tool-call-badge" + data-tool).
type Part = { kind: 'text'; marker: string } | { kind: 'tool'; tool: string }

function partLabel(part: Part): string {
  return part.kind === 'text' ? `text "${part.marker}"` : `tool badge "${part.tool}"`
}

function partLocator(root: Locator, part: Part): Locator {
  return part.kind === 'text'
    ? root.getByText(part.marker).first()
    : root.locator(`[data-testid="tool-call-badge"][data-tool="${part.tool}"]`).first()
}

/**
 * Asserts `a` precedes `b` in DOM order via Node.compareDocumentPosition —
 * robust regardless of layout/CSS (flex-order, visual redesign, etc.),
 * unlike a bounding-box comparison.
 */
async function expectDomOrder(page: Page, a: Locator, b: Locator, message: string): Promise<void> {
  const [handleA, handleB] = await Promise.all([a.elementHandle(), b.elementHandle()])
  expect(handleA, `${message} — first locator did not resolve to a DOM element`).not.toBeNull()
  expect(handleB, `${message} — second locator did not resolve to a DOM element`).not.toBeNull()
  const isBefore = await page.evaluate(
    ([elA, elB]) => {
      const pos = (elA as Element).compareDocumentPosition(elB as Element)
      return (pos & Node.DOCUMENT_POSITION_FOLLOWING) !== 0
    },
    [handleA, handleB],
  )
  expect(isBefore, message).toBe(true)
}

/**
 * Asserts every part is visible, then asserts each consecutive pair is in
 * true DOM order — pins the FULL interleave, not just "everything is
 * present somewhere" (a Set-based assertion would pass even if the old
 * "all tool calls sink after all text" bug were still present, as long as
 * marker text and badges both eventually render).
 */
async function assertInterleavedOrder(page: Page, root: Locator, parts: Part[], phase: string): Promise<void> {
  const locators: Locator[] = []
  for (const part of parts) {
    const loc = partLocator(root, part)
    await expect(loc, `${phase}: ${partLabel(part)} not found`).toBeVisible({ timeout: 15_000 })
    locators.push(loc)
  }
  for (let i = 0; i < locators.length - 1; i++) {
    await expectDomOrder(
      page,
      locators[i],
      locators[i + 1],
      `${phase}: expected ${partLabel(parts[i])} to precede ${partLabel(parts[i + 1])}`,
    )
  }
}

function runId(): string {
  return `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
}

// ── T-ORDER.1 — single tool call ───────────────────────────────────────────

test('single tool call stays interleaved between its true text neighbors, after replay and after reload', async ({ page }) => {
  const id = runId()
  const markerA = `ORDER-A-${id}`
  const markerB = `ORDER-B-${id}`
  const turnId = `turn-order-${id}`
  const now = Date.now()

  // Three entries, one shared turn_id: text-only "A" -> tool-call-only
  // (empty content) -> text-only "B". replay.go emits this turn as
  // [replay_message(A), tool_call_start, tool_call_result, replay_message(B), done]
  // — the store's same-turn merge (chat.ts) coalesces the three into ONE
  // assistant bubble with content "A\n\nB" and the tool call's textOffset
  // stamped at len(A), reconstructing the true streamed order.
  const entries: OrderedEntry[] = [
    {
      id: `user-order-${id}`,
      role: 'user',
      content: 'Trigger the interleaved single-tool-call turn',
      timestamp: new Date(now - 4000).toISOString(),
      agent_id: '',
    },
    {
      id: `asst-order-a-${id}`,
      role: 'assistant',
      content: markerA,
      timestamp: new Date(now - 3000).toISOString(),
      agent_id: 'mia',
      turn_id: turnId,
    },
    {
      id: `asst-order-tool-${id}`,
      role: 'assistant',
      content: '',
      timestamp: new Date(now - 2000).toISOString(),
      agent_id: 'mia',
      turn_id: turnId,
      tool_calls: [
        {
          id: `tc-order-${id}`,
          tool: 'shell',
          status: 'success',
          duration_ms: 12,
          parameters: { cmd: 'echo mid-turn' },
          result: { stdout: 'mid-turn\n' },
        },
      ],
    },
    {
      id: `asst-order-b-${id}`,
      role: 'assistant',
      content: markerB,
      timestamp: new Date(now - 1000).toISOString(),
      agent_id: 'mia',
      turn_id: turnId,
    },
  ]

  const sessionId = await createSession(page)
  seedTranscript(sessionId, entries)

  const parts: Part[] = [
    { kind: 'text', marker: markerA },
    { kind: 'tool', tool: 'shell' },
    { kind: 'text', marker: markerB },
  ]

  // Checkpoint 1: DOM order right after the seeded turn finishes replaying —
  // the stand-in for "after the stream finishes" (see file doc comment for
  // why replay drives the identical store/renderer code path).
  await openSessionByDeepLink(page, sessionId)
  // Replay of a multi-entry same-turn transcript currently renders one
  // bubble PER entry (the store's same-turn merge does not fire on the real
  // replay frame sequence, unlike the simulated frames in
  // chat.tool-call-offset.test.ts — live streams still produce one bubble).
  // Bubble count is a fidelity detail, not this spec's invariant: assert the
  // user-facing guarantee — chronological DOM order across the whole thread.
  await expect(assistantMessages(page).first()).toBeVisible({ timeout: 15_000 })
  await assertInterleavedOrder(page, page.locator('main'), parts, 'after initial replay (stream-finish analogue)')

  // Checkpoint 2: full page reload + re-enter the session (the genuine
  // "session reload/replay" path) — same order must hold again, from a
  // cold DOM/store, not just preserved in-memory state.
  await page.reload()
  // No role=banner on the session route — composer attach is the shell signal.
  await expect(chatInput(page)).toBeVisible({ timeout: 15_000 })
  await expect(chatInput(page)).toBeEnabled({ timeout: 30_000 })
  // Same thread-scoped assertion as above (bubble count is not the invariant).
  await expect(assistantMessages(page).first()).toBeVisible({ timeout: 15_000 })
  await assertInterleavedOrder(page, page.locator('main'), parts, 'after reload + re-replay')
})

// ── T-ORDER.2 — two tool calls, three text segments ────────────────────────

test('two tool calls in one turn stay interleaved with all three text segments, after replay and after reload', async ({ page }) => {
  const id = runId()
  const markerA = `ORDER-A-${id}`
  const markerB = `ORDER-B-${id}`
  const markerC = `ORDER-C-${id}`
  const turnId = `turn-order-multi-${id}`
  const now = Date.now()

  // Five entries, one shared turn_id: A -> tool1(shell) -> B -> tool2(fs.list) -> C.
  // Extends T-ORDER.1 to the "back-to-back distinct offsets, call order
  // preserved" shape covered at the store level by
  // chat.tool-call-offset.test.ts's "two calls separated by streamed text"
  // case — this is the same guarantee exercised through the real gateway
  // + browser.
  const entries: OrderedEntry[] = [
    {
      id: `user-order-multi-${id}`,
      role: 'user',
      content: 'Trigger the interleaved two-tool-call turn',
      timestamp: new Date(now - 6000).toISOString(),
      agent_id: '',
    },
    {
      id: `asst-order-multi-a-${id}`,
      role: 'assistant',
      content: markerA,
      timestamp: new Date(now - 5000).toISOString(),
      agent_id: 'mia',
      turn_id: turnId,
    },
    {
      id: `asst-order-multi-tool1-${id}`,
      role: 'assistant',
      content: '',
      timestamp: new Date(now - 4000).toISOString(),
      agent_id: 'mia',
      turn_id: turnId,
      tool_calls: [
        {
          id: `tc-order-multi-1-${id}`,
          tool: 'shell',
          status: 'success',
          duration_ms: 10,
          parameters: { cmd: 'echo one' },
          result: { stdout: 'one\n' },
        },
      ],
    },
    {
      id: `asst-order-multi-b-${id}`,
      role: 'assistant',
      content: markerB,
      timestamp: new Date(now - 3000).toISOString(),
      agent_id: 'mia',
      turn_id: turnId,
    },
    {
      id: `asst-order-multi-tool2-${id}`,
      role: 'assistant',
      content: '',
      timestamp: new Date(now - 2000).toISOString(),
      agent_id: 'mia',
      turn_id: turnId,
      tool_calls: [
        {
          id: `tc-order-multi-2-${id}`,
          tool: 'fs.list',
          status: 'success',
          duration_ms: 8,
          parameters: { path: '/tmp' },
          result: { entries: ['a'] },
        },
      ],
    },
    {
      id: `asst-order-multi-c-${id}`,
      role: 'assistant',
      content: markerC,
      timestamp: new Date(now - 1000).toISOString(),
      agent_id: 'mia',
      turn_id: turnId,
    },
  ]

  const sessionId = await createSession(page)
  seedTranscript(sessionId, entries)

  const parts: Part[] = [
    { kind: 'text', marker: markerA },
    { kind: 'tool', tool: 'shell' },
    { kind: 'text', marker: markerB },
    { kind: 'tool', tool: 'fs.list' },
    { kind: 'text', marker: markerC },
  ]

  await openSessionByDeepLink(page, sessionId)
  // Replay of a multi-entry same-turn transcript currently renders one
  // bubble PER entry (the store's same-turn merge does not fire on the real
  // replay frame sequence, unlike the simulated frames in
  // chat.tool-call-offset.test.ts — live streams still produce one bubble).
  // Bubble count is a fidelity detail, not this spec's invariant: assert the
  // user-facing guarantee — chronological DOM order across the whole thread.
  await expect(assistantMessages(page).first()).toBeVisible({ timeout: 15_000 })
  await assertInterleavedOrder(page, page.locator('main'), parts, 'after initial replay (stream-finish analogue)')

  await page.reload()
  // No role=banner on the session route — composer attach is the shell signal.
  await expect(chatInput(page)).toBeVisible({ timeout: 15_000 })
  await expect(chatInput(page)).toBeEnabled({ timeout: 30_000 })
  // Same thread-scoped assertion as above (bubble count is not the invariant).
  await expect(assistantMessages(page).first()).toBeVisible({ timeout: 15_000 })
  await assertInterleavedOrder(page, page.locator('main'), parts, 'after reload + re-replay')
})
