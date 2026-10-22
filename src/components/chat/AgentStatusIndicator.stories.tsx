import '@/styles/globals.css'
import type { Meta, StoryObj } from '@storybook/react-vite'
import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { AgentColor } from '@/lib/api/generated/schemas'
import type { ToolCallStartFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { AgentStatusIndicator } from './AgentStatusIndicator'

// not-wire-format: isolated app-story controls, not a gateway payload type.
type StatusStoryArgs = {
  phase: 'thinking' | 'working' | 'waiting'
  presentation: 'active' | 'fallback'
  startWithoutIdentity?: boolean
  interactive?: boolean
}

/** Real app adapter and icon, not a duplicate animation implementation.
 * Synthetic generated tool frames avoid any backend or model dependency. */
function StatusMotionFixture({ phase: initialPhase, presentation, startWithoutIdentity = false, interactive = false }: StatusStoryArgs) {
  const [phase, setPhase] = useState(initialPhase)
  const [identity, setIdentity] = useState(!startWithoutIdentity)
  const [form, setForm] = useState('Drafting the Q4 summary')
  useEffect(() => {
    const previousChat = useChatStore.getState()
    const previousSession = useSessionStore.getState()
    const previousConnection = useConnectionStore.getState()
    const sid = 'agent-status-motion-story'
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: sid, activeAgentId: 'mia' }, true)
    useConnectionStore.setState({ ...useConnectionStore.getInitialState(), isConnected: true }, true)
    const store = useChatStore.getState()
    store.handleFrame({ type: 'token', session_id: sid, message_id: 'story-reply', turn_id: 'story-turn', content: 'Preparing the summary' })
    const start: ToolCallStartFrame = {
      type: 'tool_call_start', session_id: sid, call_id: 'story-checklist', tool: 'set_todos',
      params: { outcome: 'Prepare the report', todos: [{ text: 'Write the Q4 summary', status: 'in_progress', ...(presentation === 'active' ? { active_form: form } : {}) }] },
    }
    store.handleFrame(start)
    store.handleFrame({ type: 'tool_call_result', session_id: sid, call_id: 'story-checklist', tool: 'set_todos', status: 'success', result: 'Checklist saved' })
    return () => {
      useChatStore.setState(previousChat, true)
      useSessionStore.setState(previousSession, true)
      useConnectionStore.setState(previousConnection, true)
      queryClient.clear()
    }
  }, [presentation, form])

  return (
    <div className="flex flex-col gap-[var(--space-3)] p-[var(--space-3)]">
      <AgentStatusIndicator phase={phase} label={phase === 'waiting' ? 'Waiting for your input' : null} figure={identity ? 'Omnipus' : undefined} role={identity ? 'general' : undefined} color={AgentColor.options[9]} name="Mia" />
      {/* Controls are a test harness, not part of the status UI. Static
          presentations stay independent of system button-palette emulation. */}
      {interactive && <div className="flex flex-wrap gap-[var(--space-2)]">
        {(['thinking', 'working', 'waiting'] as const).map((motion) => <Button key={motion} variant="outline" aria-pressed={phase === motion} onClick={() => setPhase(motion)}>{motion[0].toUpperCase() + motion.slice(1)}</Button>)}
        <Button variant="outline" onClick={() => setIdentity((shown) => !shown)}>{identity ? 'Hide agent' : 'Show agent'}</Button>
        {presentation === 'active' && <Button variant="outline" onClick={() => setForm('Reviewing the final notes')}>Update item</Button>}
      </div>}
    </div>
  )
}

const meta = {
  title: 'Chat/AgentStatusIndicator',
  render: StatusMotionFixture,
  args: { phase: 'thinking', presentation: 'active', startWithoutIdentity: false, interactive: false },
  parameters: {
    layout: 'padded', docs: { story: { inline: false } },
    designSystem: {
      browserAssertions: [{ selector: '[data-testid="reply-status-indicator"]', attribute: 'data-testid', value: 'reply-status-indicator' }],
      motionTargets: ['.agent-status-text', '[data-testid="agent-icon"] > [data-ink]'],
    },
  },
} satisfies Meta<StatusStoryArgs>
export default meta
type Story = StoryObj<typeof meta>

export const ThinkingActive: Story = {}
export const ThinkingFallback: Story = { args: { presentation: 'fallback' } }
export const WorkingActive: Story = { args: { phase: 'working' } }
export const WorkingFallback: Story = { args: { phase: 'working', presentation: 'fallback' } }
export const WaitingNotice: Story = { args: { phase: 'waiting' } }
export const LateIdentity: Story = { args: { startWithoutIdentity: true, interactive: true } }
export const MotionTransitions: Story = { args: { interactive: true } }
