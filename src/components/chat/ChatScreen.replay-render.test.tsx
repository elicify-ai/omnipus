import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { SubagentBlock } from './SubagentBlock'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import type { SubagentSpan, SubagentSpanTerminal } from '@/store/chat'

// W2-4: ChatScreen replay-render tests — TDD rows I-20 and I-21.
//
// Tests that the SubagentBlock component renders correctly for replay scenarios:
//   I-20: replay_message + tool_call_start + tool_call_result → tool-call-badge
//   I-21: subagent_start + nested tool_call_* + subagent_end → collapsible subagent block
//
// We test SubagentBlock directly (not full ChatScreen) because ChatScreen requires
// a TanStack Router context. SubagentBlock receives the SubagentSpan as a prop,
// which is exactly what the store produces after processing replay frames.
//
// Traces to: temporal-puzzling-melody.md W2-4
// Traces to: sprint-i-historical-replay-fidelity-spec.md TDD rows I-20, I-21

function resetStore() {
  act(() => {
    // Clear sessionsById so per-session buckets don't leak across tests, and
    // set activeSessionId to the same id the test frames carry. handleFrame
    // routes by frame.session_id, while foreground store selectors
    // (state.toolCalls, state.messages, ...) read from the bucket of the
    // active session — those must match for assertions to land on the same
    // bucket.
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      isReplaying: false,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      sessionTokens: 0,
      sessionCost: 0,
    })
    useSessionStore.setState({ activeSessionId: 'sess_test', activeAgentId: null })
    // SubagentBlock expansion state now lives in useUiStore (shared across the
    // live-render and historical-virtualized-render trees). Reset between tests
    // so default-collapsed assertions hold.
    useUiStore.setState({ expandedSpans: {} })
  })
}

beforeEach(resetStore)

// ─────────────────────────────────────────────────────────────────────────────
// TDD Row I-20: tool_call_start + tool_call_result render as tool-call-badge
// ─────────────────────────────────────────────────────────────────────────────

