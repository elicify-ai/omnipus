// PF=A / seams Q2-Q5; provider-messages D7/D12/D17, section 6, FB-2/FB-4.
// Only fetch is doubled. The real HTTP/generated validators, latest-history
// action/merge, runtime and both actual system-row renderers remain real.
// Baseline: normal merge of GREEN baaca6ff8 at87726619ec1c3f91a2ae3f313fcd6c8eb82511cb.
// PRE-contract item rejection -> placeholder is the explicit team-lead Q1=A
// RED root cause, not an isolated schema-valid mapper claim. GREEN/mutation
// and proof-of-failability are deferred to a fresh CHECK instance.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { AssistantRuntimeProvider, ThreadPrimitive } from '@assistant-ui/react'
import type { Message as WireMessage } from '@/lib/api/generated/openapi-types'
import type { ProviderFallbackFrame, ProviderFallbackNote, ServerFrame, SessionSnapshotFrame } from '@/lib/api/generated/asyncapi-types'
import { Message as MessageSchema, SessionHistoryResponse as PageSchema } from '@/lib/api/generated/schemas'
import { getApiSchemaErrorCount, resetApiSchemaErrorCount } from '@/lib/api/http'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { parseFrameSafe } from '@/lib/ws'
import { useChatStore, getMessages } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { queryClient } from '@/lib/queryClient'
import { SystemMessage, VirtualSystemMessageRow } from '@/components/chat/SystemMessageRows'
import { UserMessage, AssistantMessage } from '@/components/chat/ChatScreen'
import { entry, page, pos, replay, STAMP } from '../../tests/chat-history-paging/fixtures'

// Independent literal oracle: provider-messages section 6, not the product helper.
const PRIMARY = 'primary-y'
const ANSWERED = 'fallback-x'
const BASE = 'Answered by the Fallback model (fallback-x) because primary-y was unavailable.'
const HINT = "Pick a new model in the agent's settings."
const RETIRED = `${BASE} ${HINT}`
const PAIR = 'primary-y\u0000fallback-x' // D17 ordered pair: unavailable, NUL, answering.
const KNOWN = { answered_model: ANSWERED, unavailable_model: PRIMARY, unavailable_code: 'rate_limited' } as const
// not-wire-format: fixture options derived from the existing generated replay carrier.
type Facts = Pick<ProviderFallbackNote, 'answered_model' | 'unavailable_model' | 'unavailable_code'>
// not-wire-format: paired network test data, never serialized as this wrapper.
type Source = { raw: unknown; carrier: ServerFrame; id: string; position: string; content: string }
type View = 'historical' | 'live'
const VIEWS: View[] = ['historical', 'live']
const initialChat = useChatStore.getState()
const initialSession = useSessionStore.getState()
const initialConnection = useConnectionStore.getState()
let number = 0
let sid: string
let body: unknown
let requests: string[]

function ordinary(rank: number, content: string, role: WireMessage['role'] = 'system'): Source {
  const raw = entry(rank, { role, content, id: `ordinary-${rank}` })
  return { raw, carrier: replay(raw, sid), id: raw.id, position: pos(rank), content }
}

function fallback(rank = 1, facts: Facts = KNOWN, content = BASE, omitRole = false): Source {
  const seed = entry(rank, { id: `fallback-${rank}`, role: 'system', content, turn_id: 'saved-turn' })
  const { role, ...withoutRole } = seed
  // Future canonical raw JSON per Q2. Not cast into the pre-fix generated enum,
  // not aliased to system/message, and never delivered as a fake live frame.
  const raw: unknown = {
    ...(omitRole ? withoutRole : { ...withoutRole, role }), type: 'provider_fallback',
    ...(facts.answered_model === undefined ? {} : { model: facts.answered_model }),
    ...(facts.unavailable_model === undefined ? {} : { unavailable_model: facts.unavailable_model }),
    ...(facts.unavailable_code === undefined ? {} : { unavailable_code: facts.unavailable_code }),
  }
  const carrier: ProviderFallbackNote = {
    type: 'replay_provider_fallback', session_id: sid, entry_id: seed.id,
    timestamp: STAMP, message: content,
    history_origin: { position: pos(rank), entry_id: seed.id }, ...facts,
  }
  return { raw, carrier, id: seed.id, position: pos(rank), content }
}

