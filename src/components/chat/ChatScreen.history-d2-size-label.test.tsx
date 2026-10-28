// T66 / A2.4: every specialized tool falls back to the same readable preview
// with its true original size. Formatting wording/units are not product oracles.
import { afterEach, expect, it } from 'vitest'
import { within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { deferred } from '../../../tests/chat-history-paging/fixtures'
import { stopHistory } from '../../../tests/chat-history-paging/application'
import { hasTrueSizeLabel } from '../../../tests/chat-history-paging/size-label'
import { FULL_SOURCE, MARKER, fullPath, fullReads, json, openPreviewApp, previewToggle, toolSource, type PreviewApp } from '../../../tests/chat-history-paging/d2-preview-harness'

let app: PreviewApp | undefined
const releases: (() => void)[] = []
afterEach(async () => { releases.splice(0).forEach(fn => fn()); await stopHistory(app); app = undefined })

it.each([
  ['51,200 bytes', 51200, true], ['51.2 kB', 51200, true],
  ['50.0 KiB', 51200, true], ['64.0 KiB', 65537, true],
  ['2.0 MB', 2000000, true], ['1.9 MiB', 2000000, true],
  ['4 KiB', 51201, false], ['0 B', 51201, false],
  ['51.1 kB', 51200, false], ['no size', 51201, false],
  ['60000', 60000, false], ['3.0 MB', 2000000, false],
] as const)('size-label instrument: %s for %i bytes is %s', (label, bytes, accepted) => {
  // Decimal and binary units plus rounding error come from arithmetic, never
  // the product formatter. False controls detect a preview-size false green.
  expect(hasTrueSizeLabel(label, bytes)).toBe(accepted)
})

for (const tool of ['read_file', 'bash', 'web_search', 'd2_other_tool']) {
  it(`T66: ${tool} marker shows a visible true size collapsed and while full content loads`, async () => {
    const source = toolSource()
    source.tool_calls![0].tool = tool
    app = await openPreviewApp([source])
    const toggle = previewToggle()
    const row = toggle.closest<HTMLElement>('[data-history-tool-row]')
    expect(row, 'actual tool-owned row is reachable').not.toBeNull()
    const assertSize = () => {
      const labels = within(row!).queryAllByText(text => hasTrueSizeLabel(text, MARKER.original_size_bytes))
      expect(labels.length, 'visible label describes original encoded bytes, not the 4096-byte preview').toBeGreaterThan(0)
      for (const label of labels) expect(label).toBeVisible()
    }
    assertSize()
    expect(fullReads(app), 'collapsed output does not fetch full content').toHaveLength(0)
    const held = deferred<Response>(); releases.push(() => held.resolve(json(FULL_SOURCE)))
    app.network.respond('GET', fullPath(), () => held.promise)
    await userEvent.setup().click(toggle)
    await waitFor(() => expect(fullReads(app!)).toHaveLength(1))
    expect(row!.textContent, 'specialized renderer does not try parsing the marker as its old result shape').toContain(MARKER.preview)
    assertSize()
  })
}
