// UAT B06/B07; office-preview spec §4.3, §5.2 and §5.3.
// Exercise the real store, snapshot replay and media list together. A card-only
// render misses nonempty storage_failed frames; a media-only reducer test misses
// history reconciliation. Only gateway input is supplied as generated frames.
import { act } from 'react'
import { beforeEach, describe, expect, it } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { AssistantMediaList } from './AssistantMediaList'
import { useChatStore } from '@/store/chat'
import { emptySessionState } from '@/store/chat/session'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useToolApprovalStore } from '@/store/toolApproval'
import type { MediaFrame, ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { entry, expectValidFrame, page, pos, snapshot } from '../../../tests/chat-history-paging/fixtures'

const SID = 'office-uat-preview-session'
const ENTRY = 'office-uat-preview-entry'
const CALLS = ['preview-before', 'preview-retried', 'preview-after'] as const
// Independent four-page document, not the number of frames the reducer returns.
const PAGE_COUNT = 4

function previewFrame(call: string, outcome: 'complete' | 'unavailable', batch?: string): MediaFrame {
  return {
    type: 'media', session_id: SID, tool_call_id: call, presentation: 'page_thumbnails',
    preview: {
      source_name: `${call}.pdf`, source_version: 'a1b2c3d4e5f6', page_count: PAGE_COUNT,
      first_page: 1, last_page: PAGE_COUNT, outcome,
      ...(outcome === 'unavailable' ? { unavailable_reason: batch ? 'storage_failed' : 'renderer_damaged' } : {}),
    },
    parts: batch ? Array.from({ length: PAGE_COUNT }, (_, i) => ({
      type: 'image', url: `/api/v1/media/${call}-${batch}-${i + 1}`, filename: `page-${i + 1}.png`,
      content_type: 'image/png', page_number: i + 1, caption: `${call}.pdf · page ${i + 1} of ${PAGE_COUNT}`,
    })) : [],
  }
}

function feed(...frames: ServerFrame[]) {
  // Instrument: schema rejection is not a regression proof.
  frames.forEach(expectValidFrame)
  act(() => frames.forEach(frame => useChatStore.getState().handleFrame(frame)))
}

function toolFrames(call: string): ServerFrame[] {
  return [
    { type: 'tool_call_start', session_id: SID, tool: 'preview_document', call_id: call, params: { file: `${call}.pdf` } },
    { type: 'tool_call_result', session_id: SID, tool: 'preview_document', call_id: call, status: 'success', result: 'document preview result' },
  ]
}

function initialCalls() {
  for (const call of CALLS) feed(...toolFrames(call), previewFrame(call, 'unavailable'))
}

function MediaThread() {
  const bucket = useChatStore(state => state.sessionsById[SID])
  return <>{bucket?.messageOrder.map(id => <AssistantMediaList key={id} media={bucket.messagesById[id]?.media ?? []} />)}</>
}

function groups() {
  return screen.queryAllByTestId('preview-thumbnail-group')
}

function groupFor(call: string) {
  const matches = groups().filter(group => group.getAttribute('data-preview-group') === call)
  expect(matches, `§5.3: exactly one group for ${call}, never a missing or duplicated call`).toHaveLength(1)
  return matches[0]
}

function captions(call: string) {
  return within(groupFor(call)).queryAllByText(/\.pdf · page \d of \d/, { selector: 'p' }).map(node => node.textContent)
}

function recordedReplay(frames: MediaFrame[]) {
  // One recorded tool-call entry containing three distinct calls: row weight
  // is three, not the four pages, frame count, or number of rendered messages.
  const saved = entry(1, { id: ENTRY, type: 'tool_call', role: 'assistant', content: '' })
  const selected = page([saved], { sessionId: SID, rows: frames.length })
  const origin = { position: pos(1), entry_id: ENTRY }
  const historical = frames.flatMap(frame => [
    ...toolFrames(frame.tool_call_id!).map(part => ({ ...part, history_origin: origin })),
    { ...frame, history_origin: origin },
  ])
  feed(
    snapshot(selected),
    { type: 'session_state', session_id: SID, user_id: 'office-reader', pending_approvals: [], emitted_at: '2026-11-23T00:00:00Z', history_state: { status: 'available' } },
    ...historical,
    { type: 'done', session_id: SID, stats: { frames_emitted: historical.length } },
    { type: 'catch_up_complete', session_id: SID, seq: 100, boot_id: 'history-boot', mode: 'snapshot' },
  )
  expect(useChatStore.getState().sessionsById[SID].history.historyLoaded, 'instrument: the authoritative replay must commit, not fail its metadata checks').toBe(true)
}

beforeEach(() => {
  useChatStore.setState({
    sessionsById: { [SID]: {
      ...emptySessionState(), messageOrder: [ENTRY], messagesById: {
        [ENTRY]: { id: ENTRY, role: 'assistant', content: '', timestamp: '2026-11-23T00:00:00Z', status: 'done' },
      },
    } }, messages: [], isStreaming: false, toolCalls: {}, toolCallOrder: [], textAtToolCallStart: {}, isReplaying: false,
  })
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: null, activeAgentType: null })
  useConnectionStore.setState({ connection: null, connectionError: null })
  useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
})