describe('ChatScreen replay-render — TDD row I-20 (tool-call-badge) (W2-4)', () => {
  it('a span with one step renders [data-testid="tool-call-badge"] with correct data-tool attribute', () => {
    // BDD: Given a SubagentSpan with one step (tool_call_start for "web_search")
    // BDD: When the SubagentBlock is mounted and expanded
    // BDD: Then [data-testid="tool-call-badge"] is visible with data-tool="web_search"
    // Traces to: temporal-puzzling-melody.md W2-4, sprint-i TDD I-20

    const span: SubagentSpan = {
      spanId: 'span_test_1',
      parentCallId: 'call_1',
      taskLabel: 'Search for data',
      status: 'success',
      durationMs: 1234,
      steps: [
        { kind: 'tool', tool: {
          id: 'tc_web_1',
          call_id: 'tc_web_1',
          tool: 'web_search',
          params: { query: 'test query' },
          result: { items: ['result1'] },
          status: 'success',
          duration_ms: 500,
        }},
      ],
      finalResult: 'Found 1 result',
    }

    render(<SubagentBlock span={span} />)

    // The collapsed header must be present first
    const collapsedHeader = screen.getByTestId('subagent-collapsed')
    expect(collapsedHeader).toBeInTheDocument()

    // Expand the block
    fireEvent.click(collapsedHeader)

    // The expanded region must appear
    const expandedRegion = screen.getByTestId('subagent-expanded')
    expect(expandedRegion).toBeInTheDocument()

    // The tool-call-badge must be present with the correct data-tool attribute
    const toolCallBadge = screen.getByTestId('tool-call-badge')
    expect(toolCallBadge).toBeInTheDocument()
    expect(toolCallBadge).toHaveAttribute('data-tool', 'web_search')
  })

  it('a span with tool "exec" renders badge with data-tool="exec"', () => {
    // Differentiation test: different tool name → different data-tool attribute.
    // Traces to: temporal-puzzling-melody.md W2-4

    const span: SubagentSpan = {
      spanId: 'span_test_2',
      parentCallId: 'call_2',
      taskLabel: 'Execute command',
      status: 'success',
      durationMs: 200,
      steps: [
        { kind: 'tool', tool: {
          id: 'tc_exec_1',
          call_id: 'tc_exec_1',
          tool: 'exec',
          params: { command: 'ls -la' },
          result: { stdout: 'file.txt' },
          status: 'success',
          duration_ms: 200,
        }},
      ],
    }

    render(<SubagentBlock span={span} />)
    fireEvent.click(screen.getByTestId('subagent-collapsed'))

    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toHaveAttribute('data-tool', 'exec')

    // Differentiation: "exec" != "web_search"
    expect(badge.getAttribute('data-tool')).not.toBe('web_search')
  })

  // Simulates the store processing replay_message → tool_call_start → tool_call_result frames
  it('store processes replay frames: tool_call_start + tool_call_result register the tool call', () => {
    // BDD: Given a replay sequence: replay_message → tool_call_start → tool_call_result
    // BDD: When the frames are processed by the store
    // BDD: Then toolCalls contains the registered call with status "success"
    // Traces to: temporal-puzzling-melody.md W2-4, sprint-i FR-I-010

    act(() => {
      // 1. replay_message creates an assistant message
      useChatStore.getState().handleFrame({
        type: 'replay_message',
        role: 'assistant',
        content: 'I will search for data',
        session_id: 'sess_test',
      })

      // 2. tool_call_start registers the tool call
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_replay_web_1',
        tool: 'web_search',
        params: { query: 'replay test' },
        session_id: 'sess_test',
      })

      // 3. tool_call_result resolves it
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_replay_web_1',
        tool: 'web_search',
        result: { results: ['found'] },
        status: 'success',
        duration_ms: 300,
        session_id: 'sess_test',
      })
    })

    const state = useChatStore.getState()

    // Tool call must be registered
    const tc = state.toolCalls['tc_replay_web_1']
    expect(tc).toBeDefined()
    expect(tc.tool).toBe('web_search')
    expect(tc.status).toBe('success')
    expect(tc.duration_ms).toBe(300)

    // Assistant message with replay content must exist
    const assistantMsg = state.messages.find((m) => m.role === 'assistant')
    expect(assistantMsg).toBeDefined()
    expect(assistantMsg?.content).toBe('I will search for data')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// TDD Row I-21: subagent_start + nested tool_call_* + subagent_end → collapsible
// ─────────────────────────────────────────────────────────────────────────────

