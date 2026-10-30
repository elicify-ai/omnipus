// #1452 gate SEC-3 follow-up: when a saved occurrence cannot be served in full,
// the A2.2 route answers 200 with the D4 per-item placeholder (identity and
// position kept, no saved body; pkg/gateway serveSessionHistoryEntry,
// c0f9893a5). The reader must see a known permanent state with the preview
// kept: not the placeholder text as if it were the content, and not a
// recoverable error whose Retry returns the same placeholder forever.
import { afterEach, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { stopHistory } from '../../../tests/chat-history-paging/application'
import { entry } from '../../../tests/chat-history-paging/fixtures'
import { MARKER, TOOL_ID, fullPath, fullReads, json, openPreviewApp, pos, previewToggle, type PreviewApp } from '../../../tests/chat-history-paging/d2-preview-harness'

// The server's D4 placeholder for the occurrence at pos(1), as
// pkg/gateway/history_page_preview.go::historyPlaceholderSource and
// history_page_prepare.go::historyWireMessage project it.
const SERVER_PLACEHOLDER = 'This saved message could not be displayed.'
const placeholder = (id: string) => ({ id, agent_id: 'mia', timestamp: '2026-10-21T10:00:00Z', role: 'system', content: SERVER_PLACEHOLDER, history_position: pos(1) })

let app: PreviewApp | undefined
afterEach(async () => {
  try { await stopHistory(app) } finally { app = undefined }
})

async function expectPermanent(container: HTMLElement) {
  await within(container).findByText(/cannot be displayed/i)
  expect(within(container).queryByRole('button', { name: /^Retry$/ }), 'no Retry for a state the same read cannot change').not.toBeInTheDocument()
}

it('a tool disclosure whose full read returns the D4 placeholder keeps the preview and shows a permanent state', async () => {
  app = await openPreviewApp()
  app.network.respond('GET', fullPath(), () => json(placeholder('d2-preview-owner')))
  const user = userEvent.setup()
  await user.click(previewToggle())
  await waitFor(() => expect(fullReads(app!)).toHaveLength(1))
  const row = document.querySelector<HTMLElement>(`[data-history-tool-row="${TOOL_ID}"]`)
  expect(row, 'the outcome belongs to the connected tool row').toBeInTheDocument()
  await expectPermanent(row!)
  expect(row!.textContent, 'the saved preview stays').toContain(MARKER.preview)
  expect(within(row!).queryByRole('region', { name: 'Tool output' }), 'the placeholder is not shown as the tool output').toBeNull()
  await user.click(previewToggle()); await user.click(previewToggle())
  await expectPermanent(document.querySelector<HTMLElement>(`[data-history-tool-row="${TOOL_ID}"]`)!)
  expect(fullReads(app).length, 'no request loop').toBeLessThanOrEqual(2)
})

it('Show the rest whose full read returns the D4 placeholder keeps the saved text and shows a permanent state', async () => {
  const preview = 'SAVED_PREVIEW_START ' + 'p'.repeat(200)
  app = await openPreviewApp([entry(1, { id: 'd2-text-owner', role: 'assistant', content: preview, content_original_size_bytes: 90000 })])
  app.network.respond('GET', fullPath(), () => json(placeholder('d2-text-owner')))
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: /Show the rest/ }))
  await waitFor(() => expect(fullReads(app!)).toHaveLength(1))
  const row = document.querySelector<HTMLElement>(`[data-history-position="${pos(1)}"]`)
  expect(row, 'the saved row is drawn').toBeInTheDocument()
  await expectPermanent(row!)
  expect(row!.textContent, 'the saved preview text stays').toContain('SAVED_PREVIEW_START')
  expect(row!.textContent, 'the placeholder never replaces the saved text').not.toContain(SERVER_PLACEHOLDER)
  expect(fullReads(app)).toHaveLength(1)
})
