import { expect, type Page } from '@playwright/test'

type ApprovalDecision = 'allow_once' | 'deny'
type DecisionObserver = (decision: ApprovalDecision, command: string | null) => void

/**
 * t0's operator approves only its predetermined, side-effect-free check.
 * Everything else is denied; no standing grant or policy setting is changed.
 * WU-8/#1263 moved this decision from a modal to the inline approval card.
 */
export async function resolveGoalCheckApproval(
  page: Page,
  onDecision?: DecisionObserver,
): Promise<ApprovalDecision | null> {
  const candidate = page.getByTestId('tool-approval-panel')
  if (!(await candidate.isVisible())) return null

  const titleId = await candidate.getAttribute('aria-labelledby')
  if (!titleId) throw new Error('goal walk: inline approval has no approval identity')
  // Every read AND the click belong to the inspected request. If it resolves
  // while we act, never transfer its decision to the next queued card.
  const panel = page.locator(
    `[data-testid="tool-approval-panel"][aria-labelledby=${JSON.stringify(titleId)}]`,
  )

  let command: string | null = null
  if (await panel.getByText('Run command', { exact: true }).count() === 1) {
    // The final pre is the card's displayed Arguments JSON, after the command
    // preview/segments. Parse it rather than approving a matching prose fragment.
    const args: unknown = JSON.parse(await panel.locator('pre').last().innerText())
    if (args && typeof args === 'object' && 'command' in args && typeof args.command === 'string') {
      command = args.command
    }
  }
  const isCheck = command !== null && /^true(?:;\s*echo\s+(?:\$\?|"exit=\$\?"))?$/.test(command)
  const decision = isCheck ? 'allow_once' : 'deny'
  const label = isCheck ? 'Approve Once' : 'Deny'
  await panel.getByRole('button', { name: label, exact: true }).click({ timeout: 5_000 })
  // A queued approval may replace this one immediately; wait for THIS id to
  // leave, not for the entire panel to disappear. A failed POST stays visible
  // and fails here rather than being treated as a successful decision.
  await expect(panel).toHaveCount(0, { timeout: 5_000 })
  onDecision?.(decision, command)
  return decision
}

/** Service approvals throughout an existing wait, without extending its budget. */
export async function waitWithGoalApprovals<T>(
  page: Page,
  completion: Promise<T>,
  onDecision?: DecisionObserver,
): Promise<T> {
  let finished = false
  // Keep rejection handled while a click is in flight, then propagate the
  // original error below. Resolving an approval never manufactures completion.
  const result = completion.then(
    value => ({ value }),
    error => ({ error: error as unknown }),
  ).finally(() => { finished = true })
  while (!finished) {
    await resolveGoalCheckApproval(page, onDecision)
    if (!finished) await Promise.race([result, page.waitForTimeout(250)])
  }
  const outcome = await result
  if ('error' in outcome) throw outcome.error
  return outcome.value
}
