import type { Locator } from '@playwright/test'

/**
 * ADR-082 S-09 compares the complete rendered answer, not its surrounding
 * attribution, Copy action or optional model footer. ChatScreen's live
 * AssistantMessage and VirtualAssistantMessageRow share this body container,
 * but only the live Markdown renderer has a .prose-sm wrapper inside it.
 * Preserve the whole body and normalize only whitespace, as S-09 did before.
 */
export async function normalizedAssistantText(row: Locator): Promise<string> {
  const bodies = await row
    .locator(':scope > div > div[class~="text-[length:var(--type-body-compact-size)]"]')
    .allInnerTexts()
  if (bodies.length > 1) throw new Error('Assistant row has multiple answer containers')
  const text = (bodies[0] ?? '').replace(/\s+/g, ' ').trim()
  if (!text) throw new Error('Assistant row has no rendered answer text')
  return text
}
