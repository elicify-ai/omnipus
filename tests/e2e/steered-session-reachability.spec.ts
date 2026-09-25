import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, selectAgent, waitForConnected } from './fixtures/selectors'

const LABEL_A = 'ADR-091 child A'
const LABEL_B = 'ADR-091 child B'
const LABEL_C = 'ADR-091 child C'
const CHILD_ONLY_SENTINEL = 'ADR091_CHILD_VIEW_ONLY'

function requireApiKey(): void {
  if (!process.env.OPENROUTER_API_KEY_CI) {
    throw new Error('BLOCKED: OPENROUTER_API_KEY_CI is required for steered-session reachability')
  }
}

test('steered session is reachable in its own live view without leaking child output into the parent chat', async ({ page, context }) => {
  requireApiKey()
  test.setTimeout(420_000)

  await page.goto('/')
  await selectAgent(page, /Jim/i)
  const input = chatInput(page)
  await expect(input).toBeEnabled({ timeout: 15_000 })
  await waitForConnected(page, { timeout: 15_000 })

  // Prompt shape fixed 2026-09-24 (lane sq-gwfix), root-caused against real
  // CI evidence, not a guess: gateway.log from GitHub run 35997069836
  // (gateway-logs-llm-agents/omnipus.log, both retries of this exact spec —
  // response_preview quotes this prompt's own text verbatim) shows Jim
  // replying in PROSE that narrates/echoes the instructions — one retry
  // even wrote the literal string `delegate(agent_id="worker", label=...)`
  // as text — instead of ever issuing a real `delegate` tool call. Neither
  // attempt logs a subagent_start; the session simply goes idle. The old
  // prompt never told the model to stop narrating and act, unlike every
  // OTHER live-delegation spec in this shard (subagent.spec.ts,
  // handoff.spec.ts, delegation-hidden.spec.ts), which all share one
  // working idiom: "Call the `delegate` tool exactly once, right now, with
  // these arguments: ... Do not reply in prose. Do not call any other
  // tool. Call delegate now." Re-pointed at that same idiom for the top-
  // level call to Jim (the level the log evidence actually covers) and
  // reinforced with an explicit "do not narrate it" clause at each nested
  // level too — the A->B and B->C delegate calls were never reached by
  // either failed attempt, so that reinforcement is inferred from the same
  // failure class, not independently confirmed by a log line.
  await input.fill([
    'Call the `delegate` tool exactly once, right now, with these arguments:',
    '  agent_id: "worker"',
    `  label: "${LABEL_A}"`,
    `  task: "Call the \`delegate\` tool exactly once with agent_id=\\"worker\\" and label=\\"${LABEL_B}\\" -- call the tool directly, do not describe it in prose. Give it this task verbatim: Call the \`delegate\` tool exactly once with agent_id=\\"worker\\" and label=\\"${LABEL_C}\\" -- call the tool directly, do not describe it in prose. Give it this task verbatim: Work for at least two tool steps, report progress, then finish with exactly ${CHILD_ONLY_SENTINEL}."`,
    'Do not reply in prose. Do not call any other tool. Call delegate now.',
    'Do not repeat or paraphrase any child output in this parent chat.',
  ].join('\n'))
  await input.press('Enter')

  const activityBar = page.locator('[data-testid="activity-bar"]')
  await expect(activityBar).toBeVisible({ timeout: 180_000 })
  const parentSurface = page.locator('[data-active-session-id]').first()
  await expect(parentSurface).toHaveAttribute('data-active-session-id', /.+/)
  const parentSessionID = await parentSurface.getAttribute('data-active-session-id')
  if (!parentSessionID) {
    throw new Error('root chat did not expose its active session id')
  }
  const parentURL = new URL(`/#/sessions/${encodeURIComponent(parentSessionID)}`, page.url()).toString()
  await activityBar.click()

  const childRow = page.locator('[data-testid="activity-row"]').filter({ hasText: LABEL_A })
  await expect(childRow).toBeVisible({ timeout: 60_000 })
  await expect(childRow).toContainText(/queued|running|working/i)
  // data-testid="activity-row-open" — the exact hook ActivityPanel.test.tsx
  // ("ActivityPanel — open control (ADR-091 FR-E-004)") already asserts for
  // this control: rendered only when the row's AgentActivityItem carries a
  // childSessionId, and its click handler calls useNavigate() with
  // `{ to: '/sessions/$sessionId', params: { sessionId: childSessionId } }`
  // — i.e. through the SAME /sessions/{id} deep-link route this file's
  // parentURL uses below, not a bespoke path. Using the real testid here
  // rather than a role/name guess (ActivityPanel.tsx is mid-rewrite and the
  // visible label text is not yet settled).
  const openControl = childRow.getByTestId('activity-row-open')
  await expect(openControl).toBeVisible()
  await openControl.click()

  // NOT a `toHaveURL(/sessions\//)` + URL-regex extraction here (that was
  // this spec's original approach and it is wrong against the current
  // router): SessionRoute (src/routes/_app/sessions.$sessionId.tsx) treats
  // `/#/sessions/{id}` as a deep-link ENTRY point only — the instant it
  // resolves the session's workspace_id it replaces the URL with
  // `/#/workspaces/{workspaceId}/chat` via a client-side
  // `navigate({ replace: true })`, and the workspace route never carries a
  // session id in its path at all. Every worker session has a workspace_id
  // here (ADR-091 D1/AC-1: a steered session's record carries the
  // *creator's* workspace_id; AGENTS.md: "sub-agent sessions belong to the
  // parent's workspace") — the parent (Jim) itself only exists inside a
  // workspace to begin with (`/` redirects into the default workspace's
  // Chat tab, src/routes/_app/index.tsx), so this redirect fires for real
  // here, unlike open-in-chat.spec.ts's workspace-LESS session, which is
  // the one case that legitimately keeps a `sessions/{id}` URL. Parent and
  // child likely share the SAME workspace, so the post-Open URL can equal
  // the pre-Open URL too — the URL is not a usable "did we navigate"
  // signal at all here. The one reliable, navigation-target-agnostic
  // signal for "which session is this chat surface bound to now" is
  // ChatScreen's own data-active-session-id attribute, stamped identically
  // whether the surface is mounted via the /sessions/{id} route or the
  // /workspaces/{id}/chat route (both render the same <ChatScreen/> —
  // WorkspaceChatTab.tsx). This is the same seam
  // fixtures/session-setup.ts's openSessionByDeepLink relies on, and for
  // the same documented reason (ChatScreen.tsx's own data-active-session-id
  // comment: it can transiently hold the PREVIOUS session's id, or the
  // '__pending' optimistic-send sentinel, during a route swap).
  const boundSurface = page.locator('[data-active-session-id]').first()
  await expect
    .poll(
      async () => {
        const id = await boundSurface.getAttribute('data-active-session-id')
        return id && id !== parentSessionID && id !== '__pending' ? id : null
      },
      { timeout: 15_000 },
    )
    .not.toBeNull()
  const childSessionID = await boundSurface.getAttribute('data-active-session-id')
  expect(childSessionID, 'the open control must bind the chat surface to the child session').toBeTruthy()

  await expect(page.getByText(CHILD_ONLY_SENTINEL, { exact: false })).toBeVisible({ timeout: 240_000 })

  const childInput = chatInput(page)
  await expect(childInput).toBeEnabled({ timeout: 30_000 })
  await waitForConnected(page, { timeout: 15_000 })
  await childInput.fill('Steering update: acknowledge with "steer received" in this child session only.')
  await childInput.press('Enter')
  // CI runs 36026415761 and 36066315713 (all 4 attempts each): asking the
  // model to echo a phrase is not an oracle. The original bare
  // page.getByText('steer received') matched TWO elements the instant the
  // message was sent (the user's own echo, plus the composer's value), an
  // instant strict-mode violation. Scoping it to the message list and
  // filtering out the echo fixed the strict-mode fault but left a worse
  // one: it then matched ZERO elements, because the child received the
  // steer, kept working, and simply never repeated the phrase. Verified
  // from that job's ARIA snapshot — the child's transcript continues past
  // the steering bubble with two further Delegate chips, and the only
  // occurrence of "steer received" on the whole page is the user's echo.
  // The feature is NOT broken: steering delivery into a delegated child's
  // next round is pinned in Go by
  // pkg/agent/steer_delegated_injection_test.go (mutation-verified), so the
  // defect was this spec's oracle, not the product.
  //
  // Assert the SERVER's own acknowledgement instead. The delivery badge on
  // the sent message reaches 'working' only via
  // pkg/gateway/websocket_chat.go::markWorkingIfTurnAlreadyActive, which
  // returns early unless liveStreamers[sessionID] != nil — so the badge
  // proves BOTH that the gateway accepted this steer AND that a live turn
  // existed on THIS session (the child's) to accept it. That is exactly
  // what this test means by "reachable", it is decided by the server rather
  // than by a language model, and it cannot pass while the steer went to
  // the wrong session or to no live turn at all.
  const steerDelivery = page.getByTestId('user-message-delivery-status').last()
  await expect(steerDelivery).toBeVisible({ timeout: 30_000 })
  await expect
    .poll(async () => (await steerDelivery.getAttribute('aria-label')) ?? (await steerDelivery.innerText()), {
      timeout: 120_000,
      message:
        'the steer never reached a live turn on the child session — the delivery badge never advanced to "working"',
    })
    .toMatch(/working on it/i)

  const parentView = await context.newPage()
  await parentView.goto(parentURL)
  await expect(parentView.locator('[data-testid="chat-input"]').first()).toBeVisible({ timeout: 15_000 })
  await expect(parentView.getByText(CHILD_ONLY_SENTINEL, { exact: false })).toHaveCount(0)
  // Containment, restated so it cannot pass vacuously: the STEER ITSELF was
  // addressed to the child's session, so the parent's chat must not render
  // it. 'Steering update' is text we sent, so it provably exists somewhere
  // in the run — a zero-count here is a real absence, not the absence of a
  // phrase no one ever produced (which is what asserting on the model's
  // unproduced ack would have been).
  await expect(parentView.getByText('Steering update', { exact: false })).toHaveCount(0)
  await expect(parentView.locator('[data-testid="tool-call-badge"][data-tool="delegate"]')).toHaveCount(1)

  await parentView.reload()
  // CI run 36026415761: this segment used to wait 30s for the Activity bar to
  // reappear after a RELOAD of the completed parent session. It cannot: a
  // fresh mount of ActivityBar (src/components/chat/ActivityBar.tsx) renders
  // NOTHING unless hasOpenAgentChildren || panelOpen || hasFailedRecent (its
  // shouldMount gate, ActivityBar.tsx:94) — a completed, purely-successful
  // delegation satisfies none of the three, and the visual-qa decision that
  // produced that gate is regression-protected by ActivityBar.test.tsx
  // ("renders nothing when there is no running activity"). The delegation
  // stays visible through the surface ADR-091 D7/AC-7 designates for it at
  // idle: the delegate tool-call chip, which shouldRenderToolCall renders
  // unconditionally for a run action (toolVisibility.ts `delegate` case) —
  // proven to survive replay by the identical pre-reload assertion above.
  await expect(
    parentView.locator('[data-testid="tool-call-badge"][data-tool="delegate"]'),
    'the parent\'s delegate chip is the delegation surface that persists across reload at idle',
  ).toHaveCount(1)
  // And the inverse of the old assertion is itself part of the design
  // contract: an idle, purely-successful parent deliberately mounts no bar
  // (same shouldMount gate; pins the /visual-qa decision so an
  // always-visible idle bar cannot quietly come back).
  await expect(
    parentView.locator('[data-testid="activity-bar"]'),
    'ActivityBar mounts nothing when idle after a purely-successful delegation (shouldMount: no open child, no panel, no failure)',
  ).toHaveCount(0)
})
