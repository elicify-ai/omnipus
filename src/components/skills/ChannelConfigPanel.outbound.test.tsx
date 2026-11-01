/**
 * T-30 / T-30b rendering / S-80..S-83 / FR-026.
 * Real Panel, outbound client, HTTP wrapper, ApiError and generated validation.
 * Only HTTP responses and unrelated metadata/store/animation edges are faked.
 * Expected copy/values are from channels-spec-r8, not production constants.
 */
import React from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError } from '@/lib/api-error'
import { ApiSchemaError, getApiSchemaErrorCount } from '@/lib/api/http'
import {
  ChannelOutboundRead as ReadSchema,
  ChannelOutboundRejection as RejectionSchema,
  ChannelOutboundUpdateRequest as UpdateSchema,
  ChannelOutboundWriteResponse as WriteSchema,
} from '@/lib/api/generated/schemas'

const { addToast, devToast } = vi.hoisted(() => ({ addToast: vi.fn(), devToast: vi.fn() }))
vi.mock('@/store/ui', () => ({ useUiStore: () => ({ addToast }) }))
vi.mock('@/lib/dev-toast', () => ({ maybeDevToast: devToast }))
vi.mock('@/store/connection', () => {
  const state = { isConnected: false, connection: null }
  return { useConnectionStore: Object.assign((selector: (s: typeof state) => unknown) => selector(state), { getState: () => state }) }
})
vi.mock('@/store/whatsappPairing', () => ({
  useWhatsAppPairingStore: (selector: (s: { byChannel: Record<string, unknown>; clear: () => void }) => unknown) =>
    selector({ byChannel: {}, clear: vi.fn() }),
}))
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual, // outbound clients, request and ApiError remain REAL, not stubs.
    fetchChannelConfig: vi.fn().mockResolvedValue({ token: '[configured]' }),
    fetchChannelRouting: vi.fn().mockResolvedValue({ default_agent_id: '', workspace_id: '', bound: false }),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    fetchWorkspace: vi.fn().mockResolvedValue({}),
  }
})
vi.mock('framer-motion', () => ({
  motion: new Proxy({}, { get: (_: object, prop: string) =>
    ({ children, ...props }: Record<string, unknown>) => React.createElement(prop, props, children as React.ReactNode) }),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))
import { ChannelConfigPanel } from './ChannelConfigPanel'

const STALE = 'These settings changed since you opened them. The current values are shown; check them and save again.'
const CONNECT_FIRST = 'Connect the channel first to add Discord ids.'
const DISCORD_HELP = "A person's id sends a direct message; a channel or thread id posts there. Adding a Discord id needs the connection running."
const TELEGRAM_HELP = 'A positive id is a person (they must have started your bot first); a negative id is a group or channel; a forum topic is written chat_id/topic_id.'
const FIELD_HELP = "One id per line, like the allowed senders: a person's user id (a direct message) or a group's chat id. No @names, no *. Up to 20."
const FIELD_LABEL = 'People and groups the agent may message'
const fetchMock = vi.fn<typeof fetch>()
let readBody: ReturnType<typeof ReadSchema.parse>
let readStatus: number
let writeBody: unknown
let writeStatus: number
let reads: number
let writes: ReturnType<typeof UpdateSchema.parse>[]

beforeEach(() => {
  addToast.mockReset()
  devToast.mockReset()
  reads = 0
  writes = []
  readStatus = 200
  readBody = ReadSchema.parse({
    outbound: { policy: 'agent_may_start', targets: ['discord:111', 'discord:222'] },
    revision: 't30-before', availability: { state: 'running' }, start_supported: true,
  })
  writeStatus = 200
  writeBody = WriteSchema.parse({ outbound: { policy: 'agent_may_start', targets: ['discord:333'] }, revision: 't30-after' })
  document.cookie = 'csrf=t30-synthetic; path=/'
  fetchMock.mockReset().mockImplementation(async (input, init) => {
    const url = input instanceof Request ? input.url : String(input)
    expect(url).toMatch(/\/api\/v1\/channels\/[^/]+\/outbound$/)
    expect(init?.credentials).toBe('include')
    if ((init?.method ?? 'GET') === 'GET') {
      reads++
      return new Response(JSON.stringify(readStatus === 200 ? readBody : { error: 'QA outbound settings read unavailable' }), {
        status: readStatus, headers: { 'Content-Type': 'application/json' },
      })
    }
    expect(init?.method).toBe('PUT')
    writes.push(UpdateSchema.parse(JSON.parse(String(init?.body))))
    return new Response(JSON.stringify(writeBody), { status: writeStatus, headers: { 'Content-Type': 'application/json' } })
  })
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  document.cookie = 'csrf=; Max-Age=0; path=/'
})

function renderPanel(platform = 'discord') {
  const id = `${platform}.qa`
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  client.setQueryData(['channel-config', id], { token: '[configured]' })
  client.setQueryData(['channel-routing', id], { default_agent_id: '', workspace_id: '', bound: false })
  client.setQueryData(['agents'], [])
  client.setQueryData(['workspaces', { status: 'active' }], [])
  render(<QueryClientProvider client={client}>
    <ChannelConfigPanel channelId={id} channelName={platform === 'discord' ? 'Discord' : platform === 'telegram' ? 'Telegram' : platform === 'webchat' ? 'Web Chat' : 'Slack'}
      enabled={true} open={true} onOpenChange={vi.fn()} />
  </QueryClientProvider>)
  return client
}

async function loaded() {
  await waitFor(() => expect(screen.queryByText('Starting messages', { exact: true })).not.toBeNull())
}

function targetsField() {
  return screen.getByRole('textbox', { name: FIELD_LABEL })
}

function ownSave() {
  // Do not require a new test-id, file name or DOM tag. Find the smallest
  // rendered area containing the real radio and its own single Save, rather
  // than accidentally clicking the generic Configure form's Save.
  const radio = screen.getByRole('radio', { name: 'Only answer' })
  for (let area = radio.parentElement; area; area = area.parentElement) {
    const saves = within(area).queryAllByRole('button', { name: 'Save' })
    if (saves.length === 1) return saves[0]
  }
  throw new Error('FR-026: Starting messages needs its own Save, separate from Configure')
}

async function submit(lines: string) {
  fireEvent.change(targetsField(), { target: { value: lines } })
  await act(async () => { fireEvent.click(ownSave()) })
  await waitFor(() => expect(writes).toHaveLength(1))
}

function messages() {
  return [document.body.textContent ?? '', ...addToast.mock.calls.map(([toast]) => String(toast.message))].join('\n')
}

function expectIndexedErrors(items: Pick<NonNullable<ReturnType<typeof RejectionSchema.parse>['target_errors']>[number], 'index' | 'reason'>[]) {
  // FormError's actual announcement boundary, independent of punctuation,
  // wrapper tags, error ordering or an abbreviated target hint.
  const alerts = screen.getAllByRole('alert')
  for (const item of items) {
    const reason = item.reason
    expect(typeof reason).toBe('string')
    if (!reason) throw new Error('instrument: this fixture has a distinct reason per indexed error')
    const otherReasons = items.map((other) => other.reason).filter((other): other is string => other !== reason && other !== undefined)
    const lines = new Set(alerts.flatMap((alert) => [alert, ...alert.querySelectorAll('*')])
      .flatMap((element) => (element.textContent ?? '').split('\n'))
      .filter((line) => line.includes(reason) && !otherReasons.some((other) => line.includes(other))))
    // A nested span may repeat the reason without its parent's line label.
    // Verify the specified association, not the number of DOM text copies.
    expect([...lines], `index ${item.index} must announce its own line ${item.index + 1} with its own reason`)
      .toEqual(expect.arrayContaining([expect.stringMatching(new RegExp(`(?:^|\\D)${item.index + 1}(?:\\D|$)`))]))
  }
}

describe('T-30 starting messages section — S-80', () => {
  it('reply_only selects Only answer, hides targets and shows its exact help', async () => {
    readBody = ReadSchema.parse({ ...readBody, outbound: { policy: 'reply_only', targets: [] } })
    renderPanel()
    await loaded()
    expect(screen.getByRole('radio', { name: 'Only answer' })).toBeChecked()
    expect(screen.queryByRole('textbox', { name: FIELD_LABEL })).not.toBeInTheDocument()
    expect(screen.getByText('The agent answers messages that arrive here. It never starts a conversation.')).toBeInTheDocument()
  })

  it('may-start renders two bare ids and the exact Discord and field help', async () => {
    renderPanel()
    await loaded()
    expect(screen.getByRole('radio', { name: 'Answer and also start messages' })).toBeChecked()
    expect(targetsField()).toHaveValue('111\n222')
    expect(screen.getByText(DISCORD_HELP)).toBeInTheDocument()
    expect(screen.getByText(FIELD_HELP)).toBeInTheDocument()
    expect(screen.getByText('The agent may also start messages, but only to the people and groups listed below.')).toBeInTheDocument()
  })

  it('not-running warning preserves remove/revoke access without claiming additions work offline', async () => {
    readBody = ReadSchema.parse({ ...readBody, availability: { state: 'not_running', reason: 'stopped' } })
    renderPanel()
    await loaded()
    expect(screen.getByText('This connection is not running. You can remove ids or switch to Only answer; adding a Discord id needs it running.')).toBeInTheDocument()
    expect(targetsField()).toHaveValue('111\n222')
    expect(ownSave()).toBeEnabled()
  })

  it('Slack shows unsupported text instead of either start-policy radio', async () => {
    readBody = ReadSchema.parse({ ...readBody, start_supported: false, outbound: { policy: 'reply_only', targets: [] } })
    renderPanel('slack')
    await loaded()
    expect(screen.getByText('Starting messages is not supported yet on Slack. The agent only answers here.')).toBeInTheDocument()
    expect(screen.queryByRole('radio', { name: 'Only answer' })).not.toBeInTheDocument()
    expect(screen.queryByRole('radio', { name: 'Answer and also start messages' })).not.toBeInTheDocument()
    expect(screen.queryByRole('textbox', { name: FIELD_LABEL })).not.toBeInTheDocument()
    expect(reads).toBe(0) // Chief Q1 / S-80: unsupported platforms do not GET the setting.
  })

  it('webchat has a real Configure panel but no Starting messages section', async () => {
    renderPanel('webchat')
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Configure Web Chat' })).toBeInTheDocument())
    // A loaded Configure form, not a closed panel or a loading skeleton, is
    // the control for this negation (T-2 nonconnector row / NB-10).
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save', exact: true })).toBeInTheDocument())
    expect(screen.queryByText('Starting messages', { exact: true })).not.toBeInTheDocument()
    expect(screen.queryByRole('radio', { name: 'Only answer' })).not.toBeInTheDocument()
    expect(screen.queryByRole('radio', { name: 'Answer and also start messages' })).not.toBeInTheDocument()
    expect(screen.queryByRole('textbox', { name: FIELD_LABEL })).not.toBeInTheDocument()
    expect(reads).toBe(0)
    expect(writes).toEqual([])
  })

  it('Only answer and back preserves the exact entered list without saving', async () => {
    renderPanel()
    await loaded()
    expect(targetsField()).toHaveValue('111\n222')
    fireEvent.change(targetsField(), { target: { value: '333\n444' } })
    fireEvent.click(screen.getByRole('radio', { name: 'Only answer' }))
    expect(screen.getByRole('radio', { name: 'Only answer' })).toBeChecked()
    expect(screen.queryByRole('textbox', { name: FIELD_LABEL })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('radio', { name: 'Answer and also start messages' }))
    expect(screen.getByRole('radio', { name: 'Answer and also start messages' })).toBeChecked()
    expect(targetsField()).toHaveValue('333\n444') // Spec ambiguity6 / chief Q2, not the original GET list.
    expect(writes).toEqual([])
  })

  it('Telegram shows its exact help after choosing may-start', async () => {
    readBody = ReadSchema.parse({ ...readBody, outbound: { policy: 'reply_only', targets: [] } })
    renderPanel('telegram')
    await loaded()
    fireEvent.click(screen.getByRole('radio', { name: 'Answer and also start messages' }))
    expect(targetsField()).toHaveValue('')
    expect(screen.getByText(TELEGRAM_HELP)).toBeInTheDocument()
  })

  it('own Save sends the whole entered list and exact GET revision to the dedicated route only', async () => {
    writeBody = WriteSchema.parse({ outbound: { policy: 'agent_may_start', targets: ['discord:333', 'discord:444'] }, revision: 't30-after' })
    renderPanel()
    await loaded()
    await submit('333\n444')
    expect(writes).toEqual([{ policy: 'agent_may_start', targets: ['333', '444'], expected_revision: 't30-before' }])
    const put = fetchMock.mock.calls.find(([, init]) => init?.method === 'PUT')
    expect(put?.[0]).toBe('/api/v1/channels/discord.qa/outbound')
    // The HTTP fake refuses every other endpoint, so a generic Configure save
    // cannot make this assertion green. Numeric validity remains the server's job.
  })
})

describe('T-30 write-response contract — FR-090 / required outbound and revision', () => {
  it('a schema-invalid 200 write records a contract violation and hydrates neither form nor cache', async () => {
    // Contract table: revision is required even when outbound itself is valid.
    // A different valid target makes accidental success hydration observable.
    writeBody = { outbound: { policy: 'agent_may_start', targets: ['discord:999'] } }
    expect(WriteSchema.safeParse(writeBody).success).toBe(false)
    const client = renderPanel()
    await loaded()
    const originalRead = ReadSchema.parse(client.getQueryData(['channel-outbound', 'discord.qa']))
    const violationsBefore = getApiSchemaErrorCount()
    await submit('333\n444')
    await waitFor(() => expect(client.getMutationCache().getAll()[0]?.state.status).toBe('error'))
    const mutations = client.getMutationCache().getAll()
    expect(mutations).toHaveLength(1)
    const error = mutations[0].state.error
    expect(error).toBeInstanceOf(ApiSchemaError)
    if (!(error instanceof ApiSchemaError)) throw new Error('instrument: invalid write must retain the real contract-schema error')
    expect(error.endpoint).toBe('PUT /api/v1/channels/discord.qa/outbound')
    expect(error.zodIssues.map((issue) => issue.path)).toEqual([['revision']])
    expect(error.rawBody).toEqual(writeBody)
    expect(getApiSchemaErrorCount()).toBe(violationsBefore + 1)
    expect(devToast).toHaveBeenCalledTimes(1)
    expect(targetsField()).toHaveValue('333\n444')
    expect(screen.getByRole('radio', { name: 'Answer and also start messages' })).toBeChecked()
    expect(client.getQueryData(['channel-outbound', 'discord.qa'])).toEqual(originalRead)
    expect(writes).toEqual([{ policy: 'agent_may_start', targets: ['333', '444'], expected_revision: 't30-before' }])
    expect(addToast.mock.calls.filter(([toast]) => toast.variant === 'success')).toEqual([])
  })
})

describe('T-30 per-code conflict handling — S-81', () => {
  it('stale_revision re-reads GET, replaces values and shows exactly the stale-form guidance', async () => {
    renderPanel()
    await loaded()
    readBody = ReadSchema.parse({ ...readBody, revision: 't30-concurrent', outbound: { policy: 'agent_may_start', targets: ['discord:999'] } })
    writeStatus = 409
    writeBody = RejectionSchema.parse({ code: 'stale_revision', error: 'stale revision' })
    await submit('333')
    await waitFor(() => expect(targetsField()).toHaveValue('999'))
    expect(reads).toBeGreaterThanOrEqual(2)
    expect(messages()).toContain(STALE)
  })

  it('stale_revision failed re-read keeps the form and entered values without claiming current values are shown', async () => {
    const client = renderPanel()
    await loaded()
    readStatus = 503
    writeStatus = 409
    writeBody = RejectionSchema.parse({ code: 'stale_revision', error: 'stale revision' })
    await submit('333\n444')
    // Wait for the async rejection/reread handler to finish, not just for
    // the second GET to start. S-81's current-values claim is conditional
    // on a successful refresh; no fallback wording is specified.
    await waitFor(() => expect(client.getMutationCache().getAll()[0]?.state.status).toBe('error'))
    expect(reads).toBe(2)
    const field = screen.queryByRole('textbox', { name: FIELD_LABEL })
    expect(field, 'F5: a failed stale-revision reread must keep the editable form visible, not replace it with the load-error screen').not.toBeNull()
    expect(field).toHaveValue('333\n444')
    expect(screen.getByRole('radio', { name: 'Answer and also start messages' })).toBeChecked()
    expect(messages()).not.toContain(STALE)
    expect(screen.getAllByRole('alert').length).toBeGreaterThan(0)
  })

  it('connect_first preserves entered values and never presents stale-form guidance', async () => {
    writeStatus = 409
    writeBody = RejectionSchema.parse({ code: 'connect_first', error: 'connect first' })
    renderPanel()
    await loaded()
    await submit('333\n444')
    await waitFor(() => expect(messages()).toContain(CONNECT_FIRST))
    expect(targetsField()).toHaveValue('333\n444')
    expect(messages()).not.toContain(STALE)
  })

  it('saved_live_mismatch preserves values and requires connection reconciliation, not GET alone', async () => {
    writeStatus = 409
    writeBody = RejectionSchema.parse({ code: 'saved_live_mismatch', error: 'saved live mismatch' })
    renderPanel()
    await loaded()
    await submit('333\n444')
    await waitFor(() => expect(messages()).toMatch(/(?:earlier|previous) change/i))
    expect(messages()).toMatch(/(?:did not|not).*finish.*appl|not.*applied/i)
    expect(messages()).toMatch(/successful save|save (?:this |the )?connection|disable.*enable/i)
    expect(messages()).toMatch(/save.*(?:list|again)/i)
    expect(messages()).not.toContain(STALE)
    expect(targetsField()).toHaveValue('333\n444')
  })
})

describe('T-30 indexed errors and T-30b actual section decoder — S-81/S-83', () => {
  it('maps by request index, not duplicate hint or response order, and counts omitted errors', async () => {
    const errors = [
      { index: 7, target: 'same hint', code: 'invalid_id', reason: 'eighth entry is invalid' },
      { index: 1, target: 'same hint', code: 'username_not_allowed', reason: 'second entry is a username' },
    ]
    writeStatus = 400
    writeBody = RejectionSchema.parse({ code: 'invalid_targets', error: 'invalid targets', target_errors: errors, omitted_count: 2 })
    renderPanel()
    await loaded()
    await submit('111\n@alice\n333\n444\n555\n666\n777\nbad')
    await waitFor(() => expect(messages()).toContain('eighth entry is invalid'))
    expectIndexedErrors(errors)
    expect(screen.getByText('+2 more entries were rejected; fix the ones shown, then save again.')).toBeInTheDocument()
    expect(targetsField()).toHaveValue('111\n@alice\n333\n444\n555\n666\n777\nbad')
  })

  it('schema-maximal real HTTP 400 retains all ten indexed reasons and +54 more', async () => {
    const errors = Array.from({ length: 10 }, (_, i) => ({
      index: 54 + i, target: `${'h'.repeat(20)}...`, code: 'unsupported_channel_kind', reason: `reason${i}`.padEnd(120, 'r'),
    }))
    writeStatus = 400
    writeBody = RejectionSchema.parse({ error: 'E'.repeat(500), code: 'invalid_targets', target_errors: errors, omitted_count: 54 })
    const raw = JSON.stringify(writeBody)
    expect(new TextEncoder().encode(raw).byteLength).toBeLessThan(4096)
    const client = renderPanel()
    await loaded()
    await submit(Array.from({ length: 64 }, (_, i) => `raw-${i}`).join('\n'))
    await waitFor(() => expect(messages()).toContain(errors[9].reason))
    expectIndexedErrors(errors)
    expect(screen.getByText('+54 more entries were rejected; fix the ones shown, then save again.')).toBeInTheDocument()
    const error = client.getMutationCache().getAll().map((mutation) => mutation.state.error).find((candidate) => candidate instanceof ApiError)
    expect(error).toBeInstanceOf(ApiError)
    if (!(error instanceof ApiError)) throw new Error('instrument: section Save must retain the real HTTP ApiError')
    expect(error.code).toBe('invalid_targets')
    expect(error.body).toBe(raw)
  })

  it('an unsafe reason fails actual section schema validation, announces a generic error and emits the dev contract toast', async () => {
    writeStatus = 400
    writeBody = { error: 'invalid targets', code: 'invalid_targets', target_errors: [{ index: 0, target: 'safe', code: 'invalid_id', reason: 'UNSAFE-SENTINEL\u007f' }] }
    expect(RejectionSchema.safeParse(writeBody).success).toBe(false)
    renderPanel()
    await loaded()
    await submit('bad')
    await waitFor(() => expect(devToast).toHaveBeenCalledTimes(1))
    const alertText = screen.getAllByRole('alert').map((element) => element.textContent ?? '').join('\n')
    expect(alertText.trim().length).toBeGreaterThan(0)
    expect(alertText).not.toContain('UNSAFE-SENTINEL')
    expect(messages()).not.toContain('UNSAFE-SENTINEL')
    expect(addToast.mock.calls.filter(([toast]) => toast.variant === 'success')).toEqual([])
  })
})
