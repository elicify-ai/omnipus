// Oracle: founder's latest #1406 rulings, 2026-10-21. Real indicator,
// generated argument frames, reducer, icon and production query client.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import type { ComponentProps } from 'react'
import { AgentColor } from '@/lib/api/generated/schemas'
import type { ToolCallStartFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { __resetFinishedTurnIdsForTests } from '@/store/chat/frames'
import { AgentStatusIndicator } from './AgentStatusIndicator'

vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchSessionMessages: vi.fn().mockResolvedValue([]),
}))

const SID = 'status-active-form'
let sequence = 0

// not-wire-format: local fixture props, never sent across the gateway.
type FixtureProps = {
  phase?: ComponentProps<typeof AgentStatusIndicator>['phase']
  label?: string | null
  identity?: boolean
}

function Fixture({ phase = 'thinking', label = null, identity = true }: FixtureProps) {
  return (
    <QueryClientProvider client={queryClient}>
      <AgentStatusIndicator phase={phase} label={label} figure={identity ? 'Omnipus' : undefined} role={identity ? 'general' : undefined} color={AgentColor.options[9]} name="Mia" />
    </QueryClientProvider>
  )
}

function accept(todos: ToolCallStartFrame['params']['todos']) {
  const callId = `active-form-call-${++sequence}`
  act(() => {
    useChatStore.getState().handleFrame({ type: 'tool_call_start', session_id: SID, call_id: callId, tool: 'set_todos', params: { outcome: 'Prepare the report', todos } })
    useChatStore.getState().handleFrame({ type: 'tool_call_result', session_id: SID, call_id: callId, tool: 'set_todos', status: 'success', result: 'Checklist saved' })
  })
  expect(useChatStore.getState().sessionsById[SID].toolCalls[callId].status).toBe('success')
  expect(useChatStore.getState().sessionsById[SID].toolCalls[callId].params.todos).toEqual(todos)
  return callId
}

function fallback() {
  const element = screen.getByTestId('reply-status-fallback')
  expect(element.textContent).toBe('Processing your request…')
  expect(element).toHaveAttribute('aria-hidden', 'true')
  expect(within(screen.getByTestId('reply-status-indicator')).getByText('Thinking')).toHaveClass('sr-only')
  expect(screen.queryByTestId('reply-current-todo')).not.toBeInTheDocument()
  return element
}

beforeEach(() => {
  sequence = 0
  __resetFinishedTurnIdsForTests()
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: SID, activeAgentId: 'mia' }, true)
  useConnectionStore.setState({ ...useConnectionStore.getInitialState(), isConnected: true }, true)
  act(() => useChatStore.getState().handleFrame({ type: 'token', session_id: SID, message_id: 'status-answer', turn_id: 'status-turn', content: 'Preparing the report' }))
})

afterEach(() => {
  cleanup()
  queryClient.clear()
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
})