describe('ChatScreen replay-render — TDD row I-21 (subagent-collapsed/expanded) (W2-4)', () => {
  it('SubagentBlock with two steps renders collapsed header and expands to show both badges', () => {
    // BDD: Given a SubagentSpan with 2 nested tool calls
    // BDD: When the SubagentBlock is mounted
    // BDD: Then [data-testid="subagent-collapsed"] is rendered
    // BDD: And clicking it reveals [data-testid="subagent-expanded"]
    // BDD: And the expanded region contains 2 [data-testid="tool-call-badge"] elements
    // Traces to: temporal-puzzling-melody.md W2-4, sprint-i TDD I-21

    const span: SubagentSpan = {
      spanId: 'span_nested_1',
      parentCallId: 'spawn_call_1',
      taskLabel: 'Analyse files',
      status: 'success',
      durationMs: 5000,
      steps: [
        { kind: 'tool', tool: {
          id: 'tc_nested_1',
          call_id: 'tc_nested_1',
          tool: 'fs.read',
          params: { path: '/etc/hosts' },
          result: { content: '127.0.0.1 localhost' },
          status: 'success',
          duration_ms: 100,
        }},
        { kind: 'tool', tool: {
          id: 'tc_nested_2',
          call_id: 'tc_nested_2',
          tool: 'web_search',
          params: { query: 'security check' },
          result: { items: [] },
          status: 'success',
          duration_ms: 200,
        }},
      ],
      finalResult: 'Analysis complete',
    }

    render(<SubagentBlock span={span} />)

    // Collapsed header must be visible initially
    const collapsed = screen.getByTestId('subagent-collapsed')
    expect(collapsed).toBeInTheDocument()
    expect(collapsed).toHaveAttribute('aria-expanded', 'false')

    // Expanded body must NOT be visible before click
    expect(screen.queryByTestId('subagent-expanded')).toBeNull()

    // Click to expand
    fireEvent.click(collapsed)

    // Expanded body must now be visible
    const expanded = screen.getByTestId('subagent-expanded')
    expect(expanded).toBeInTheDocument()

    // Must contain exactly 2 tool-call-badge elements (one per step)
    const badges = screen.getAllByTestId('tool-call-badge')
    expect(badges).toHaveLength(2)
    expect(badges[0]).toHaveAttribute('data-tool', 'fs.read')
    expect(badges[1]).toHaveAttribute('data-tool', 'web_search')

    // Header must now show aria-expanded=true
    expect(collapsed).toHaveAttribute('aria-expanded', 'true')
  })

  // Simulates the store processing full subagent replay frame sequence
  it('store processes subagent_start + tool_call_start + tool_call_result + subagent_end', () => {
    // BDD: Given a replay sequence: reply_message → subagent_start → tool calls → subagent_end
    // BDD: When the frames are processed
    // BDD: Then messages[0].spans[0] has 2 steps and status "success"
    // Traces to: temporal-puzzling-melody.md W2-4

    act(() => {
      // 1. Create an assistant message first (subagent_start attaches to the last assistant msg)
      useChatStore.getState().appendMessage({
        id: 'asst-replay-1',
        role: 'assistant',
        content: 'Delegating task...',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
      })

      // 2. subagent_start creates the span on the assistant message
      useChatStore.getState().handleFrame({
        type: 'subagent_start',
        span_id: 'span_replay_1',
        parent_call_id: 'spawn_call_replay_1',
        task_label: 'Replay sub-task',
        agent_id: 'agent-1',
        session_id: 'sess_test',
      })

      // 3. tool_call_start (nested — has parent_call_id)
      useChatStore.getState().handleFrame({
        type: 'tool_call_start',
        call_id: 'tc_sub_1',
        tool: 'fs.list',
        params: { path: '/tmp' },
        parent_call_id: 'spawn_call_replay_1',
        session_id: 'sess_test',
      })

      // 4. tool_call_result
      useChatStore.getState().handleFrame({
        type: 'tool_call_result',
        call_id: 'tc_sub_1',
        tool: 'fs.list',
        result: { entries: ['file.txt'] },
        status: 'success',
        duration_ms: 50,
        parent_call_id: 'spawn_call_replay_1',
        session_id: 'sess_test',
      })

      // 5. subagent_end closes the span
      useChatStore.getState().handleFrame({
        type: 'subagent_end',
        span_id: 'span_replay_1',
        status: 'success',
        duration_ms: 1000,
        final_result: 'Listed files',
        session_id: 'sess_test',
      })
    })

    const state = useChatStore.getState()
    const assistantMsg = state.messages.find((m) => m.id === 'asst-replay-1')
    expect(assistantMsg).toBeDefined()
    expect(assistantMsg?.spans).toHaveLength(1)

    const span = assistantMsg!.spans![0]
    expect(span.spanId).toBe('span_replay_1')
    expect(span.status).toBe('success')
    // Narrow to terminal to access durationMs and finalResult.
    const ts = span as SubagentSpanTerminal
    expect(ts.durationMs).toBe(1000)
    expect(ts.finalResult).toBe('Listed files')
    expect(span.steps).toHaveLength(1)
    const step0 = span.steps[0]
    expect(step0.kind === 'tool' ? step0.tool.tool : undefined).toBe('fs.list')
    expect(step0.kind === 'tool' ? step0.tool.status : undefined).toBe('success')
  })
})
