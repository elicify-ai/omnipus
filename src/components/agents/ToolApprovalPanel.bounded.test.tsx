// Train 3 gate r1, code-reviewer F05 (Important): a long inline approval card
// must keep its decision controls reachable. The card is height-bounded and
// its details (helper row, previews, segments, countdown, scope choice) scroll
// inside it, while the header and the Approve/Deny row stay outside that
// scroll region. jsdom has no layout, so this pins the structure that makes it
// true in a browser: a bounded, column-laid card; a scrollable details region
// holding the long content; the decision buttons NOT inside that region.
import { it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, act, within } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import type { ComponentType } from 'react'
import { queryClient } from '@/lib/queryClient'
import { useToolApprovalStore } from '@/store/toolApproval'
import type { ToolApprovalRequiredFrame } from '@/lib/api/generated/asyncapi-types'
import { cleanupApprovalState, installApprovalNetwork, loadApprovalPanel, resetSharedState } from '../../../tests/l1-ui-red-u23/support'

const longScript = Array.from({ length: 80 }, (_, i) => `echo "step ${i + 1}"`).join(' && ')
const segments = Array.from({ length: 40 }, (_, i) => ({
  segment_index: i, command_text: `echo "step ${i + 1}"`, resolved_binary: 'echo',
  classification: 'none' as const, suggested_prefix: 'echo', prefix_available: true, network_required: false,
}))

const longRequest: ToolApprovalRequiredFrame = {
  type: 'tool_approval_required', approval_id: 'appr-long', tool_call_id: 'call-long',
  tool_name: 'bash', args: { command: longScript, cwd: 'work' },
  agent_id: 'mia', session_id: 'omnipus-main', workspace_id: 'ws-sales',
  turn_id: 'turn-long', expires_in_ms: 600_000, segments,
}

let Panel: ComponentType
beforeEach(async () => {
  resetSharedState('omnipus-main')
  installApprovalNetwork()
  Panel = await loadApprovalPanel()
})
afterEach(async () => { await cleanupApprovalState() })

it('F05: a long approval scrolls its details inside a height-bounded card; Approve and Deny stay outside the scroll region', () => {
  render(<QueryClientProvider client={queryClient}><Panel /></QueryClientProvider>)
  act(() => useToolApprovalStore.getState().enqueue(longRequest))

  const panel = screen.getByTestId('tool-approval-panel')
  // Bounded, column-laid card: its height is capped relative to the viewport.
  expect(panel.className).toMatch(/(^|\s)flex-col(\s|$)/)
  expect(panel.className).toMatch(/(^|\s)max-h-\[[^\]]*vh\](\s|$)/)

  // The long content lives in ONE scrollable region inside the card.
  const details = within(panel).getByTestId('tool-approval-details')
  expect(details.className).toMatch(/(^|\s)overflow-y-auto(\s|$)/)
  expect(details.className).toMatch(/(^|\s)min-h-0(\s|$)/)
  expect(details).toContainElement(screen.getByTestId('command-segment-39'))

  // The decision controls are reachable without scrolling that region.
  const approve = within(panel).getByRole('button', { name: 'Approve Once' })
  const deny = within(panel).getByRole('button', { name: 'Deny' })
  expect(details).not.toContainElement(approve)
  expect(details).not.toContainElement(deny)
})
