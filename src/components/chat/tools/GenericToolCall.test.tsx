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

// ── BLOCKER 2: delegation-denied sentinel rendering ──────────────────────────

describe('GenericToolCall — delegation-denied result sentinel', () => {
  it('renders a distinct delegation-failure block (reason + policy), NOT raw JSON', () => {
    const delegationDenied = {
      error: 'delegation_denied' as const,
      reason: 'Scout is not in your trust set for delegation.',
      policy: 'trust_set' as const,
      tool: 'delegate',
      target_agent_id: 'scout-01',
    }

    render(
      <GenericToolCall
        toolName="delegate"
        result={delegationDenied}
        status={{ type: 'incomplete', reason: 'error' } as MessagePartStatus}
      />
    )

    fireEvent.click(screen.getByRole('button'))

    const block = screen.getByTestId('result-delegation-denied')
    expect(block).toBeInTheDocument()
    // Human reason surfaced verbatim.
    expect(block).toHaveTextContent('Scout is not in your trust set for delegation.')
    // Policy axis rendered with its human label.
    expect(block).toHaveTextContent('Trust set')
    // Target agent shown when present.
    expect(block).toHaveTextContent('scout-01')
    expect(block).toHaveTextContent('Delegation denied')

    // The raw JSON sentinel keys must NOT leak into a code/pre blob.
    expect(screen.queryByText(/"delegation_denied"/)).toBeNull()
    expect(screen.queryByText(/"error":/)).toBeNull()
  })

  it('renders the mode policy axis and omits target agent when absent', () => {
    const delegationDenied = {
      error: 'delegation_denied' as const,
      reason: 'Delegation is disabled in solo mode.',
      policy: 'mode' as const,
      tool: 'delegate',
    }

    render(
      <GenericToolCall
        toolName="delegate"
        result={delegationDenied}
        status={{ type: 'incomplete', reason: 'error' } as MessagePartStatus}
      />
    )

    fireEvent.click(screen.getByRole('button'))

    const block = screen.getByTestId('result-delegation-denied')
    expect(block).toHaveTextContent('Delegation is disabled in solo mode.')
    expect(block).toHaveTextContent('Delegation mode')
    expect(block).not.toHaveTextContent('Target agent')
  })

  it('(G17) collapsed header shows "Delegation denied · <axis>", not generic "Failed"', () => {
    const delegationDenied = {
      error: 'delegation_denied' as const,
      reason: 'Scout is not in your trust set for delegation.',
      policy: 'trust_set' as const,
      tool: 'delegate',
      target_agent_id: 'scout-01',
    }

    render(
      <GenericToolCall
        toolName="delegate"
        result={delegationDenied}
        status={{ type: 'incomplete', reason: 'error' } as MessagePartStatus}
      />
    )

    // WITHOUT expanding, the collapsed header names the denial + the policy axis.
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toHaveTextContent('Delegation denied · Trust set')
    // The generic "Failed" label must not be used for a delegation denial.
    expect(badge).not.toHaveTextContent(/\bFailed\b/)
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
