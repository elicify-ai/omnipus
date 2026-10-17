import type { Locator } from '@playwright/test'

/**
 * ADR-082 S-09 compares the complete rendered answer, not its surrounding
 * attribution, Copy action or optional model footer. ChatScreen's live
 * AssistantMessage and VirtualAssistantMessageRow share this body container,
 * but only the live Markdown renderer has a .prose-sm wrapper inside it.
 * Preserve the whole body and normalize only whitespace, as S-09 did before.
 * Reject terminal failures across the WHOLE row first: ADR-087's interruption
 * and cutoff suffixes, and the error disclosure, can sit outside the body.
 */
export async function normalizedAssistantText(row: Locator): Promise<string> {
  const status = await row.getAttribute('data-status')
  if (status === 'error' || status === 'interrupted' || status === 'incomplete') {
    throw new Error(`Assistant row has failed or incomplete status: ${status}`)
  }
  const failureNotices = row
    .getByText(/^\((interrupted|cut off at the output limit)\)$/)
    .or(row.locator('[data-testid="error-detail-disclosure"], [role="alert"]'))
  if (await failureNotices.count()) {
    throw new Error('Assistant row contains an interruption, cutoff or error notice')
  }

  const bodies = await row
    .locator(':scope > div > div[class~="text-[length:var(--type-body-compact-size)]"]')
    .allInnerTexts()
  if (bodies.length > 1) throw new Error('Assistant row has multiple answer containers')
  const text = (bodies[0] ?? '').replace(/\s+/g, ' ').trim()
  if (!text) throw new Error('Assistant row has no rendered answer text')
  return text
}
