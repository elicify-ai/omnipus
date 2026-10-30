// Office-document preview — SPA store thumbnail groups (office-preview spec
// §5.2, FR-035; TDD row T-48 store leg, T-62 reconnect leg; BDD B-11/B-12/B-40).
//
// Oracles from the spec (§5.2), never from the implementation:
//   - "SPA store: media grouped by (message, tool_call_id) instead of
//     flattened; dedup key (tool_call_id, URL); screenshots (no
//     tool_call_id) unchanged."
//   - MediaPart.page_number (>= 1) reaches the store so the viewer can
//     page within a group; the preview record (outcome, reason) reaches
//     the store so the unavailable card ("Page previews unavailable —
//     <reason>", §5.3) can render after reload without re-rendering.
//   - An unavailable page_thumbnails frame carries NO parts; the store
//     must record the unavailable state, never the generic "_1 attachment
//     could not be displayed._" notice.
//
// RED reasons on the pre-change code (verified by reading
// replay-and-status-frames.ts::handleReplayAndStatusFrame before writing):
// the media reducer drops tool_call_id/presentation/preview/page_number,
// dedupes by URL, and turns an empty-parts frame into the generic notice.
import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useToolApprovalStore } from './toolApproval'
import { pendingCancelAckSids } from './chat/runtime-state'
import type { ServerFrame } from '@/lib/ws'

const SID = 'session-office-preview'

const PAGE_FRAME = {
  type: 'media',
  session_id: SID,
  tool_call_id: 'call-prev-1',
  presentation: 'page_thumbnails',
  preview: {
    source_name: 'report.docx',
    source_version: 'a1b2c3d4e5f6',
    page_count: 12,
    first_page: 1,
    last_page: 2,
    outcome: 'complete',
  },
  parts: [
    { type: 'image', url: '/api/v1/media/p1', filename: 'page-1.png', content_type: 'image/png', page_number: 1, caption: 'report.docx · page 1 of 12' },
    { type: 'image', url: '/api/v1/media/p2', filename: 'page-2.png', content_type: 'image/png', page_number: 2, caption: 'report.docx · page 2 of 12' },
  ],
} as unknown as ServerFrame

const SECOND_GROUP_FRAME = {
  ...PAGE_FRAME,
  tool_call_id: 'call-prev-2',
  parts: [
    { type: 'image', url: '/api/v1/media/p1', filename: 'page-1.png', content_type: 'image/png', page_number: 1, caption: 'report.docx · page 1 of 12' },
  ],
} as unknown as ServerFrame

const UNAVAILABLE_FRAME = {
  type: 'media',
  session_id: SID,
  tool_call_id: 'call-prev-3',
  presentation: 'page_thumbnails',
  preview: {
    source_name: 'report.docx',
    source_version: 'a1b2c3d4e5f6',
    page_count: 12,
    first_page: 1,
    last_page: 12,
    outcome: 'unavailable',
    unavailable_reason: 'renderer_missing',
  },
  parts: [],
} as unknown as ServerFrame

function sessionMedia(): Array<Record<string, unknown>> {
  const sid = useSessionStore.getState().activeSessionId ?? SID
  const state = useChatStore.getState() as unknown as {
    sessionsById: Record<string, {
      messageOrder: string[]
      messagesById: Record<string, { media?: Array<Record<string, unknown>> }>
    }>
  }
  const bucket = state.sessionsById[sid]
  const media: Array<Record<string, unknown>> = []
  for (const id of bucket?.messageOrder ?? []) {
    const msg = bucket?.messagesById?.[id]
    if (msg?.media) media.push(...msg.media)
  }
  return media
}

function noticeText(): string {
  const sid = useSessionStore.getState().activeSessionId ?? SID
  const state = useChatStore.getState() as unknown as {
    sessionsById: Record<string, {
      messageOrder: string[]
      messagesById: Record<string, { content?: string }>
    }>
  }
  const bucket = state.sessionsById[sid]
  return (bucket?.messageOrder ?? []).map((id) => bucket?.messagesById?.[id]?.content ?? '').join('\n')
}

beforeEach(() => {
  pendingCancelAckSids.clear()
  useChatStore.setState({
    sessionsById: {
      [SID]: {
        messageOrder: ['m1'],
        messagesById: { m1: { id: 'm1', role: 'assistant', content: 'done', timestamp: new Date().toISOString() } },
      },
    },
    messages: [],
    isStreaming: false,
    toolCalls: {},
    toolCallOrder: [],
    textAtToolCallStart: {},
    sessionTokens: 0,
    sessionCost: 0,
    isReplaying: false,
    replayCompletedForSession: null,
    rateLimitEvent: null,
    lastUserMessageAt: null,
    cancelStage: null,
    lastReceivedEventTime: null,
  } as Partial<typeof useChatStore extends { setState: (s: infer S) => void } ? S : never>)
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: null, activeAgentType: null })
  useConnectionStore.setState({ connectionError: null })
  useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
})

describe('office preview thumbnail groups in the chat store (§5.2)', () => {
  it('carries page_number and the preview record so the group and viewer can render', () => {
    act(() => {
      useChatStore.getState().handleFrame({ frame: PAGE_FRAME } as never)
    })
    const media = sessionMedia()
    expect(media.length).toBe(2)
    for (const [i, part] of media.entries()) {
      // §5.2: parts travel in page order with page_number on each part.
      expect(part.pageNumber ?? part.page_number).toBe(i + 1)
    }
  })

  it('keeps two identical previews as separate groups (dedup key = tool_call_id + URL)', () => {
    act(() => {
      useChatStore.getState().handleFrame({ frame: PAGE_FRAME } as never)
      useChatStore.getState().handleFrame({ frame: SECOND_GROUP_FRAME } as never)
    })
    // B-40: the same page URL in a second preview group must survive —
    // today's URL-only dedupe collapses it into the first group.
    expect(sessionMedia().length).toBe(3)
  })

  it('records the unavailable state instead of the generic not-displayed notice', () => {
    act(() => {
      useChatStore.getState().handleFrame({ frame: UNAVAILABLE_FRAME } as never)
    })
    // §5.3: the person sees the persistent "Page previews unavailable —
    // <reason>" card (renderer_missing shows install guidance, no Retry).
    // The generic markdown notice means the unavailable card cannot render.
    expect(noticeText()).not.toContain('could not be displayed')
    const media = sessionMedia()
    const unavailable = media.find((m) => m.unavailableReason !== undefined || m.unavailable_reason !== undefined)
    expect(unavailable).toBeDefined()
  })
})
