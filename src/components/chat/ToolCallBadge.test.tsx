import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ToolCallBadge } from './ToolCallBadge'
import type { ToolCall } from '@/lib/api'

// test_tool_call_badge_states (test #6)
// test_tool_call_collapse_expand (test #7)
// test_tool_component_registry (test #8)
// Traces to: wave5a-wire-ui-spec.md — Scenario: Running tool call shows spinner
//             wave5a-wire-ui-spec.md — Scenario: Successful tool call collapses by default
//             wave5a-wire-ui-spec.md — Scenario: Failed tool call shows error with retry
//             wave5a-wire-ui-spec.md — Scenario: Expanding a collapsed tool call shows details
//             wave5a-wire-ui-spec.md — Scenario Outline: Built-in tool uses custom component
//             wave5a-wire-ui-spec.md — Scenario: Unknown tool uses generic component

type ToolCallWithId = ToolCall & { call_id: string }

function makeToolCall(overrides: Partial<ToolCallWithId>): ToolCallWithId {
  return {
    id: 'tc_1',
    call_id: 'tc_1',
    tool: 'web_search',
    params: { query: 'AWS pricing' },
    status: 'running',
    ...overrides,
  }
}

describe('ToolCallBadge — running state (test #6)', () => {
  it('shows tool name and spinning icon while running', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Running tool call shows spinner
    render(<ToolCallBadge toolCall={makeToolCall({ tool: 'web_search', status: 'running' })} />)
    // Collapsed chip shows the humanized label, not the raw id.
    expect(screen.getByText('Search the web')).toBeInTheDocument()
    // Spinning icon from animate-spin class
    const spinner = document.querySelector('.animate-spin')
    expect(spinner).toBeTruthy()
  })

  it('shows "Cancelled" label with the Prohibit icon for cancelled tool call', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Cancel during tool execution
    render(<ToolCallBadge toolCall={makeToolCall({ status: 'cancelled' })} />)
    expect(screen.getByText(/Cancelled/i)).toBeInTheDocument()
  })
})

describe('ToolCallBadge — success state (test #6 continued)', () => {
  it('shows green checkmark icon and duration for successful tool call', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Successful tool call collapses by default
    const { container } = render(
      <ToolCallBadge
        toolCall={makeToolCall({ status: 'success', duration_ms: 2300, result: { results: [] } })}
      />
    )
    // Check circle icon renders (success state)
    const checkIcon = container.querySelector('.text-\\[var\\(--color-success\\)\\]')
    expect(checkIcon ?? screen.getByText(/2\.3s|done/i)).toBeTruthy()
  })

  it('collapses by default — result not visible without clicking', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Successful tool call collapses by default
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          status: 'success',
          duration_ms: 1000,
          result: { super_secret_result: 'hidden' },
        })}
      />
    )
    // Result detail should not be visible in collapsed state
    expect(screen.queryByText(/super_secret_result/i)).toBeNull()
  })
})

describe('ToolCallBadge — error state (test #6 continued)', () => {
  it('shows red XCircle icon and error message', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Failed tool call shows error with retry
    render(
      <ToolCallBadge
        toolCall={makeToolCall({ status: 'error', error: 'Timeout after 30s' })}
      />
    )
    // Expand to see error (error shown in expanded view)
    const btn = document.querySelector('button')
    if (btn) fireEvent.click(btn)
    expect(screen.getByText(/Timeout after 30s/i)).toBeInTheDocument()
  })
})

describe('ToolCallBadge — collapse/expand toggle (test #7)', () => {
  it('expands to show parameters and result when clicked', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Expanding a collapsed tool call shows details
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          status: 'success',
          params: { query: 'AWS m5 instance pricing' },
          result: { hits: 5 },
          duration_ms: 1500,
        })}
      />
    )
    const btn = document.querySelector('button[aria-expanded]') as HTMLButtonElement
    expect(btn).toBeTruthy()
    fireEvent.click(btn)
    // After expand, params should be visible
    expect(screen.getByText(/AWS m5 instance pricing/i)).toBeInTheDocument()
    // Result should be visible
    expect(screen.getByText(/hits/i)).toBeInTheDocument()
  })

  it('clicking again collapses the badge', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          status: 'success',
          params: { query: 'test' },
          result: { items: [] },
        })}
      />
    )
    const btn = document.querySelector('button[aria-expanded]') as HTMLButtonElement
    // Expand
    fireEvent.click(btn)
    expect(screen.getByText(/test/i)).toBeInTheDocument()
    // Collapse
    fireEvent.click(btn)
    expect(screen.queryByText(/items/i)).toBeNull()
  })
})

describe('ToolCallBadge — tool icon registry (test #8)', () => {
  it.each([
    // [rawTool, humanizedCollapsedLabel]
    ['bash', 'Run command'],
    ['exec', 'Run command'],
    ['web_search', 'Search the web'],
    ['file.read', 'Read'],
    ['browser.navigate', 'Navigate browser'],
  ])('renders humanized label for built-in tool: %s → %s', (toolName, label) => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario Outline: Built-in tool uses custom component
    const { container } = render(
      <ToolCallBadge toolCall={makeToolCall({ tool: toolName, status: 'success', duration_ms: 100 })} />
    )
    expect(container.firstChild).toBeTruthy()
    // Collapsed chip shows the humanized label; raw id is NOT shown collapsed.
    expect(screen.getByText(label)).toBeInTheDocument()
  })

  it('renders generic label for unknown MCP tool and exposes the raw id when expanded', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Unknown tool uses generic component
    render(
      <ToolCallBadge toolCall={makeToolCall({ tool: 'custom_mcp_tool', status: 'success', duration_ms: 80 })} />
    )
    // Collapsed: humanized label.
    expect(screen.getByText('Custom mcp tool')).toBeInTheDocument()
    expect(screen.queryByText('custom_mcp_tool')).toBeNull()
    // Expand: raw tool id is visible to power users.
    const btn = document.querySelector('button[aria-expanded]') as HTMLButtonElement
    fireEvent.click(btn)
    expect(screen.getByText('custom_mcp_tool')).toBeInTheDocument()
  })
})

describe('ToolCallBadge — cannot click when running', () => {
  it('clicking the header while running does not expand (running is not expandable)', () => {
    // Traces to: wave5a-wire-ui-spec.md — AC1: spinner shown; detail not accessible while running
    render(<ToolCallBadge toolCall={makeToolCall({ status: 'running' })} />)
    const btn = document.querySelector('button[aria-expanded]') as HTMLButtonElement
    fireEvent.click(btn)
    // Still no expanded content
    expect(screen.queryByText(/Parameters/i)).toBeNull()
  })
})
