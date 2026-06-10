// GenericToolCall component tests — W1-4 truncation/marshal-error sentinel rendering
// Traces to: temporal-puzzling-melody.md §Wave 1 W1-4 (TS half)

import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { GenericToolCall } from './GenericToolCall'
import type { MessagePartStatus } from '@assistant-ui/react'

const COMPLETE_STATUS: MessagePartStatus = { type: 'complete' }
const RUNNING_STATUS: MessagePartStatus = { type: 'running' }

// ── W1-4: truncated sentinel rendering ───────────────────────────────────────

describe('GenericToolCall — truncated result sentinel', () => {
  it('renders truncation banner with human-readable size when _truncated is true', () => {
    const truncatedResult = {
      _truncated: true as const,
      original_size_bytes: 2 * 1024 * 1024, // 2 MiB
      preview: 'first 10 KiB of content...',
    }

    render(
      <GenericToolCall
        toolName="fs.read"
        result={truncatedResult}
        status={COMPLETE_STATUS}
      />
    )

    // Expand to see the result pane
    fireEvent.click(screen.getByRole('button'))

    // Banner must be visible
    const banner = screen.getByTestId('result-truncated-banner')
    expect(banner).toBeInTheDocument()
    expect(banner).toHaveTextContent('Truncated')
    expect(banner).toHaveTextContent('10 KiB')
    expect(banner).toHaveTextContent('2.0 MiB')

    // Preview text renders below the banner
    expect(screen.getByText('first 10 KiB of content...')).toBeInTheDocument()
  })

  it('renders truncation banner for 512 KiB original size', () => {
    const truncatedResult = {
      _truncated: true as const,
      original_size_bytes: 512 * 1024, // 512 KiB
      preview: 'preview text',
    }

    render(
      <GenericToolCall
        toolName="fs.read"
        result={truncatedResult}
        status={COMPLETE_STATUS}
      />
    )

    fireEvent.click(screen.getByRole('button'))

    const banner = screen.getByTestId('result-truncated-banner')
    expect(banner).toHaveTextContent('Truncated')
    // 512 KiB renders as KiB
    expect(banner).toHaveTextContent('512.0 KiB')
  })

  it('does NOT render truncation banner for normal results', () => {
    render(
      <GenericToolCall
        toolName="exec"
        result={{ exit_code: 0, stdout: 'ok' }}
        status={COMPLETE_STATUS}
      />
    )

    fireEvent.click(screen.getByRole('button'))

    expect(screen.queryByTestId('result-truncated-banner')).toBeNull()
    // Normal JSON result renders
    expect(screen.getByText(/"exit_code"/)).toBeInTheDocument()
  })
})

// ── W1-4: marshal-error sentinel rendering ───────────────────────────────────

describe('GenericToolCall — marshal-error result sentinel', () => {
  it('renders red error banner with the marshal error message', () => {
    const marshalErrorResult = {
      _marshal_error: 'json: unsupported type: chan int',
    }

    render(
      <GenericToolCall
        toolName="exec"
        result={marshalErrorResult}
        status={COMPLETE_STATUS}
      />
    )

    fireEvent.click(screen.getByRole('button'))

    const banner = screen.getByTestId('result-marshal-error')
    expect(banner).toBeInTheDocument()
    expect(banner).toHaveTextContent('json: unsupported type: chan int')
    expect(banner).toHaveTextContent('serialization failed')
  })

  it('does NOT render marshal-error banner for normal results', () => {
    render(
      <GenericToolCall
        toolName="exec"
        result="normal string result"
        status={COMPLETE_STATUS}
      />
    )

    fireEvent.click(screen.getByRole('button'))

    expect(screen.queryByTestId('result-marshal-error')).toBeNull()
  })
})

// ── Baseline: non-sentinel result still renders normally ────────────────────

describe('GenericToolCall — baseline rendering', () => {
  it('renders header with humanized tool name', () => {
    render(
      <GenericToolCall
        toolName="web_search"
        status={RUNNING_STATUS}
      />
    )
    // Collapsed chip shows the humanized label, not the raw id.
    expect(screen.getByText('Search the web')).toBeInTheDocument()
  })

  it('expanded pane shows plain result when result is a plain object', () => {
    render(
      <GenericToolCall
        toolName="exec"
        result={{ stdout: 'hello\n', exit_code: 0 }}
        status={COMPLETE_STATUS}
      />
    )

    fireEvent.click(screen.getByRole('button'))

    // Neither banner appears
    expect(screen.queryByTestId('result-truncated-banner')).toBeNull()
    expect(screen.queryByTestId('result-marshal-error')).toBeNull()
    // JSON content renders
    expect(screen.getByText(/"stdout"/)).toBeInTheDocument()
  })
})
