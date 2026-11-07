/**
 * RED: a workspace file card survives reload (chat-links US-5 acceptance
 * scenario 4, BS-5.4) and tool media stay on the folded row (chat-history
 * paging AC-D2-1). The card is the media frame replayed with the send_file
 * record: filename plan.md, Library path notes/plan.md.
 *
 * Driven through the real snapshot commit and catch_up_complete, not a
 * direct fold or merge call. Expected values are the spec's, not a run of
 * the current fold. CHECK mutation: stop carrying media onto the folded
 * row (leave tool_calls and spans), and stop keeping an already loaded
 * card when a saved record with no media is rebound.
 */
import { beforeEach, afterEach, expect, it } from 'vitest'
import type { MediaFrame, MediaPart } from '@/lib/api/generated/asyncapi-types'
import type { Message } from '@/lib/api/generated/openapi-types'
import { entry } from '../../tests/chat-history-paging/fixtures'
import { reset, cleanup } from '../../tests/chat-history-paging/final-gate-harness'
import {
  SID, TURN, assistantRows, begin, empty, native, prose, rest,
} from '../../tests/chat-history-paging/s11-fold-harness'
import { useChatStore } from './chat'

const PLAN_URL = '/api/v1/media/plan-md'
const NOTES_URL = '/api/v1/media/notes-md'
const IMAGE_URL = '/api/v1/media/board-png'
const PLAN_PATH = 'notes/plan.md'
const NOTES_PATH = 'notes/notes.md'
const IMAGE_PATH = 'notes/board.png'

const PLAN_PART: MediaPart = {
  type: 'file', url: PLAN_URL, filename: 'plan.md', content_type: 'application/octet-stream',
  library_ref: { workspace_id: 'W', path: PLAN_PATH },
}
const NOTES_PART: MediaPart = {
  type: 'file', url: NOTES_URL, filename: 'notes.md', content_type: 'application/octet-stream',
  library_ref: { workspace_id: 'W', path: NOTES_PATH },
}
const IMAGE_PART: MediaPart = {
  type: 'image', url: IMAGE_URL, filename: 'board.png', content_type: 'image/png',
  library_ref: { workspace_id: 'W', path: IMAGE_PATH },
}

/** The store card BS-5.4 still shows after the media frame is replayed. */
const PLAN_CARD = {
  type: 'file' as const, url: PLAN_URL, filename: 'plan.md', contentType: 'application/octet-stream',
  libraryRef: { workspace_id: 'W', path: PLAN_PATH },
}

beforeEach(() => { reset(); begin() })
afterEach(cleanup)

function sendFile(rank: number, id: string, callId: string, path: string): Message {
  return entry(rank, {
    id, role: 'assistant', agent_id: 'mia', turn_id: TURN, content: '', type: 'tool_call',
    tool_calls: [{
      id: callId, tool: 'send_file', status: 'success', content_state: 'full',
      parameters: { path }, result: { text: 'sent' },
    }],
  })
}

function mediaFrame(source: Message, part: MediaPart): MediaFrame {
  return {
    type: 'media', session_id: SID, parts: [part],
    history_origin: { position: source.history_position!, entry_id: source.id, turn_id: source.turn_id },
  }
}

/** Empty record, send_file, then the reply. The real reload shape. */
function sentFileTurn(part: MediaPart = PLAN_PART) {
  const blank = empty(1, 'blank-plan')
  const sent = sendFile(2, 'send-plan', 'call-plan', part.library_ref?.path ?? PLAN_PATH)
  const reply = prose(3, 'reply-plan', 'Sent the plan.')
  return { sources: [blank, sent, reply], frames: [mediaFrame(sent, part)] }
}

function foldedRow() {
  const rows = assistantRows()
  expect(rows, 'US-5 AS-4: the turn is one folded row after catch_up_complete').toHaveLength(1)
  return rows[0]
}

it('keeps the plan.md card on the folded row after the snapshot commits', () => {
  const turn = sentFileTurn()
  native(turn.sources, false, turn.frames)
  expect(foldedRow().media, 'BS-5.4: the reloaded card names plan.md at notes/plan.md').toEqual([PLAN_CARD])
})

it('keeps the plan.md card after a second snapshot in the same tab', () => {
  const turn = sentFileTurn()
  native(turn.sources, false, turn.frames)
  native(turn.sources, false, turn.frames)
  expect(foldedRow().media, 'BS-5.4: a second attach still shows plan.md at notes/plan.md').toEqual([PLAN_CARD])
})

it('keeps a sent image on the folded row', () => {
  const turn = sentFileTurn(IMAGE_PART)
  native(turn.sources, false, turn.frames)
  expect(foldedRow().media, 'AC-D2-1: the image is still on the reloaded row').toEqual([{
    type: 'image', url: IMAGE_URL, filename: 'board.png', contentType: 'image/png',
    libraryRef: { workspace_id: 'W', path: IMAGE_PATH },
  }])
})

it('keeps media that arrived on the second tool row of a tool-only segment', () => {
  const first = sendFile(2, 'tool-a', 'call-a', 'notes/a.md')
  const second = sendFile(4, 'tool-b', 'call-b', PLAN_PATH)
  native([first, second], false, [mediaFrame(second, PLAN_PART)])
  expect(foldedRow().media, 'AC-D2-1: media on the second tool row is on the one folded row').toEqual([PLAN_CARD])
})

it('keeps an already loaded card when the REST window refresh has no media', async () => {
  const turn = sentFileTurn()
  native(turn.sources, false, turn.frames)
  useChatStore.getState().enterHistoryReading(SID)
  // BS-5.4: the REST page is not how the card arrives. The refresh must not wipe one already loaded.
  await rest(turn.sources, 'refresh')
  expect(foldedRow().media, 'BS-5.4: a REST window refresh keeps plan.md at notes/plan.md').toEqual([PLAN_CARD])
})

it('shows one card when the same url arrives twice', () => {
  const once = sentFileTurn()
  native(once.sources, false, [mediaFrame(once.sources[1], PLAN_PART), mediaFrame(once.sources[1], PLAN_PART)])
  expect(foldedRow().media, 'the same url twice is one plan.md card').toEqual([PLAN_CARD])
})

it('shows two cards when one turn sends two different files', () => {
  const sent = sendFile(2, 'send-two', 'call-two', PLAN_PATH)
  const reply = prose(3, 'reply-two', 'Sent both.')
  native([empty(1, 'blank-two'), sent, reply], false, [mediaFrame(sent, PLAN_PART), mediaFrame(sent, NOTES_PART)])
  expect(foldedRow().media, 'two different files in one turn are two cards').toEqual([
    PLAN_CARD,
    {
      type: 'file', url: NOTES_URL, filename: 'notes.md', contentType: 'application/octet-stream',
      libraryRef: { workspace_id: 'W', path: NOTES_PATH },
    },
  ])
})
