// ChatScreen.agent-picker-scoping.test.tsx
//
// UAT (Bug 2, ADR-040 browser-panel round): "switching agents via the agent
// PICKER while a turn is in-flight relabels the ENTIRE visible message
// history (older completed messages too) with the newly-picked agent's
// name/avatar in the live view (correct again on reload)."
//
// Investigation: VirtualAssistantMessageRow (historical rows) and
// AssistantMessage (the live/last-message row's name label) already prefer
// the per-message `agentId` over the session-level `activeAgentId` — see
// `messageAgentId = message.agentId ?? activeAgentId` in both. BUT
// AssistantMessageAvatar (the live row's AVATAR icon/color, mounted only for
// the currently-streaming last message via the real ThreadPrimitive.Messages)
// reads `activeAgentId` directly with no per-message fallback at all — see
// ChatScreen.tsx's AssistantMessageAvatar(). That means the instant the user
// flips the picker mid-turn, the CURRENT bubble's avatar visibly swaps to the
// newly-picked agent while its own name label (correctly) still shows the
// original agent — a live, user-visible inconsistency that self-heals on
// reload (replay re-derives everything from stored data, and the picker
// reverts to matching the session's actual agent on next navigation).
//
// This file deliberately leaves '@assistant-ui/react' UNMOCKED (mirrors
// ChatScreen.assistant-empty-bubble.test.tsx) so the real AssistantMessage /
// AssistantMessageAvatar components mount for the live row, alongside a real
// historical row via VirtualAssistantMessageRow, in the same render.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider, useMessagePartText } from '@assistant-ui/react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
// ResizeObserver availability is stubbed per-test below — it decides whether
// ChatScreen renders history through PlainMessageList (undefined) or the real
// react-virtual-backed VirtualizedMessageListInner (ResizeObserverStub); see
// the comments on each test.
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollTo) {
  Element.prototype.scrollTo = function () {}
}

// '@assistant-ui/react' is intentionally NOT mocked in this file — see header.

vi.mock('@/lib/api', () => ({
  fetchAgents: vi.fn().mockResolvedValue([
    { id: 'agent-mia', name: 'Mia', color: '#111111', icon: null },
    { id: 'agent-jim', name: 'Jim', color: '#222222', icon: null },
  ]),
  fetchSessionMessages: vi.fn().mockResolvedValue([]),
  fetchCommands: vi.fn().mockResolvedValue([]),
  fetchSkills: vi.fn().mockResolvedValue([]),
  uploadFiles: vi.fn(),
  fetchProviders: vi.fn().mockResolvedValue([]),
}))

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./SessionPanel', () => ({ SessionPanel: () => null }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./ActivityBar', () => ({ ActivityBar: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
// Real IconRenderer is unnecessary noise here — both seeded agents pass icon:null anyway,
// so AssistantMessageAvatar/VirtualAssistantMessageRow fall through to the Robot glyph;
// what we're asserting is the `title` attribute (agent name), not the icon itself.
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
// Composer Redesign (variant A1): stub the composer's picker sub-components —
// this file targets AssistantMessage/AssistantMessageAvatar/VirtualAssistantMessageRow's
// per-message agent resolution (Bug 2), not the agent PICKER's own UI (that's
// covered by composer/AgentPicker.test.tsx). Stubbing avoids mocking the
// workspaces/providers query plumbing those sub-components own.
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => {
    const { text } = useMessagePartText()
    return React.createElement('span', { 'data-testid': 'assistant-text' }, text)
  },
}))
vi.mock('./historical-markdown', () => ({
  HistoricalMessageMarkdown: ({ content }: { content: string }) =>
    React.createElement('div', { 'data-testid': 'historical-markdown' }, content),
}))

import { ChatScreen } from './ChatScreen'

const SID = 'sess_agent_picker_scoping_test'

function Providers({ children }: { children: React.ReactNode }) {
  const queryClientRef = React.useRef<QueryClient | null>(null)
  if (!queryClientRef.current) {
    queryClientRef.current = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  }
  const runtime = useOmnipusRuntime()
  return (
    <QueryClientProvider client={queryClientRef.current}>
      <AssistantRuntimeProvider runtime={runtime}>{children}</AssistantRuntimeProvider>
    </QueryClientProvider>
  )
}

/**
 * Seeds: a completed (historical) assistant message from Mia, then a
 * still-streaming (live) assistant message ALSO from Mia (her turn is
 * in-flight) — then sets the session's `activeAgentId` to Jim, simulating
 * the user flipping the agent picker mid-turn WITHOUT starting a new session
 * (ChatControls.handleAgentSelect calls setActiveSession(activeSessionId,
 * agentId, ...) — same session id, just a new activeAgentId).
 */
