// Office-document preview — preview-group facts on shortened history pages
// (office-preview spec §5.2: "same projection from live WebSocket, replay and
// REST history").
//
// A near-admission history page is SHORTENED server-side: it delivers fewer
// messages, and the snapshot replay for the delivered messages re-sends each
// preview group's media frames — the same tool call's frames can arrive twice
// (a live delivery, then the re-delivered page), and an unavailable group's
// marker can be followed by the re-delivered pages. Whatever the shortened
// page delivers, the stored group must keep its FULL facts — page_number, the
// preview record (page_count, outcome, reason) and the captions the group and
// viewer render — never a duplicate page and never a stranded marker.
//
// Harness: the REAL store reducer (useChatStore.handleFrame, the
// frame-cases precedent — no hand-rolled reducer double). Oracles from the
// spec and the §5.2 contract, never from the implementation.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useToolApprovalStore } from './toolApproval'
import { pendingCancelAckSids } from './chat/runtime-state'
import { emptySessionState } from './chat/session'
import { partitionMedia } from '@/components/chat/previewGroups'
import type { ServerFrame } from '@/lib/ws'
import type { MediaAttachment } from './types'

const SID = 'session-office-shortened-history'

function pageFrame(toolCallId: string, outcome: 'complete' | 'unavailable'): ServerFrame {
  return {
    type: 'media',
    session_id: SID,
    tool_call_id: toolCallId,
    presentation: 'page_thumbnails',
    preview: {
      source_name: 'report.docx',
      source_version: 'a1b2c3d4e5f6',
      page_count: 12,
      first_page: 1,
      last_page: 2,
      outcome,
      ...(outcome === 'unavailable' ? { unavailable_reason: 'renderer_failed' } : {}),
    },
    parts:
      outcome === 'complete'
        ? [
            { type: 'image', url: '/api/v1/media/p2', filename: 'page-2.png', content_type: 'image/png', page_number: 2, caption: 'report.docx · page 2 of 12' },
            { type: 'image', url: '/api/v1/media/p1', filename: 'page-1.png', content_type: 'image/png', page_number: 1, caption: 'report.docx · page 1 of 12' },
          ]
        : [],
  } as unknown as ServerFrame
}

/** All preview media stored anywhere in the session bucket. */
function sessionMedia(): MediaAttachment[] {
  const state = useChatStore.getState() as unknown as {
    sessionsById: Record<string, {
      messageOrder: string[]
      messagesById: Record<string, { media?: MediaAttachment[] }>
    }>
  }
  const bucket = state.sessionsById[SID]
  const media: MediaAttachment[] = []
  for (const id of bucket?.messageOrder ?? []) {
    const msg = bucket?.messagesById?.[id]
    if (msg?.media) media.push(...msg.media)
  }
  return media
}

beforeEach(() => {
  pendingCancelAckSids.clear()
  useChatStore.setState({
    sessionsById: {
      [SID]: {
        ...emptySessionState(),
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

describe('preview-group facts on shortened history pages (§5.2)', () => {
  it('a re-delivered page frame keeps the full facts and never duplicates a page', () => {
    // Live delivery, then the shortened history page re-delivers the same
    // group's frame (identical tool_call_id and page URLs).
    act(() => {
      useChatStore.getState().handleFrame(pageFrame('call-1', 'complete'))
      useChatStore.getState().handleFrame(pageFrame('call-1', 'complete'))
    })
    const media = sessionMedia()
    expect(media).toHaveLength(2) // pages 1 and 2, not 4
    expect(media.every((item) => item.presentation === 'page_thumbnails')).toBe(true)
    // The store keeps delivery order; the GROUP presents page order (the
    // partitionMedia assertion below is that contract). Facts per item:
    for (const item of media) {
      expect(item.toolCallId).toBe('call-1')
      expect([1, 2]).toContain(item.pageNumber)
      expect(item.preview?.page_count).toBe(12)
      expect(item.preview?.outcome).toBe('complete')
      expect(item.caption).toBe(`report.docx · page ${item.pageNumber} of 12`)
    }
    // The derived group the list renders still carries the facts.
    const blocks = partitionMedia(media)
    expect(blocks).toHaveLength(1)
    const group = blocks[0].kind === 'group' ? blocks[0].group : null
    expect(group?.pages.map((p) => p.pageNumber)).toEqual([1, 2])
    expect(group?.preview?.source_name).toBe('report.docx')
    expect(group?.preview?.page_count).toBe(12)
  })

  it('a re-delivered complete frame replaces a stranded unavailable marker', () => {
    // The live turn ended unavailable; the shortened history page later
    // re-delivers the group with its pages (a Retry result's persisted state).
    act(() => {
      useChatStore.getState().handleFrame(pageFrame('call-2', 'unavailable'))
      useChatStore.getState().handleFrame(pageFrame('call-2', 'complete'))
    })
    const media = sessionMedia()
    expect(media.some((item) => item.unavailableReason !== undefined)).toBe(false, 'the marker must not survive the re-delivery')
    expect(media).toHaveLength(2)
    expect(media.every((item) => item.pageNumber !== undefined)).toBe(true)
  })
})
