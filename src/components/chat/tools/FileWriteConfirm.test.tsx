/**
 * ADR-059 W5 / A4-3 — a failed file operation must say why.
 *
 * The first version of this file was a green test for a state production
 * cannot reach, which is the exact failure mode the review wave was told to
 * hunt. Every case hand-fed `status: { type: 'incomplete' }` alongside a
 * truthy `result` — and `toMessagePartStatus` in @assistant-ui/core cannot
 * emit that pair:
 *
 *     if (part.type === "tool-call") if (!part.result) return message.status;
 *     else return COMPLETE_STATUS;
 *
 * A tool-call part with a result is ALWAYS `complete`. So the real failure was
 * worse than the one A4-3 set out to fix: a refused write rendered with the
 * success dot and a "Done" label, and the reason never mounted.
 *
 * These cases therefore pair a truthy result with `complete`, exactly as the
 * runtime does, and carry the outcome in the chat store — which is where it
 * actually lives (ToolCall.status / ToolCall.error, set from the
 * tool_call_result frame).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from '@testing-library/react'
import { useChatStore } from '@/store/chat'
import type { ToolCall } from '@/lib/api'

type RenderFn = (props: {
  toolCallId: string
  args: unknown
  result: unknown
  status: { type: string; reason?: string }
}) => React.ReactNode

const captured = vi.hoisted(() => ({
  write: null as RenderFn | null,
  edit: null as RenderFn | null,
  append: null as RenderFn | null,
}))

vi.mock('@assistant-ui/react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@assistant-ui/react')>()
  return {
    ...original,
    makeAssistantToolUI: (config: Record<string, unknown>) => {
      if (config.toolName === 'write_file') captured.write = config.render as RenderFn
      if (config.toolName === 'edit_file') captured.edit = config.render as RenderFn
      if (config.toolName === 'append_file') captured.append = config.render as RenderFn
      return config
    },
  }
})

import './FileWriteConfirm'

const CALL_ID = 'call-1'

const REFUSAL = {
  error: 'file_exists',
  path: '/w/a.svg',
  reason: 'file: /w/a.svg already exists. Set overwrite=true to replace.',
  tool: 'write_file',
}

/** seedCall puts a tool call in the store the way the WS frame handler does. */
function seedCall(partial: Partial<ToolCall>) {
  useChatStore.setState({
    toolCalls: {
      [CALL_ID]: {
        id: CALL_ID,
        call_id: CALL_ID,
        tool: 'write_file',
        params: { path: '/w/a.svg' },
        status: 'success',
        ...partial,
      } as ToolCall & { call_id: string },
    },
  })
}

/** renderRow drives a render fn with a runtime-PRODUCIBLE prop combination. */
function renderRow(fn: RenderFn, result: unknown, statusType = 'complete') {
  return render(
    <>{fn({ toolCallId: CALL_ID, args: { path: '/w/a.svg', content: 'x' }, result, status: { type: statusType } })}</>
  )
}

beforeEach(() => {
  useChatStore.setState({ toolCalls: {} })
})

describe('FileWriteConfirm failure reasons', () => {
  it('shows the reason for a structured refusal, with the part status the runtime really emits', () => {
    seedCall({ status: 'error', result: REFUSAL, error: REFUSAL.reason })
    const { container } = renderRow(captured.write!, REFUSAL)
    expect(container.textContent).toContain('already exists')
    expect(container.textContent).toContain('overwrite=true')
  })

  it('labels a refused write Failed, not Done', () => {
    seedCall({ status: 'error', result: REFUSAL, error: REFUSAL.reason })
    const { container } = renderRow(captured.write!, REFUSAL)
    expect(container.textContent).toContain('Failed')
    expect(container.textContent).not.toContain('Done')
  })

  it('shows the reason for an ordinary failure, where the result is plain text', () => {
    seedCall({ status: 'error', result: 'disk full while writing output', error: 'disk full while writing output' })
    const { container } = renderRow(captured.write!, 'disk full while writing output')
    expect(container.textContent).toContain('disk full while writing output')
    expect(container.textContent).toContain('Failed')
  })

  it('never renders the raw JSON payload', () => {
    seedCall({ status: 'error', result: REFUSAL, error: REFUSAL.reason })
    const { container } = renderRow(captured.write!, REFUSAL)
    // The discriminator is for machines. A user seeing `file_exists` or a
    // brace means the object leaked into the view instead of being read.
    expect(container.textContent).not.toContain('file_exists')
    expect(container.textContent).not.toContain('{')
  })

  it('stays silent on success — a completed write has nothing to explain', () => {
    seedCall({ status: 'success', result: 'File written: /w/a.svg' })
    const { container } = renderRow(captured.write!, 'File written: /w/a.svg')
    expect(container.textContent).not.toContain('File written')
    expect(container.textContent).toContain('Done')
  })

  it('stays silent while the write is still running', () => {
    seedCall({ status: 'running' })
    const { container } = renderRow(captured.write!, undefined, 'running')
    expect(container.textContent).toContain('write_file')
    expect(container.textContent).toContain('Running')
  })

  it('shows the reason for a cancelled op, under the cancelled label', () => {
    seedCall({ status: 'cancelled', result: 'cancelled by user', error: 'cancelled by user' })
    const { container } = renderRow(captured.write!, 'cancelled by user')
    expect(container.textContent).toContain('cancelled by user')
    expect(container.textContent).not.toContain('Failed')
  })

  it('applies to edit_file and append_file too — the same silent row', () => {
    for (const renderFn of [captured.edit!, captured.append!]) {
      seedCall({ status: 'error', result: 'permission denied', error: 'permission denied' })
      const { container } = renderRow(renderFn, 'permission denied')
      expect(container.textContent).toContain('permission denied')
    }
  })

  it('falls back to the part result when the store has no record of the call', () => {
    // Replay and any post-eviction render reach the row with an empty store.
    useChatStore.setState({ toolCalls: {} })
    const { container } = render(
      <>
        {captured.write!({
          toolCallId: 'not-in-store',
          args: { path: '/w/a.svg', content: 'x' },
          result: REFUSAL,
          status: { type: 'incomplete' },
        })}
      </>
    )
    expect(container.textContent).toContain('already exists')
  })

  it('tolerates a result with no usable reason without rendering an empty line', () => {
    for (const result of [undefined, null, '', '   ', {}, { reason: '' }, 42]) {
      seedCall({ status: 'error', result })
      const { container } = renderRow(captured.write!, result)
      expect(container.textContent).toContain('write_file')
      expect(container.textContent).toContain('Failed')
    }
  })
})