function packet(sources: Source[]) {
  // These datasets have exactly one public narration row per whole source.
  const metadata = page(sources.map((source, index) => entry(index + 1, { history_position: source.position })), {
    sessionId: sid, info: { projected_bytes: 0 },
  })
  const result = { ...metadata, entries: sources.map(source => source.raw) }
  const { projected_bytes, soft_byte_budget, byte_budget_exceeded, budget_override, ...info } = result.page_info
  void projected_bytes; void soft_byte_budget; void byte_budget_exceeded; void budget_override
  const rest = { ...result, page_info: info }
  const { entries, session_id, ...snapshotMetadata } = rest
  void entries
  // Genuine separately built fallback replay carrier, NOT replay_message(raw).
  const websocket = [{ type: 'session_snapshot', session_id, seq: 100, ...snapshotMetadata }, ...sources.map(source => source.carrier)]
  result.page_info.projected_bytes = Math.max(Buffer.byteLength(JSON.stringify(rest)), Buffer.byteLength(JSON.stringify(websocket)))
  return result
}

async function load(sources: Source[]) {
  body = packet(sources)
  await act(async () => { await useChatStore.getState().loadLatestHistory(sid) })
  const bucket = useChatStore.getState().sessionsById[sid]
  expect(bucket.history.requests.latest, 'setup: real bounded read/merge completed, not a page rejection').toStrictEqual({ status: 'idle', error: null, attempt: 1 })
  expect(bucket.history.historyLoaded, 'setup: accepted history, not a failed HTTP/array adapter').toBe(true)
  expect(bucket.history.readerPositions).toStrictEqual(sources.map(source => source.position))
  return bucket
}

function deliver(...frames: ServerFrame[]) {
  for (const frame of frames) {
    const parsed = parseFrameSafe(frame)
    expect(parsed, 'fixture: genuine generated-valid live/replay carrier').not.toBeNull()
    if (!parsed) throw new Error('Fixture instrument: generated frame validation failed')
    // WsFrame is a wider TS union; prove the genuine ServerFrame round-trips,
    // then hand the SAME typed fixture to the reducer, without a narrowing cast.
    expect(parsed, 'fixture: generated server carrier round-trips without mutation').toStrictEqual(frame)
    act(() => { useChatStore.getState().handleFrame(frame) })
  }
}

function live(facts = KNOWN): ProviderFallbackFrame {
  return { type: 'provider_fallback', session_id: sid, turn_id: 'later-live-turn', ...facts }
}

function HistoricalRows() {
  const bucket = useChatStore(state => state.sessionsById[sid])
  return <>{getMessages(bucket).filter(row => row.role === 'system').map(row => <VirtualSystemMessageRow key={row.id} message={row} />)}</>
}

function LiveRows() {
  const runtime = useOmnipusRuntime()
  return <AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root><ThreadPrimitive.Messages components={{ UserMessage, AssistantMessage, SystemMessage }} /></ThreadPrimitive.Root></AssistantRuntimeProvider>
}

function show(view: View) { return render(view === 'live' ? <LiveRows /> : <HistoricalRows />) }

function observeItem(source: Source) {
  const parsed = MessageSchema.safeParse(source.raw)
  const row = useChatStore.getState().sessionsById[sid].messagesById[source.id]
  // Diagnostic observation only: never assert rejection/placeholder is correct.
  // These lines tie the intended marker failure to type=provider_fallback item
  // rejection while a structurally valid ordinary page passes in this SAME run.
  console.info('PF-A item-to-render evidence', JSON.stringify({
    id: source.id, raw_type: typeof source.raw === 'object' && source.raw ? Reflect.get(source.raw, 'type') : null,
    generated_item_accepted: parsed.success,
    issues: parsed.success ? [] : parsed.error.issues.map(issue => ({ path: issue.path, code: issue.code, message: issue.message })),
    api_item_errors: getApiSchemaErrorCount(), normalized_content: row.content,
    marker: row.providerFallbackNote ?? null,
  }))
}

function expectNote(source: Source, retired = false) {
  const row = useChatStore.getState().sessionsById[sid].messagesById[source.id]
  observeItem(source)
  // Pre-RED oracle correction (team-lead Q2=A): Q4 specifies these values,
  // not the live builder's whole private key set; pair metadata may coexist.
  expect.soft(row.providerFallbackNote, 'Q4: persisted fallback display marker must survive real REST normalization and accepted history merge')
    .toMatchObject({ message: source.content })
  if (retired) {
    expect.soft(row.providerFallbackNote?.pickNewModelHint, 'Q4: recorded model_retired enables the hint').toBe(true)
  } else {
    expect.soft([false, undefined], 'Q4: non-retired hint flag is false or absent, never invented true')
      .toContain(row.providerFallbackNote?.pickNewModelHint)
  }
  expect.soft(row, 'Q2/Q4: original saved identity/content/author/turn/occurrence stay intact').toMatchObject({
    id: source.id, role: 'system', status: 'done', content: source.content, timestamp: STAMP,
    agentId: 'mia', turnId: 'saved-turn', historyPosition: source.position,
  })
  expect.soft(screen.queryAllByTestId('provider-fallback-note'), 'actual shared note renderer, not a generic corruption placeholder').toHaveLength(1)
  expect.soft(getApiSchemaErrorCount(), 'supported canonical fallback must not degrade as a malformed item after GREEN').toBe(0)
}