describe('active-form-only status copy', () => {
  it('shows exactly the trimmed active form rather than the task text or an added prefix', () => {
    render(<Fixture />)
    const todos = [{ text: 'Write the Q4 summary', status: 'in_progress', active_form: ' \n Drafting the Q4 summary\t ' }]
    accept(todos)
    const item = screen.getByTestId('reply-current-todo')
    expect(item.textContent).toBe('Drafting the Q4 summary')
    expect(item).toHaveAttribute('aria-hidden', 'true')
    expect(item.querySelector('[title]')).toHaveAttribute('title', 'Drafting the Q4 summary')
    expect(within(screen.getByTestId('reply-status-indicator')).getByText('Working on a checklist item')).toHaveClass('sr-only')
    expect(item.textContent).not.toMatch(/Working on\s*:/)
  })

  it('selects the first in-progress item, not a later item with a usable form', () => {
    vi.spyOn(Math, 'random').mockReturnValue(0.2)
    render(<Fixture />)
    accept([{ text: 'First item', status: 'in_progress' }, { text: 'Later item', status: 'in_progress', active_form: 'Not the first item' }])
    fallback()
  })

  it('keeps the generated 500-code-point form bound after trimming raw arguments', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    render(<Fixture />)
    const activeForm = '𠮷'.repeat(500)
    accept([{ text: 'Prepare the report', status: 'in_progress', active_form: ` \t${activeForm}\n ` }])
    expect(screen.getByTestId('reply-current-todo').textContent).toBe(activeForm)
    expect(warn).not.toHaveBeenCalled()
  })

  it('falls back and diagnoses a form beyond the generated code-point bound', () => {
    vi.spyOn(Math, 'random').mockReturnValue(0.2)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    render(<Fixture />)
    const callId = accept([{ text: 'Prepare the report', status: 'in_progress', active_form: '𠮷'.repeat(501) }])
    fallback()
    expect(warn).toHaveBeenCalledExactlyOnceWith('chat.current_todo_validation_failed', {
      sessionId: SID, callId, issues: [{ code: 'too_big', path: '0.active_form' }],
    })
  })

  it('uses the fixed fallback for an in-progress item without the optional form', () => {
    vi.spyOn(Math, 'random').mockReturnValue(0.2)
    render(<Fixture />)
    accept([{ text: 'Write the Q4 summary', status: 'in_progress' }])
    fallback()
  })

  it.each(['', ' \n\t '])('treats blank optional form %j as absent without a validation warning', (activeForm) => {
    vi.spyOn(Math, 'random').mockReturnValue(0.2)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    render(<Fixture />)
    accept([{ text: 'Write the summary', status: 'in_progress', active_form: activeForm }])
    fallback()
    expect(warn).not.toHaveBeenCalled()
  })

  it.each([null, false, 42, [], {}].map((value) => [value]))('ignores invalid form %j, keeps the fallback, and logs once per accepted call', (activeForm) => {
    vi.spyOn(Math, 'random').mockReturnValue(0.2)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const view = render(<Fixture />)
    const callId = accept([{ text: 'Write the summary', status: 'in_progress', active_form: activeForm }])
    fallback()
    expect(warn).toHaveBeenCalledExactlyOnceWith('chat.current_todo_validation_failed', {
      sessionId: SID, callId, issues: [{ code: 'invalid_type', path: '0.active_form' }],
    })
    act(() => useChatStore.getState().handleFrame({ type: 'token', session_id: SID, message_id: 'status-answer', turn_id: 'status-turn', content: ' continuing' }))
    fallback()
    view.rerender(<Fixture phase="waiting" label="Waiting for your input" />)
    expect(screen.getByText('Waiting for your input')).toBeInTheDocument()
    view.rerender(<Fixture />)
    fallback()
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it.each(['pending', 'completed'])('does not display the form of a %s item', (status) => {
    vi.spyOn(Math, 'random').mockReturnValue(0.2)
    render(<Fixture />)
    accept([{ text: 'Write the summary', status, active_form: 'Not the current step' }])
    fallback()
  })

  it('uses the normal fallback for an unreadable list while retaining its diagnostic', () => {
    vi.spyOn(Math, 'random').mockReturnValue(0.2)
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    render(<Fixture />)
    const callId = accept([null])
    fallback()
    expect(screen.getByTestId('reply-status-indicator').textContent).not.toMatch(/Current checklist\s+unavailable/)
    expect(warn).toHaveBeenCalledExactlyOnceWith('chat.current_todo_validation_failed', {
      sessionId: SID, callId, issues: [{ code: 'invalid_type', path: '0' }],
    })
  })
})

describe('fixed fallback selection', () => {
  it('picks a phrase at first appearance, once, rather than forcing the first entry', () => {
    const random = vi.spyOn(Math, 'random').mockReturnValue(0.2)
    const view = render(<Fixture />)
    fallback()
    expect(random).toHaveBeenCalledTimes(1)
    view.rerender(<Fixture label="A goal-specific working label" />)
    fallback()
    expect(random).toHaveBeenCalledTimes(1)
  })

  it('does not change the selected phrase across former rotation ticks', () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const random = vi.spyOn(Math, 'random').mockReturnValue(0)
    render(<Fixture />)
    expect(within(screen.getByTestId('reply-status-indicator')).getByText('Thinking…')).toBeInTheDocument()
    let alternate = false
    random.mockImplementation(() => { alternate = !alternate; return alternate ? 0.2 : 0.6 })
    act(() => vi.advanceTimersByTime(8000))
    expect(within(screen.getByTestId('reply-status-indicator')).getByText('Thinking…')).toBeInTheDocument()
  })

  it('keeps the same chosen fallback across active-form changes and removal', () => {
    const random = vi.spyOn(Math, 'random').mockReturnValue(0.2)
    render(<Fixture />)
    fallback()
    accept([{ text: 'Write the summary', status: 'in_progress', active_form: 'Drafting the summary' }])
    expect(screen.getByTestId('reply-current-todo').textContent).toBe('Drafting the summary')
    accept([])
    fallback()
    expect(random).toHaveBeenCalledTimes(1)
  })
})

