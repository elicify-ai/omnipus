// Replayed/baked tool-chip humanization regression (UAT, 2026-06-10).
//
// Symptom: after reloading a Mia→Jim handover session, the baked/historical
// tool-call chips rendered RAW tool ids ("handoff", "remember", "recall_memory",
// "browser.navigate", "browser.screenshot") instead of humanized labels.
//
// The baked/replay render path is ChatScreen's VirtualAssistantMessageRow, which
// renders each stored tool call as:
//
//     <GenericToolCall toolName={tc.tool} args={tc.params} result={tc.result}
//                      status={{ type: 'complete' }} ... />
//
// where `tc.tool` is the SPA tool-call object's name field, mapped from the wire
// `tool` field by rawToToolCall()/the store reducer. This test reproduces that
// exact call shape for every tool in the repro session and locks:
//   (a) the COLLAPSED chip shows the humanized label, and
//   (b) the EXPANDED chip still shows the RAW id (power-user parity with the
//       live path).

import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { GenericToolCall } from './GenericToolCall'
import type { MessagePartStatus } from '@assistant-ui/react'

const COMPLETE: MessagePartStatus = { type: 'complete' }

// The repro session's tool set: { wire `tool` value → expected humanized label }.
const REPRO_TOOLS: ReadonlyArray<readonly [rawTool: string, humanLabel: string]> = [
  ['handoff', 'Hand off'],
  ['remember', 'Remember'],
  ['recall_memory', 'Recall memory'],
  ['browser.navigate', 'Navigate browser'],
  ['browser.screenshot', 'Take screenshot'],
]

describe('baked/replay tool chips — humanized label, raw id preserved', () => {
  it.each(REPRO_TOOLS)(
    'tool "%s" renders collapsed label "%s" (not the raw id)',
    (rawTool, humanLabel) => {
      // Mirror VirtualAssistantMessageRow's call: toolName comes from tc.tool,
      // and a baked tool call always has args so the chip is expandable.
      render(
        <GenericToolCall
          toolName={rawTool}
          args={{ probe: 1 }}
          status={COMPLETE}
        />,
      )

      // (a) Collapsed chip shows the humanized label.
      expect(screen.getByText(humanLabel)).toBeInTheDocument()
      // The raw id must NOT appear as the collapsed chip label (unless the raw
      // id happens to equal the humanized form — none of the repro tools do).
      const badge = screen.getByTestId('tool-call-badge')
      const collapsedLabel = badge.querySelector('span.font-medium')
      expect(collapsedLabel?.textContent?.trim()).toBe(humanLabel)
      expect(collapsedLabel?.textContent?.trim()).not.toBe(rawTool)
    },
  )

  it.each(REPRO_TOOLS)(
    'tool "%s" still exposes the raw id in the expanded detail',
    (rawTool, humanLabel) => {
      render(
        <GenericToolCall
          toolName={rawTool}
          args={{ probe: 1 }}
          status={COMPLETE}
        />,
      )

      fireEvent.click(screen.getByRole('button'))

      // (b) Expanded "Tool" section shows the raw id verbatim.
      const badge = screen.getByTestId('tool-call-badge')
      expect(within(badge).getByText(rawTool)).toBeInTheDocument()
      // Humanized label still present (collapsed header stays rendered).
      expect(within(badge).getByText(humanLabel)).toBeInTheDocument()
    },
  )
})