function seedMiaHistoryAndLiveTurnThenPickJim(): void {
  const userMsg1: ChatMessage = {
    id: 'msg_user_1', role: 'user', content: 'hi', timestamp: new Date().toISOString(), status: 'done',
  }
  const historicalMia: ChatMessage = {
    id: 'msg_asst_mia_done',
    role: 'assistant',
    content: 'Mia already answered this one.',
    timestamp: new Date().toISOString(),
    status: 'done',
    isStreaming: false,
    agentId: 'agent-mia',
  }
  const userMsg2: ChatMessage = {
    id: 'msg_user_2', role: 'user', content: 'ok, keep going', timestamp: new Date().toISOString(), status: 'done',
  }
  const liveMia: ChatMessage = {
    id: 'msg_asst_mia_live',
    role: 'assistant',
    content: 'Mia is still mid-turn...',
    timestamp: new Date().toISOString(),
    status: 'streaming',
    isStreaming: true,
    agentId: 'agent-mia',
  }
  const allMessages = [userMsg1, historicalMia, userMsg2, liveMia]
  const bucket = makeBucketMessages(allMessages)
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [SID]: {
        ...((s.sessionsById ?? {})[SID] ?? {}),
        ...bucket,
        isStreaming: true,
        isReplaying: false,
        replayCompletedForSession: SID,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        sessionTokens: 0,
        sessionCost: 0,
        rateLimitEvent: null,
        lastUserMessageAt: null,
        cancelStage: null,
        lastReceivedEventTime: null,
        spanByParentCallId: {},
        trimmedCount: 0,
      },
    },
    messages: allMessages,
    isStreaming: true,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
  // The picker switch: same session, new activeAgentId — mirrors
  // ChatControls.tsx's handleAgentSelect exactly.
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-jim' })
  useConnectionStore.setState({ connection: null, isConnected: true, connectionError: null })
}

describe('Bug 2 — agent picker mid-turn must not relabel other agents’ messages', () => {
  it('a HISTORICAL (completed) message keeps its own agent’s label+avatar after the picker switches to a different agent', async () => {
    // Force the PlainMessageList fallback: @tanstack/react-virtual computes its
    // visible range off the scroll container's measured size, which jsdom
    // always reports as 0 — VirtualizedMessageListInner would render zero rows
    // for a purely historical (no live) transcript in this environment.
    // PlainMessageList renders every row unconditionally, still through the
    // real (unmocked) VirtualAssistantMessageRow this test targets.
    vi.stubGlobal('ResizeObserver', undefined)
    seedMiaHistoryAndLiveTurnThenPickJim()

    await act(async () => {
      render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
    })

    const historical = () => {
      const bubbles = screen.getAllByTestId('assistant-message')
      return bubbles.find((b) => b.getAttribute('data-message-id') === 'msg_asst_mia_done')
    }
    expect(historical()).toBeTruthy()

    // The ['agents'] useQuery resolves asynchronously — wait for it so the
    // label/avatar reflect the real agent record (name/title), not the raw
    // agentId string fallback used while agents:[] is still loading.
    await waitFor(() => {
      expect(within(historical()!).getByTestId('agent-label').textContent).toBe('Mia')
    })

    const avatar = historical()!.querySelector('[title]')
    expect(avatar?.getAttribute('title')).toBe('Mia')
  })

  it('the LIVE (in-flight) message keeps ITS OWN agent’s avatar+label even though the picker now points at a different agent', async () => {
    // Force the REAL @tanstack/react-virtual-backed VirtualizedMessageListInner
    // path (not the PlainMessageList ResizeObserver-unavailable fallback the
    // historical test above uses) — only VirtualizedMessageListInner splits the
    // last, still-streaming message out into the real ThreadPrimitive.Messages
    // render, which is what mounts AssistantMessage/AssistantMessageAvatar (the
    // components this test targets). PlainMessageList renders every message —
    // including an in-flight one — through VirtualAssistantMessageRow instead
    // (see the D-fix comment on VirtualAssistantMessageRow in ChatScreen.tsx),
    // which would never exercise AssistantMessageAvatar at all.
    vi.stubGlobal('ResizeObserver', ResizeObserverStub)
    seedMiaHistoryAndLiveTurnThenPickJim()

    await act(async () => {
      render(
        <Providers>
          <ChatScreen />
        </Providers>,
      )
    })

    const live = () => {
      const bubbles = screen.getAllByTestId('assistant-message')
      return bubbles.find((b) => b.getAttribute('data-message-id') === 'msg_asst_mia_live')
    }
    expect(live()).toBeTruthy()

    // Name label: AssistantMessage() already computes messageAgentId from the
    // per-message agentId — this should already pass, once the ['agents']
    // query resolves.
    await waitFor(() => {
      expect(within(live()!).getByTestId('agent-label').textContent).toBe('Mia')
    })

    // Avatar (Bug 2 regression lock): AssistantMessageAvatar() used to read
    // activeAgentId directly with no per-message fallback, so this assertion
    // failed (showed Jim) before the fix. It now takes messageAgentId as a
    // prop from the caller's message.agentId ?? activeAgentId resolution, so
    // it must show Mia (this message's own agent), not Jim (whichever agent
    // is newly selected in the picker).
    const avatar = live()!.querySelector('[title]')
    expect(avatar?.getAttribute('title')).toBe('Mia')
  })
})