describe('Office Retry across live delivery and recorded replay (UAT B06/B07)', () => {
  it('keeps the save-failure warning and Retry when rendered pages accompany storage_failed', () => {
    render(<MediaThread />)
    initialCalls()
    expect(groups().map(group => group.getAttribute('data-preview-group'))).toEqual([...CALLS])
    feed(previewFrame(CALLS[1], 'unavailable', 'unsaved'))

    const failed = within(groupFor(CALLS[1]))
    expect(failed.queryByText('Page previews could not be saved'), 'B07: live pages are not an unqualified saved success').not.toBeNull()
    expect(failed.queryByRole('button', { name: 'Retry' }), 'B07: failed persistence must leave a recovery control').not.toBeNull()
    expect(groupFor(CALLS[0]).getAttribute('data-outcome')).toBe('unavailable')
    expect(groupFor(CALLS[2]).getAttribute('data-outcome')).toBe('unavailable')
  })

  it('does not keep unsaved live pages after an authoritative reload of the original unavailable call', () => {
    render(<MediaThread />)
    initialCalls()
    feed(previewFrame(CALLS[1], 'unavailable', 'unsaved'))
    expect(captions(CALLS[1]), 'instrument: this tab really received the four live-only pages').toHaveLength(PAGE_COUNT)

    // Failed settle wrote nothing (§5.2): all three persisted calls are still
    // unavailable. The snapshot must replace, not union, the unsaved batch.
    recordedReplay(CALLS.map(call => previewFrame(call, 'unavailable')))
    expect(captions(CALLS[1]), 'B07: reload must not make a failed save appear durable').toEqual([])
    expect(groups().map(group => group.getAttribute('data-preview-group'))).toEqual([...CALLS])
    expect(within(groupFor(CALLS[1])).queryByRole('button', { name: 'Retry' })).not.toBeNull()
  })

  it('replays a successful middle-call Retry once with four pages and both neighbouring cards intact', () => {
    render(<MediaThread />)
    initialCalls()
    feed(previewFrame(CALLS[1], 'complete', 'saved'))
    recordedReplay(CALLS.map(call => previewFrame(call, call === CALLS[1] ? 'complete' : 'unavailable', call === CALLS[1] ? 'saved' : undefined)))

    expect(groups().map(group => group.getAttribute('data-preview-group')), 'B06: reload cannot drop another call or duplicate the retried one').toEqual([...CALLS])
    expect(captions(CALLS[1])).toEqual([1, 2, 3, 4].map(n => `${CALLS[1]}.pdf · page ${n} of 4`))
    expect(within(groupFor(CALLS[1])).queryByTestId('preview-unavailable-card')).toBeNull()
    for (const call of [CALLS[0], CALLS[2]]) {
      expect(within(groupFor(call)).getByTestId('preview-unavailable-card').getAttribute('data-reason')).toBe('renderer_damaged')
    }
  })

  it('replaces a previous successful Retry batch rather than accumulating eight pages for the same four-page call', () => {
    render(<MediaThread />)
    initialCalls()
    feed(previewFrame(CALLS[1], 'complete', 'first-save'))
    // Replaying or retrying can return fresh media URLs for the same call.
    // §5.2 settles its original group; it does not create a second batch.
    feed(previewFrame(CALLS[1], 'complete', 'later-save'))
    expect(captions(CALLS[1])).toEqual([1, 2, 3, 4].map(n => `${CALLS[1]}.pdf · page ${n} of 4`))
    expect(groups().map(group => group.getAttribute('data-preview-group'))).toEqual([...CALLS])
  })
})
