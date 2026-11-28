// F1 / B07, office-preview §5.2–5.3: a cached source is reconciled as a
// whole thumbnail group, with saved state winning unless live changed after
// snapshot capture. Real store/history fold; generated frames only, no mocks.
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useToolApprovalStore } from './toolApproval'
import { useUiStore } from './ui'
import type { MediaFrame, ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { entry, expectValidFrame, expectValidPage, page, pos, replay, snapshot } from '../../tests/chat-history-paging/fixtures'

const SID = 'office-cached-snapshot'
const SOURCE = 'office-cached-source'
const BOOT = 'office-cached-boot'
const TARGET = 'retry-target'
const OTHER = 'other-unavailable'
const SHARED = '/api/v1/media/shared-page'
const SHOT = '/api/v1/media/interleaved-screenshot'
const SOURCE_CALLS = [TARGET, 'same-url-first', 'screenshot', 'same-url-second', OTHER]

function preview(call: string, outcome: 'complete' | 'unavailable', urls: string[] = []): MediaFrame {
  return {
    type: 'media', session_id: SID, tool_call_id: call, presentation: 'page_thumbnails',
    preview: {
      source_name: `${call}.pdf`, source_version: '012345abcdef', page_count: 2,
      first_page: 1, last_page: 2, outcome,
      ...(outcome === 'unavailable' ? { unavailable_reason: urls.length ? 'storage_failed' : 'renderer_failed' } : {}),
    },
    parts: urls.map((url, i) => ({ type: 'image', url, filename: `page-${i + 1}.png`, content_type: 'image/png', page_number: i + 1, caption: `${call}.pdf · page ${i + 1} of 2` })),
  }
}

function savedFrames(): MediaFrame[] {
  return [
    preview(TARGET, 'unavailable'), preview('same-url-first', 'complete', [SHARED]),
    { type: 'media', session_id: SID, parts: [{ type: 'image', url: SHOT, filename: 'screenshot.png', content_type: 'image/png' }] },
    preview('same-url-second', 'complete', [SHARED]), preview(OTHER, 'unavailable'),
  ]
}

function feed(...frames: ServerFrame[]) {
  frames.forEach(expectValidFrame)
  act(() => frames.forEach(frame => useChatStore.getState().handleFrame(frame)))
}

function beginSnapshot(cut: number) {
  const source = entry(1, { id: SOURCE, type: 'tool_call', role: 'assistant', content: '' })
  const selected = page([source], { sessionId: SID, rows: 5 })
  feed(
    { ...snapshot(selected), seq: cut, boot_id: BOOT },
    { type: 'session_state', session_id: SID, user_id: 'office-reader', pending_approvals: [], emitted_at: '2026-11-24T00:00:00Z', history_state: { status: 'available' } },
  )
}

function finishSnapshot(cut: number) {
  const origin = { position: pos(1), entry_id: SOURCE }
  const historical: ServerFrame[] = savedFrames().flatMap((frame, i) => {
    const call = SOURCE_CALLS[i]
    const tool = call === 'screenshot' ? 'browser_screenshot' : 'preview_document'
    return [
      { type: 'tool_call_start', session_id: SID, tool, call_id: call, params: {}, history_origin: origin },
      { type: 'tool_call_result', session_id: SID, tool, call_id: call, status: 'success', result: 'saved result', history_origin: origin },
      { ...frame, history_origin: origin },
    ]
  })
  feed(...historical,
    { type: 'done', session_id: SID, stats: { frames_emitted: historical.length } },
    { type: 'catch_up_complete', session_id: SID, seq: cut, boot_id: BOOT, mode: 'snapshot' },
  )
  expect(bucket().history.historyLoaded, 'instrument: real source-owned replay commits its metadata').toBe(true)
  expect(bucket().history.readerPositions).toEqual([pos(1)])
}

function bucket() { return useChatStore.getState().sessionsById[SID] }
function media() { return bucket().messageOrder.flatMap(id => bucket().messagesById[id].media ?? []) }
function group(call: string) { return media().filter(item => item.toolCallId === call && item.presentation === 'page_thumbnails') }

function expectUnrelatedGroups() {
  expect(media().filter(item => item.url === SHARED).map(item => item.toolCallId).sort()).toEqual(['same-url-first', 'same-url-second'])
  expect(group(OTHER)).toHaveLength(1)
  expect(group(OTHER)[0]).toMatchObject({ url: '', unavailableReason: 'renderer_failed' })
  expect(media().filter(item => item.url === SHOT)).toHaveLength(1)
  expect(media().find(item => item.url === SHOT)?.toolCallId).toBeUndefined()
}

function cachedThenUnsavedRetry() {
  beginSnapshot(10)
  finishSnapshot(10)
  expect(group(TARGET)).toHaveLength(1)
  expect(group(TARGET)[0]).toMatchObject({ url: '', unavailableReason: 'renderer_failed' })
  expectUnrelatedGroups()
  feed({ ...preview(TARGET, 'unavailable', ['/api/v1/media/unsaved-1', '/api/v1/media/unsaved-2']), seq: 11 })
  expect(group(TARGET).map(item => item.url)).toEqual(['/api/v1/media/unsaved-1', '/api/v1/media/unsaved-2', ''])
  expect(group(TARGET).at(-1)?.unavailableReason).toBe('storage_failed')
}

afterEach(() => useUiStore.setState({ toasts: [] }))

beforeEach(() => {
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {}, isStreaming: false, toolCalls: {}, toolCallOrder: [], textAtToolCallStart: {}, isReplaying: false })
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: null, activeAgentType: null })
  useConnectionStore.setState({ connection: null, connectionError: null })
  useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
})

