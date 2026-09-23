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

  await input.fill([
    'Call delegate exactly once with agent_id="worker" and label="ADR-091 child A".',
    'Give it this task verbatim:',
    `Call delegate exactly once with agent_id="worker" and label="${LABEL_B}".`,
    'The B task must call delegate exactly once with agent_id="worker" and',
    `label="${LABEL_C}". C must work for at least two tool steps, report progress,`,
    `and finish with exactly ${CHILD_ONLY_SENTINEL}.`,
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
  await expect(page.getByText('steer received', { exact: false })).toBeVisible({ timeout: 120_000 })

  const parentView = await context.newPage()
  await parentView.goto(parentURL)
  await expect(parentView.locator('[data-testid="chat-input"]').first()).toBeVisible({ timeout: 15_000 })
  await expect(parentView.getByText(CHILD_ONLY_SENTINEL, { exact: false })).toHaveCount(0)
  await expect(parentView.getByText('steer received', { exact: false })).toHaveCount(0)
  await expect(parentView.locator('[data-testid="tool-call-badge"][data-tool="delegate"]')).toHaveCount(1)

  await parentView.reload()
  const replayedActivityBar = parentView.locator('[data-testid="activity-bar"]')
  await expect(replayedActivityBar).toBeVisible({ timeout: 30_000 })
  await replayedActivityBar.click()
  await expect(parentView.locator('[data-testid="activity-row"]').filter({ hasText: LABEL_A })).toBeVisible()
})