beforeEach(() => {
  number += 1; sid = `pf-history-${number}`; requests = []; body = undefined
  sessionStorage.clear(); resetApiSchemaErrorCount(); queryClient.clear()
  act(() => {
    useChatStore.setState({ ...initialChat, sessionsById: {}, messages: [], messagesById: {}, toolCalls: {}, toolCallOrder: [], textAtToolCallStart: {} }, true)
    useSessionStore.setState({ ...initialSession, activeSessionId: sid, activeAgentId: 'mia' }, true)
    useConnectionStore.setState({ ...initialConnection, connection: null, isConnected: false }, true)
  })
  vi.stubGlobal('fetch', async (input: RequestInfo | URL) => {
    const url = String(input)
    if (!url.startsWith(`/api/v1/sessions/${sid}/messages`)) throw new Error(`Unregistered network request: ${url}`)
    requests.push(url)
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
  })
})

afterEach(() => {
  cleanup()
  act(() => { for (const id of Object.keys(useChatStore.getState().sessionsById)) useChatStore.getState().releaseSessionHistory(id) })
  queryClient.clear(); vi.unstubAllGlobals(); sessionStorage.clear()
})

describe.each(VIEWS)('PF=A real paged history -> %s system renderer', view => {
  it('CONTROL: a nonempty ordinary strict page is accepted and actually rendered', async () => {
    const source = ordinary(1, 'Ordinary strict-page control')
    const result = await load([source])
    expect(PageSchema.safeParse(body).success, 'control: current generated nonempty page schema sees valid input').toBe(true)
    show(view)
    expect(screen.getByText(source.content, { exact: true })).toBeVisible()
    expect(result.messagesById[source.id]).toMatchObject({ id: source.id, content: source.content, historyPosition: source.position })
    expect(requests).toStrictEqual([`/api/v1/sessions/${sid}/messages?limit=40&min_rows=20`])
    expect(getApiSchemaErrorCount()).toBe(0)
  })

  it('preserves a saved rate-limit fallback marker and real note treatment', async () => {
    const source = fallback()
    await load([source]); show(view)
    expectNote(source)
    expect.soft(screen.queryByText(HINT, { exact: true }), 'rate_limited does not invent a retired-model hint').not.toBeInTheDocument()
  })

  it('keeps saved retired text verbatim and shows the settings hint exactly once', async () => {
    const source = fallback(1, { ...KNOWN, unavailable_code: 'model_retired' }, RETIRED)
    await load([source]); show(view)
    expectNote(source, true)
    const noteText = screen.getByTestId('provider-fallback-note').textContent
    expect(noteText, 'the rendered fallback note has text before counting its hint').not.toBeNull()
    if (noteText === null) throw new TypeError('Rendered fallback note text is missing')
    expect.soft(noteText.split(HINT).length - 1,
      'D12/Q4: saved text already has the single hint; do not append a duplicate').toBe(1)
  })

  it('CONTROL: existing live retired composition is the intended single-hint oracle', async () => {
    await load([])
    deliver({ ...live(), unavailable_code: 'model_retired' }); show(view)
    const note = screen.getByTestId('provider-fallback-note')
    expect(note.textContent).toContain(BASE)
    expect(note.textContent?.split(HINT)).toHaveLength(2)
    expect(getApiSchemaErrorCount()).toBe(0)
  })

  it('pins the assigned single-hint rule on an actual saved retired replay carrier', async () => {
    await load([])
    const source = fallback(1, { ...KNOWN, unavailable_code: 'model_retired' }, RETIRED)
    deliver(source.carrier); show(view)
    expect(useChatStore.getState().sessionsById[sid].messagesById[source.id].content).toBe(RETIRED)
    expect(screen.getByTestId('provider-fallback-note').textContent?.split(HINT),
      'live parity: a saved retired replay must not display two settings hints').toHaveLength(2)
  })
})

it('retains the saved note after its answer rather than replacing it with a corruption placeholder', async () => {
  const answer = ordinary(1, 'The persisted answer.', 'assistant')
  const source = fallback(2)
  const bucket = await load([answer, source]); show('historical')
  expect(bucket.messageOrder).toStrictEqual([answer.id, source.id])
  expectNote(source)
})

