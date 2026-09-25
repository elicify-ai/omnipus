import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, dismissStaleDialogOverlay, selectAgent, waitForConnected } from './fixtures/selectors'

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
  // A pending approval modal left over from earlier state rehydrates on every
  // fresh page load while its ask pends (reconcileWithSessionState,
  // src/store/toolApproval.ts) and its Radix dialog-overlay intercepts pointer
  // events — so selectAgent's click never lands and the test burns its whole
  // budget in a retry loop. CI evidence: run 35997069836 saw 826 retries
  // against `<div data-testid="dialog-overlay"> intercepts pointer events`;
  // run 36123574726 attempt 3 shows a pending ask expiring at 11:27:30 with
  // NO deny ever recorded — no page ever dismissed it, because this spec's
  // previous ONE-SHOT guard raced the rehydrating session_state frame: the
  // frame can land after goto('/') returns, after the single count() check.
  // dismissStaleDialogOverlay is poll-shaped for exactly that reason, and
  // Escape maps to Deny while an approval is live, so the dismissal sticks
  // (the next snapshot cannot rehydrate a resolved approval).
  await dismissStaleDialogOverlay(page)
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

  // Two wrong oracles have stood here. Matching CHILD_ONLY_SENTINEL page-wide proved
  // only that the child RECEIVED the task: the parent interpolates the sentinel into
  // the task text, so it renders as the child's own INBOUND user message the moment
  // the view opens — it passed ~0.8s in, against a child that had done no work.
  //
  // Requiring the sentinel in an ASSISTANT row was worse, not better. It demands the
  // model emit an exact magic string, so the test fails whenever the child paraphrases
  // or reports a tool failure instead of completing the task — which is exactly the
  // model-compliance oracle the steer assertion below was repaired to remove. It duly
  // failed in CI with "element(s) not found" while the child was working correctly.
  //
  // What "reachable in its own live view" claims is that the CHILD's own transcript
  // renders here. Assert the system-decided facts: an assistant message exists, and it
  // is attributed to an agent other than the parent. `agent-label` renders that
  // message's own agentId (ChatScreen.tsx), so no model wording can satisfy or break it.
  await expect(
    page.locator('[data-message-role="assistant"]').first(),
    'the child session must render its own assistant output in its own live view',
  ).toBeVisible({ timeout: 240_000 })
  const firstChildLabel = (
    await page.getByTestId('agent-label').first().innerText({ timeout: 30_000 })
  ).trim()
  expect(
    firstChildLabel,
    'the child view must attribute its output to the worker, not to the parent agent',
  ).not.toMatch(/Jim/i)

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
  //
  // The badge's accessible name lives on the IconButton INSIDE the testid
  // wrapper (ConnectionStatus.tsx::StatusTooltip renders
  // `<IconButton aria-label={label}>`), not on the wrapper itself — the
  // wrapper holds only role="status" and the icon, so reading its text or
  // aria-label yields "" forever. CI run on 2aca3f4fe failed exactly that
  // way: 'Received string: ""'. Match the button by its accessible name so
  // the assertion reads the node that actually carries the state.
  const steerDelivery = page.getByTestId('user-message-delivery-status').last()
  await expect(steerDelivery).toBeVisible({ timeout: 30_000 })
  await expect(
    steerDelivery.getByRole('button', { name: /working on it/i }),
    'the steer never reached a live turn on the child session — the delivery badge never advanced to "working"',
  ).toBeVisible({ timeout: 120_000 })

  // ── Containment probe: WHO produced the message, not WHAT it says ─────────
  //
  // CI run 36096479273 / job 107950031485 (b19dd0c2c), attempt 1, proved the
  // old probe here —
  // `expect(parentView.getByText(CHILD_ONLY_SENTINEL)).toHaveCount(0)` —
  // unsound by construction, and the artifacts say so three separate ways:
  //
  //   1. THE PARENT ITSELF TYPES THE SENTINEL. It is interpolated verbatim
  //      into the `task:` line of the human's own prompt above (this file,
  //      the input.fill() block). test-failed-2.png for that attempt is the
  //      parentView, and the sentinel is plainly visible inside the user
  //      bubble carrying that prompt. So the parent chat legitimately
  //      contains it with containment working perfectly — "Received: 1" was
  //      the test reading its OWN input back.
  //   2. IT IS NOT IN A TOOL-CALL BADGE. ToolCallBadge.tsx renders the
  //      Parameters <pre> only under `{expanded && !isRunning && ...}`
  //      (ToolCallBadge.tsx:104 opens the badge, the detail block is gated
  //      below it) and badges start collapsed — so "exclude the delegate
  //      badge" would not have fixed anything; the match is the human's own
  //      message row.
  //   3. THE COUNT IS NOT STABLE, so "assert at most N" is not available
  //      either. Retry #2 of the same job got PAST this line with a count of
  //      ZERO and failed much later (its error-context.md shows the final
  //      ActivityBar assertion, line ~203). The message list is virtualised
  //      — useVirtualizer with `overscan: 5`, ChatScreen.tsx:1678-1686 — and
  //      re-pins to the BOTTOM on mount (ChatScreen.tsx:1610), so the human's
  //      prompt (the OLDEST row) is mounted or not depending purely on how
  //      long the parent's transcript grew. A count-based oracle here is a
  //      coin flip.
  //
  // And the sentinel is unusable even in principle: "only the child's final
  // answer, through the proper envelope" means an answer propagating C->B->A
  // ->parent may legitimately carry it. The same job's child-A snapshot
  // (error-context.md, attempt 1) shows child A's own assistant prose quoting
  // "...finish with exactly ADR091_CHILD_VIEW_ONLY" unprompted. Any
  // text-match on this string in the parent chat is therefore either a
  // model-compliance oracle or a false alarm. Do not reinstate one.
  //
  // Replaced with ATTRIBUTION, which the DOM does expose. Note what it is
  // NOT: rendered messages carry no producing-session attribute at all —
  // ChatScreen.tsx's four message rows (1006, 1068, 1075, 1264) and
  // MessageItem.tsx:184 stamp only data-message-role / data-message-id /
  // data-status, so "no message in the parent view belongs to the child
  // session" cannot be written directly. What IS stamped per message is the
  // agent that produced it: `agent-label` renders `agentDisplayName`, derived
  // from that MESSAGE's own `agentId` (ChatScreen.tsx:1162-1164 for the
  // virtualised row, 1808-1811 for the live one — the session's active agent
  // is only the fallback). The id->name mapping is a server-side roster fact
  // pinned in Go: pkg/coreagent/adr090_roster_test.go:23 maps agent id
  // "worker" (the `agent_id` this test's prompt delegates to) to the display
  // name "General Purpose", while the parent runs Jim.
  //
  // So: read the child's attribution off the child view at runtime rather
  // than hard-coding it (a hard-coded string that drifts would make this
  // assertion silently vacuous), prove it is NOT the parent's own agent — an
  // absence assertion is only worth writing if the thing could have been
  // present, and a worker-attributed row provably exists, in the child view,
  // right now — then require the parent thread to contain none of it.
  //
  // This is the ADR-091 D7 rule stated literally: "the parent's chat shows
  // nothing of the child ... a child's steps, narration and output never
  // render there" (ADR-091-steered-sessions-replace-subagents.md:201, :205).
  // The regression it is aimed at is a child turn's frame carrying the
  // PARENT's session id as its routing key — the one thing that would file
  // child output into the parent's own store bucket and render it
  // (src/store/chat/slices/frames.ts routes purely by frame.session_id; a
  // frame stamped with the CHILD's id lands in a separate bucket and is not
  // rendered in the parent view at all). `agent_id` rides those same frames,
  // so the leaked row arrives already labelled with the worker.
  //
  // KNOWN LIMIT, stated rather than papered over: a leaked frame carrying NO
  // agent_id would fall back to the parent's session agent and render as
  // "Jim", which this probe cannot see. The tool-call half of that hole is
  // covered by the delegate-chip count below — the child's OWN nested
  // delegate calls would push it past 1.
  const childAgentLabel = (
    await page.getByTestId('agent-label').first().innerText({ timeout: 30_000 })
  ).trim()
  expect(
    childAgentLabel,
    'the child view must attribute its assistant messages to some agent, or there is no attribution to look for in the parent',
  ).not.toBe('')
  expect(
    childAgentLabel,
    'the child must run a DIFFERENT agent from the parent (Jim), or "no child-attributed message in the parent" is vacuous',
  ).not.toMatch(/Jim/i)

  const parentView = await context.newPage()
  await parentView.goto(parentURL)
  await expect(parentView.locator('[data-testid="chat-input"]').first()).toBeVisible({ timeout: 15_000 })
  // The parent's transcript must actually be mounted before absence means
  // anything — otherwise every check below passes against an empty thread.
  await expect(parentView.locator('[data-message-role="assistant"]').first()).toBeVisible({ timeout: 30_000 })
  await expect(
    parentView.getByTestId('agent-label').filter({ hasText: childAgentLabel }),
    `the parent thread renders a message attributed to "${childAgentLabel}" — the delegated child's own output reached the human's chat instead of staying in its own session`,
  ).toHaveCount(0)
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
  // This previously asserted the bar mounts NOTHING here. That premise —
  // "idle and purely successful" — is not something this test can guarantee, and
  // it failed in CI for a legitimate reason: a retained "1 failed" chip made
  // hasFailedRecent true, so the gate mounted the bar correctly and the
  // assertion called it a regression. Since 37965946f the gate also mounts for
  // any open background command (founder decision: the pill is ActivityPanel's
  // only entry point), giving the old form a SECOND premise it cannot control.
  //
  // Assert the invariant that actually matters instead, and which no unrelated
  // background work can disturb: once the delegation has finished, nothing may
  // still be CLAIMED as running. A retained failure or a live shell job may
  // legitimately keep the pill up — a phantom "N running" may not.
  const parentBarLabel = parentView.locator('[data-testid="activity-bar-label"]')
  if ((await parentBarLabel.count()) > 0) {
    await expect(
      parentBarLabel,
      'after the delegation finished the pill must not still claim a running child',
    ).not.toHaveText(/\d+ running/)
  }
})