describe('Office cached snapshot group ownership (F1 / B07)', () => {
  it('replaces unchanged cached unsaved pages with exactly the saved unavailable marker', () => {
    cachedThenUnsavedRetry()
    beginSnapshot(11)
    finishSnapshot(11)
    expect(group(TARGET).map(item => item.url), 'F1: no live-only URL may survive authoritative saved replacement').toEqual([''])
    expect(group(TARGET)[0]).toMatchObject({ toolCallId: TARGET, unavailableReason: 'renderer_failed', preview: { outcome: 'unavailable', page_count: 2 } })
    expectUnrelatedGroups()
  })

  it('preserves a newer live Retry after capture instead of unioning older saved state into it', () => {
    cachedThenUnsavedRetry()
    beginSnapshot(11)
    feed({ ...preview(TARGET, 'complete', ['/api/v1/media/new-live-1', '/api/v1/media/new-live-2']), seq: 12 })
    expect(bucket().cursor?.seq, 'instrument: the post-capture live frame passes the sequence gate').toBe(12)
    expect(group(TARGET).map(item => item.url)).toEqual(['/api/v1/media/new-live-1', '/api/v1/media/new-live-2'])
    finishSnapshot(11)
    expect(group(TARGET).map(item => item.url), 'F1 converse: the post-capture live group wins atomically').toEqual(['/api/v1/media/new-live-1', '/api/v1/media/new-live-2'])
    expect(group(TARGET).every(item => item.preview?.outcome === 'complete')).toBe(true)
    expectUnrelatedGroups()
  })

  it('reconciles each thumbnail group independently on a still-streaming combined assistant row', () => {
    // Team-lead update 3: saved state wins for an unchanged group even while
    // the combined row keeps streaming; a group changed after capture wins live.
    const display = 'office-combined-first'
    const tools = 'office-combined-tools'
    const turn = 'office-combined-active-turn'
    const firstText = 'Inspecting the document. '
    const secondText = 'The turn is still running.'
    const state: ServerFrame = {
      type: 'session_state', session_id: SID, user_id: 'office-reader', pending_approvals: [],
      emitted_at: '2026-11-24T00:00:00Z', history_state: { status: 'available' },
      active_turn: { turn_id: turn, agent_id: 'mia', started_at: '2026-11-24T00:00:00Z' },
    }
    feed(state,
      { type: 'token', session_id: SID, turn_id: turn, agent_id: 'mia', message_id: display, content: firstText, seq: 1 },
      { type: 'token', session_id: SID, turn_id: turn, agent_id: 'mia', message_id: SOURCE, content: secondText, seq: 2 },
    )
    expect(bucket().messageOrder, 'instrument: real tokens produce one combined row').toEqual([display])
    expect(bucket().messagesById[display]).toMatchObject({
      content: firstText + secondText, isStreaming: true, turnId: turn, mergedReplayIds: [SOURCE],
    })
    expect(bucket().messagesById[display].historyParts?.map(part => part.sourceId)).toEqual([display, SOURCE])
    const sources = [
      entry(1, { id: display, role: 'assistant', turn_id: turn, content: firstText }),
      entry(2, { id: SOURCE, role: 'assistant', turn_id: turn, content: secondText }),
      entry(3, { id: tools, type: 'tool_call', role: 'assistant', turn_id: turn, content: '',
        tool_calls: [TARGET, OTHER].map(id => ({ id, tool: 'preview_document', status: 'success' as const,
          content_state: 'full' as const, parameters: {}, result: { text: 'saved result' } })),
      }),
    ]
    const selected = page(sources, { sessionId: SID, rows: 4 })
    expectValidPage(selected, [pos(1), pos(2), pos(3)], 4)
    const begin = (cut: number) => feed({ ...snapshot(selected), seq: cut, boot_id: BOOT }, state)
    const finish = (cut: number) => {
      const history_origin = { position: pos(3), entry_id: tools, turn_id: turn }
      feed(...sources.slice(0, 2).map(source => replay(source, SID)),
        ...[TARGET, OTHER].flatMap((call): ServerFrame[] => [
          { type: 'tool_call_start', session_id: SID, tool: 'preview_document', call_id: call, params: {}, agent_id: 'mia', history_origin },
          { type: 'tool_call_result', session_id: SID, tool: 'preview_document', call_id: call, status: 'success', result: 'saved result', history_origin },
          { ...preview(call, 'unavailable'), history_origin },
        ]),
        { type: 'done', session_id: SID },
        { type: 'catch_up_complete', session_id: SID, seq: cut, boot_id: BOOT, mode: 'snapshot' },
      )
      expect(bucket().history.historyLoaded).toBe(true)
      expect(bucket().history.readerPositions).toEqual([pos(1), pos(2), pos(3)])
    }
    begin(10)
    finish(10)
    expect(group(TARGET).map(item => item.url)).toEqual([''])
    expect(group(OTHER).map(item => item.url)).toEqual([''])
    expect(bucket().messagesById[display].isStreaming, 'instrument: history did not finish the active turn').toBe(true)
    feed({ ...preview(TARGET, 'unavailable', ['/api/v1/media/combined-unsaved-1', '/api/v1/media/combined-unsaved-2']), seq: 11 })
    expect(group(TARGET).map(item => item.url)).toEqual(['/api/v1/media/combined-unsaved-1', '/api/v1/media/combined-unsaved-2', ''])
    expect(group(TARGET).at(-1)?.unavailableReason).toBe('storage_failed')
    begin(11)
    feed({ ...preview(OTHER, 'complete', ['/api/v1/media/combined-live-1', '/api/v1/media/combined-live-2']), seq: 12 })
    expect(bucket().cursor?.seq, 'instrument: the changed control is admitted after capture').toBe(12)
    expect(group(OTHER).map(item => item.url)).toEqual(['/api/v1/media/combined-live-1', '/api/v1/media/combined-live-2'])
    finish(11)
    expect(bucket().messageOrder, 'one combined display row survives reconciliation').toEqual([display])
    expect(bucket().messagesById[display]).toMatchObject({ content: firstText + secondText, isStreaming: true, turnId: turn })
    expect(bucket().messagesById[display].historyParts?.map(part => part.sourceId)).toEqual([display, SOURCE, tools])
    expect(group(OTHER).map(item => item.url), 'F1 residual control: the group changed live stays whole').toEqual(['/api/v1/media/combined-live-1', '/api/v1/media/combined-live-2'])
    expect(group(OTHER).every(item => item.preview?.outcome === 'complete')).toBe(true)
    expect(group(TARGET).map(item => item.url), 'F1 residual: unchanged unsaved Retry pages disappear despite the streaming combined row').toEqual([''])
    expect(group(TARGET)[0]).toMatchObject({ toolCallId: TARGET, unavailableReason: 'renderer_failed', preview: { outcome: 'unavailable' } })
  })
})
