/**
 * ADR-059 W5 / A4-3 — a failed file operation must say why.
 *
 * Before this, a refused or failed write rendered as
 * `write_file · a.svg · 1.2 KB · Failed` and nothing else. The row is
 * identical whether the file already existed (nothing is wrong, a sibling got
 * there first), the disk filled up, or the path was denied — so the one person
 * who could act on the difference could not see it. The backend half of W5
 * gives the calling agent a machine-readable discriminator; this is the human
 * half.
 *
 * FileOpBlock is not exported, so the render functions are captured through a
 * makeAssistantToolUI mock, keyed by tool name — the pattern
 * BashOutput.edge.test.tsx established.
 */

import { describe, it, expect, vi } from 'vitest'
import { render } from '@testing-library/react'

type RenderFn = (props: {
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

const REFUSAL = {
  error: 'file_exists',
  path: '/w/a.svg',
  reason: 'file: /w/a.svg already exists. Set overwrite=true to replace.',
  tool: 'write_file',
}

describe('FileWriteConfirm failure reasons', () => {
  it('shows the reason from a structured refusal instead of a bare Failed', () => {
    const { container } = render(
      <>
        {captured.write!({
          args: { path: '/w/a.svg', content: 'x' },
          result: REFUSAL,
          status: { type: 'incomplete' },
        })}
      </>
    )
    expect(container.textContent).toContain('already exists')
    expect(container.textContent).toContain('overwrite=true')
  })

  it('shows the reason for an ordinary failure, where the result is plain text', () => {
    const { container } = render(
      <>
        {captured.write!({
          args: { path: '/w/a.svg', content: 'x' },
          result: 'disk full while writing output',
          status: { type: 'incomplete' },
        })}
      </>
    )
    expect(container.textContent).toContain('disk full while writing output')
  })

  it('never renders the raw JSON payload', () => {
    const { container } = render(
      <>
        {captured.write!({
          args: { path: '/w/a.svg', content: 'x' },
          result: REFUSAL,
          status: { type: 'incomplete' },
        })}
      </>
    )
    // The discriminator is for machines. A user seeing `file_exists` or a
    // brace means the object leaked into the view instead of being read.
    expect(container.textContent).not.toContain('file_exists')
    expect(container.textContent).not.toContain('{')
  })

  it('stays silent on success — a completed write has nothing to explain', () => {
    const { container } = render(
      <>
        {captured.write!({
          args: { path: '/w/a.svg', content: 'x' },
          result: 'File written: /w/a.svg',
          status: { type: 'complete' },
        })}
      </>
    )
    expect(container.textContent).not.toContain('File written')
  })

  it('stays silent while the write is still running', () => {
    const { container } = render(
      <>
        {captured.write!({
          args: { path: '/w/a.svg', content: 'x' },
          result: undefined,
          status: { type: 'running' },
        })}
      </>
    )
    expect(container.textContent).toContain('write_file')
  })

  it('applies to edit_file and append_file too — the same silent row', () => {
    for (const renderFn of [captured.edit!, captured.append!]) {
      const { container } = render(
        <>
          {renderFn({
            args: { path: '/w/a.svg', content: 'x' },
            result: 'permission denied',
            status: { type: 'incomplete' },
          })}
        </>
      )
      expect(container.textContent).toContain('permission denied')
    }
  })

  it('tolerates a result with no usable reason without rendering an empty line', () => {
    for (const result of [undefined, null, '', '   ', {}, { reason: '' }, 42]) {
      const { container } = render(
        <>
          {captured.write!({
            args: { path: '/w/a.svg', content: 'x' },
            result,
            status: { type: 'incomplete' },
          })}
        </>
      )
      expect(container.textContent).toContain('write_file')
    }
  })
})