describe('status-text motion wiring', () => {
  it.each(['thinking', 'working'] as const)('uses the shared pulse wrapper for active and fallback copy during %s', (phase) => {
    vi.spyOn(Math, 'random').mockReturnValue(0.2)
    render(<Fixture phase={phase} />)
    fallback()
    const text = screen.getByTestId('agent-status-text')
    expect(text).toHaveClass('agent-status-text')
    expect(screen.getByTestId('reply-status-indicator')).toHaveAttribute('data-agent-status-motion', phase)
    accept([{ text: 'Write the summary', status: 'in_progress', active_form: 'Drafting the summary' }])
    expect(screen.getByTestId('reply-current-todo').textContent).toBe('Drafting the summary')
    expect(screen.getByTestId('agent-status-text')).toBe(text)
    expect(screen.getByTestId('agent-status-text')).toHaveClass('agent-status-text')
  })

  it('restarts the icon and text together for a motion epoch, not on a copy update', () => {
    const view = render(<Fixture />)
    const firstText = screen.getByTestId('agent-status-text')
    const firstIcon = screen.getByTestId('agent-icon')
    view.rerender(<Fixture phase="working" />)
    expect(screen.getByTestId('agent-status-text')).not.toBe(firstText)
    expect(screen.getByTestId('agent-icon')).not.toBe(firstIcon)
    const workingText = screen.getByTestId('agent-status-text')
    const workingIcon = screen.getByTestId('agent-icon')
    accept([{ text: 'Write the summary', status: 'in_progress', active_form: 'Drafting the summary' }])
    expect(screen.getByTestId('agent-status-text')).toBe(workingText)
    expect(screen.getByTestId('agent-icon')).toBe(workingIcon)
  })

  it('restarts text when a late identity first draws its icon', () => {
    const view = render(<Fixture identity={false} />)
    const text = screen.getByTestId('agent-status-text')
    expect(screen.queryByTestId('agent-icon')).not.toBeInTheDocument()
    view.rerender(<Fixture identity />)
    expect(screen.getByTestId('agent-icon')).toBeInTheDocument()
    expect(screen.getByTestId('agent-status-text')).not.toBe(text)
  })

  it('selects still motion for reduced-motion users in both copy modes', () => {
    vi.stubGlobal('matchMedia', (query: string) => ({ matches: query.includes('prefers-reduced-motion'), media: query, addEventListener() {}, removeEventListener() {} }))
    render(<Fixture phase="working" />)
    expect(screen.getByTestId('reply-status-indicator')).toHaveAttribute('data-agent-status-motion', 'none')
    expect(screen.getByTestId('agent-icon')).toHaveAttribute('data-motion', 'none')
    expect(screen.queryByTestId('agent-icon')?.querySelector('[data-glow]')).toBeNull()
    accept([{ text: 'Write the summary', status: 'in_progress', active_form: 'Drafting the summary' }])
    expect(screen.getByTestId('reply-status-indicator')).toHaveAttribute('data-agent-status-motion', 'none')
  })
})