const unknownCases: { name: string; facts: Facts; paired: boolean; retired?: boolean }[] = [
  { name: 'both models and code absent, even though prose names them', facts: {}, paired: false },
  { name: 'only answering model recorded', facts: { answered_model: ANSWERED, unavailable_code: 'rate_limited' }, paired: false },
  { name: 'only unavailable model recorded', facts: { unavailable_model: PRIMARY, unavailable_code: 'rate_limited' }, paired: false },
  { name: 'code absent but both recorded models known', facts: { answered_model: ANSWERED, unavailable_model: PRIMARY }, paired: true },
  { name: 'retired code known but models absent', facts: { unavailable_code: 'model_retired' }, paired: false, retired: true },
]
it.each(unknownCases)('missing optional facts remain unknown: $name', async ({ facts, paired, retired }) => {
  const source = fallback(1, facts, retired ? RETIRED : BASE, true)
  await load([source]); show('historical')
  expectNote(source, retired)
  const bucket = useChatStore.getState().sessionsById[sid]
  expect.soft(new Set(bucket.providerFallbackPairsSeen),
    'Q4: ENTIRE pair set contains only both recorded model facts; never any pair invented from prose/config')
    .toStrictEqual(paired ? new Set([PAIR]) : new Set())
  deliver(live())
  expect.soft(useChatStore.getState().sessionsById[sid].messageOrder,
    'unknown pair must not suppress a legitimate subsequent live note; known pair must suppress it')
    .toHaveLength(paired ? 1 : 2)
})

it('seeds the existing per-chat pair guard from REST without suppressing a changed pair', async () => {
  const source = fallback()
  await load([source]); show('historical')
  observeItem(source)
  expect.soft(useChatStore.getState().sessionsById[sid].providerFallbackPairsSeen,
    'Q4: accepted REST ingestion seeds the existing guard, not a second guard').toStrictEqual(new Set([PAIR]))
  // C-8: this announces call2 of3, after a60s honored retry (not a timing oracle).
  deliver({ type: 'provider_retry', session_id: sid, turn_id: 'later-live-turn', provider: 'openrouter', model: PRIMARY,
    retry_at: '2026-10-21T10:01:00Z', retry_after_seconds: 60, sent_at: STAMP, attempt: 2, max_attempts: 3, error_code: 'rate_limited' })
  expect(useChatStore.getState().sessionsById[sid].providerRetryEvent?.turnId).toBe('later-live-turn')
  deliver(live())
  expect.soft(useChatStore.getState().sessionsById[sid].providerRetryEvent, 'pair suppression still clears the live retry slot').toBeNull()
  expect.soft(useChatStore.getState().sessionsById[sid].messageOrder, 'same recorded pair adds no new synthetic note').toStrictEqual([source.id])
  deliver({ ...live(), answered_model: 'fallback-z' })
  expect.soft(useChatStore.getState().sessionsById[sid].messageOrder, 'a genuinely different pair gets one new note').toHaveLength(2)
  expect.soft(screen.queryAllByTestId('provider-fallback-note')).toHaveLength(2)
})

it('CONTROL: the same pair is still announced in a different chat, never guarded globally', async () => {
  await load([]); deliver(live())
  const firstSid = sid
  expect(useChatStore.getState().sessionsById[firstSid].messageOrder).toHaveLength(1)
  sid = `${firstSid}-other-chat`
  act(() => { useSessionStore.setState({ activeSessionId: sid }) })
  await load([]); deliver(live())
  expect(useChatStore.getState().sessionsById[firstSid].messageOrder).toHaveLength(1)
  expect(useChatStore.getState().sessionsById[sid].messageOrder).toHaveLength(1)
  expect(useChatStore.getState().sessionsById[sid].providerFallbackPairsSeen).toStrictEqual(new Set([PAIR]))
})

it('does not erase or duplicate a genuine replay occurrence when the same REST history overlaps', async () => {
  const source = fallback()
  const history = packet([source])
  const { entries, ...metadata } = history
  void entries
  const snapshot: SessionSnapshotFrame = { type: 'session_snapshot', seq: 100, boot_id: 'pf-history-boot', reason: 'unknown_position', ...metadata }
  deliver(snapshot, source.carrier, { type: 'catch_up_complete', session_id: sid, boot_id: 'pf-history-boot', seq: 100, mode: 'snapshot' })
  const replayMarker = useChatStore.getState().sessionsById[sid].messagesById[source.id].providerFallbackNote
  expect(replayMarker, 'control: real generated replay owner created the note before overlapping REST').toMatchObject({ message: BASE })
  expect([false, undefined], 'control: replay non-retired hint is false or absent').toContain(replayMarker?.pickNewModelHint)
  await load([source]); show('historical')
  expectNote(source)
  deliver(source.carrier)
  expect.soft(useChatStore.getState().sessionsById[sid].messageOrder, 'same saved ID/position replay stays one occurrence').toStrictEqual([source.id])
  deliver(live())
  expect.soft(useChatStore.getState().sessionsById[sid].messageOrder, 'same pair after replay/REST overlap stays one note').toStrictEqual([source.id])
})
