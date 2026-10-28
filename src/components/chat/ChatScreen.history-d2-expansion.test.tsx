// T66/B72: real application, store, Generic/tool-specific renderers and HTTP
// adapter; only network delivery is controlled. Tool disclosure triggers read
// (team-lead clarification2026-10-28); message Show-the-rest stays explicit.
import { afterEach, expect, it } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useChatStore } from '@/store/chat'
import { deferred } from '../../../tests/chat-history-paging/fixtures'
import { stopHistory } from '../../../tests/chat-history-paging/application'
import { FULL_SOURCE, FULL_TEXT, MAIN_ID, MARKER, TOOL_ID, fullPath, fullReads, json, openPreviewApp, previewToggle, toolSource, type PreviewApp } from '../../../tests/chat-history-paging/d2-preview-harness'

let app: PreviewApp | undefined
const releases: (() => void)[] = []
afterEach(async () => { releases.splice(0).forEach(release => release()); await stopHistory(app); app = undefined })

it('T66: opening two tool disclosures for one occurrence coalesces one full read and never stores full content', async () => {
  const two = toolSource()
  two.tool_calls!.push({ ...two.tool_calls![0], id: TOOL_ID + '-two' })
  app = await openPreviewApp([two])
  const held = deferred<Response>(); releases.push(() => held.resolve(json(FULL_SOURCE)))
  app.network.respond('GET', fullPath(), () => held.promise)
  expect(fullReads(app), 'collapsed previews do not eagerly fetch bodies').toHaveLength(0)
  const user = userEvent.setup()
  await user.click(previewToggle(0)); await user.click(previewToggle(1))
  await waitFor(() => expect(fullReads(app!)).toHaveLength(1))
  expect(document.body.textContent, 'preview remains readable while full content is pending').toContain(MARKER.preview)
  await act(async () => held.resolve(json({ ...FULL_SOURCE, tool_calls: [FULL_SOURCE.tool_calls![0], { ...FULL_SOURCE.tool_calls![0], id: TOOL_ID + '-two' }] })))
  await waitFor(() => expect(document.body.textContent).toContain('-FULL_END'))
  expect(fullReads(app)).toHaveLength(1)
  const stored = JSON.stringify(useChatStore.getState().sessionsById[MAIN_ID]?.messagesById)
  expect(stored, 'A2.4 full bodies belong only to query cache/render overlay').not.toContain(FULL_TEXT)
  expect(stored).toContain('_history_preview')
})

for (const failure of ['network', 'timeout', '500', 'malformed JSON', 'schema invalid'] as const) {
  it(`T66: ${failure} keeps preview and Retry repeats the exact occurrence read`, async () => {
    app = await openPreviewApp()
    app.network.respond('GET', fullPath(), () => {
      if (failure === 'network') throw new TypeError('test transport offline')
      if (failure === 'timeout') throw new DOMException('test transport deadline', 'TimeoutError')
      if (failure === '500') return json({ error: 'Temporary session storage failure' }, 500)
      if (failure === 'malformed JSON') return new Response('{broken', { status: 200, headers: { 'Content-Type': 'application/json' } })
      return json({ id: 'not-a-public-Message' })
    })
    const user = userEvent.setup()
    await user.click(previewToggle())
    await waitFor(() => expect(fullReads(app!)).toHaveLength(1))
    const alert = await screen.findByRole('alert')
    expect(document.body.textContent).toContain(MARKER.preview)
    const retry = within(alert).getByRole('button', { name: /^Retry$/ })
    retry.focus()
    app.network.respond('GET', fullPath(), () => json(FULL_SOURCE))
    await user.keyboard('{Enter}')
    await waitFor(() => expect(document.body.textContent).toContain('-FULL_END'))
    expect(fullReads(app).map(read => read.path)).toEqual([fullPath(), fullPath()])
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(JSON.stringify(useChatStore.getState().sessionsById[MAIN_ID]?.messagesById)).not.toContain(FULL_TEXT)
  })
}

it.each([404, 410] as const)('T66: permanent HTTP%i keeps preview without Retry or a request loop', async status => {
  app = await openPreviewApp()
  app.network.respond('GET', fullPath(), () => json({ error: status === 410 ? 'history entry expired' : 'history position not found' }, status))
  const user = userEvent.setup()
  await user.click(previewToggle())
  await waitFor(() => expect(fullReads(app!)).toHaveLength(1))
  await screen.findByText(status === 410 ? /expired/i : /no longer available/i)
  expect(document.body.textContent).toContain(MARKER.preview)
  expect(screen.queryByRole('button', { name: /^Retry$/ })).not.toBeInTheDocument()
  await user.click(previewToggle()); await user.click(previewToggle())
  await screen.findByText(status === 410 ? /expired/i : /no longer available/i)
  expect(screen.queryByRole('button', { name: /^Retry$/ })).not.toBeInTheDocument()
  // Two explicit openings permit at most two user-initiated reads. The spec
  // does not require caching a permanent error; it forbids autonomous loops.
  expect(fullReads(app).length).toBeLessThanOrEqual(2)
})

it('T66: HTTP401 invokes existing sign-in handling without silent retries', async () => {
  app = await openPreviewApp()
  app.network.respond('GET', fullPath(), () => json({ error: 'unauthorized' }, 401))
  app.network.respond('GET', '/api/v1/auth/validate', () => json({ error: 'unauthorized' }, 401))
  await userEvent.setup().click(previewToggle())
  await waitFor(() => expect(fullReads(app!)).toHaveLength(1))
  await screen.findByRole('heading', { name: /sign in/i })
  expect(fullReads(app)).toHaveLength(1)
})

it('T66: ordinary small output is rendered unchanged without a full-entry read', async () => {
  // Existing read_file renderer's ordinary contract is the {text} envelope.
  app = await openPreviewApp([toolSource({ text: 'small exact tool output' })])
  await userEvent.setup().click(previewToggle())
  await screen.findByText('small exact tool output', { exact: false })
  expect(fullReads(app)).toHaveLength(0)
})
