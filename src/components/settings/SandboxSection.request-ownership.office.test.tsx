// SFH-03 / Office B09; Settings FR-015a (founder Q-SB1 B, 2026-11-28):
// recover only this Save's refused new entry; preserve every other draft,
// newer typed input and replaced row. Saved entries keep their row errors.
// Real editor, Save bar, shell password prompt and mutation; only the HTTP
// API edge is controlled by promise barriers, never the outcome handlers.
import { act, type ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SandboxSection } from './SandboxSection'
import { SettingsShellContext } from './SettingsShellContext'
import { useSavePasswordPrompt } from './useSavePasswordPrompt'
import { ApiError, fetchAppState, fetchSandboxConfig, fetchSandboxStatus, reAuth, updateSandboxConfig } from '@/lib/api'
import type { AppState, SandboxConfigResponse, SandboxStatus } from '@/lib/api'
import { deferred } from '../../../tests/chat-history-paging/fixtures'

vi.mock('@/lib/api', async importOriginal => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchAppState: vi.fn(), fetchSandboxConfig: vi.fn(), fetchSandboxStatus: vi.fn(),
  reAuth: vi.fn(), updateSandboxConfig: vi.fn(),
}))

const SAVED = '/srv/office-request/saved'
const A = '/srv/office-request/path-a'
const B = '/srv/office-request/path-b'
const VALID = '/srv/office-request/accepted'
const REASON_A = `Path ${A} was refused for request A.`
const REASON_B = `Path ${B} was refused for request B.`
const SAVED_ADDRESS = 'saved.office.internal'
const ADDRESS_A = 'request-a.office.internal'
const ADDRESS_B = 'request-b.office.internal'
const VALID_ADDRESS = 'accepted.office.internal'
const CONFIG: SandboxConfigResponse = {
  mode: 'permissive', allowed_paths: [SAVED], ssrf: { allow_internal: [] }, requires_restart: false,
}
const STATUS: SandboxStatus = {
  backend: 'landlock', available: true, kernel_level: true, policy_applied: true,
  seccomp_enabled: true, bind_ports_count: 0,
}
const FAMILIES = [
  { family: 'allowed_paths', kind: 'path', saved: SAVED, refused: A, valid: VALID, replacement: B, label: 'Allowed folders' },
  { family: 'ssrf.allow_internal', kind: 'ssrf', saved: SAVED_ADDRESS, refused: ADDRESS_A, valid: VALID_ADDRESS, replacement: ADDRESS_B, label: 'Internal network addresses' },
] as const

function SaveShell({ children }: { children: ReactNode }) {
  const { requestPassword, dialog } = useSavePasswordPrompt()
  return <SettingsShellContext.Provider value={{
    announce: () => {}, setDraftCount: () => {}, offerRestart: () => {}, refreshPendingRestart: () => {},
    requestPassword, saveBarVisible: false, setSaveBarVisible: () => {},
  }}>{children}{dialog}</SettingsShellContext.Provider>
}

async function mountEditor() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const view = render(<QueryClientProvider client={client}><SaveShell><SandboxSection /></SaveShell></QueryClientProvider>)
  await screen.findByText(SAVED)
  // FR-026 moves this read to the real shell prompt, not the bare section.
  await waitFor(() => expect(vi.mocked(fetchAppState).mock.results).toHaveLength(1))
  return { close: () => { view.unmount(); client.clear() } }
}

async function stage(path: string) {
  const input = await screen.findByRole('textbox', { name: 'New allowed path' })
  fireEvent.change(input, { target: { value: path } })
  fireEvent.click(screen.getByRole('button', { name: 'Add path' }))
  expect(screen.getByRole('button', { name: `Delete path ${path}` })).toBeVisible()
  expect(screen.queryByRole('dialog', { name: 'Confirm your password' }), 'FR-010: Add is a draft, not a Save').toBeNull()
  return { input }
}

async function stageEntry(kind: 'path' | 'ssrf', value: string) {
  if (kind === 'path') return stage(value)
  const input = screen.getByRole('textbox', { name: 'New SSRF allow entry' })
  fireEvent.change(input, { target: { value } })
  fireEvent.click(screen.getByRole('button', { name: 'Add SSRF entry' }))
  expect(screen.getByRole('button', { name: `Delete SSRF entry ${value}` })).toBeVisible()
  expect(screen.queryByRole('dialog', { name: 'Confirm your password' }), 'FR-010: Add is a draft, not a Save').toBeNull()
  return { input }
}

async function openSave(labels = ['Allowed folders']) {
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  const dialog = await screen.findByRole('dialog', { name: 'Confirm your password' })
  expect(within(dialog).getByTestId('reauth-items').textContent).toBe(labels.join(''))
  return dialog
}

async function confirm(dialog: HTMLElement) {
  // Synthetic consent only; this fixture has no real account or password.
  fireEvent.change(within(dialog).getByLabelText('Password'), { target: { value: 'office-test-consent' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }))
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Confirm your password' })).toBeNull())
}

function configuration() {
  return screen.getByText('Sandbox configuration', { exact: true }).parentElement!
}

function listRow(family: 'path' | 'ssrf', value: string) {
  const name = family === 'path' ? `Delete path ${value}` : `Delete SSRF entry ${value}`
  const row = screen.getByRole('button', { name }).parentElement?.parentElement
  expect(row, `row containing ${name} and its own message`).toBeInstanceOf(HTMLElement)
  return row as HTMLElement
}

function addArea(kind: 'path' | 'ssrf') {
  const input = screen.getByRole('textbox', { name: kind === 'path' ? 'New allowed path' : 'New SSRF allow entry' })
  const area = input.parentElement?.parentElement
  expect(area, 'scope includes only this Add input, its button and its own error').toBeInstanceOf(HTMLElement)
  return area as HTMLElement
}

function listState() {
  return {
    paths: screen.queryAllByRole('button', { name: /^Delete path / }).map(button => button.getAttribute('aria-label')!.slice('Delete path '.length)),
    ssrf: screen.queryAllByRole('button', { name: /^Delete SSRF entry / }).map(button => button.getAttribute('aria-label')!.slice('Delete SSRF entry '.length)),
    pathInput: (screen.getByRole('textbox', { name: 'New allowed path' }) as HTMLInputElement).value,
    ssrfInput: (screen.queryByRole('textbox', { name: 'New SSRF allow entry' }) as HTMLInputElement | null)?.value ?? null,
  }
}

function expectInputReason(kind: 'path' | 'ssrf', reason: string) {
  // FR-015a fixes the literal server reason and its scope, not whether the
  // wire address prefix is repeated. Either way, no reason text may be lost.
  expect(within(addArea(kind)).getByText(reason, { exact: false }), 'FR-015a: server reason is under this recovered Add input').toBeVisible()
}

function expectConfigurationReason(kind: 'path' | 'ssrf', rowValue: string, reason: string, response: string) {
  expect(within(listRow(kind, rowValue)).queryByText(reason, { exact: false }), 'FR-015a: a guarded refusal must not label the current row').toBeNull()
  expect(within(addArea('path')).queryByText(reason, { exact: false }), 'FR-015a: newer path input must not inherit the older refusal').toBeNull()
  if (screen.queryByRole('textbox', { name: 'New SSRF allow entry' })) {
    expect(within(addArea('ssrf')).queryByText(reason, { exact: false }), 'FR-015a: newer address input must not inherit the older refusal').toBeNull()
  }
  const messages = within(configuration()).getAllByText(response)
  expect(messages.length, 'full server response must remain visible under the whole configuration').toBeGreaterThan(0)
  for (const message of messages) {
    expect(message).toBeVisible()
    expect(message.closest('[data-settings-row]'), 'guarded refusal is not attached to an editable setting').toBeNull()
  }
}

async function refuse(request: ReturnType<typeof deferred<SandboxConfigResponse>>, reason: string, row = 1, family = 'allowed_paths') {
  const response = `${family}[${row}]: ${reason}`
  await act(async () => { request.reject(new ApiError(400, response)) })
  // FR-020: receipt of this exact 400 is never a success. Recovery can leave
  // zero staged fields, so a one-draft headline is not required in that case.
  await waitFor(() => expect(within(configuration()).getAllByText(response)[0]).toBeVisible())
  expect(screen.queryByText(/^Saved at /)).toBeNull()
  return response
}

beforeEach(() => {
  vi.resetAllMocks()
  localStorage.clear()
  sessionStorage.clear()
  vi.mocked(fetchAppState).mockResolvedValue({
    onboarding_complete: true, identity: { mode: 'local', edition: 'core', signed_in: true },
  } as AppState)
  vi.mocked(fetchSandboxStatus).mockResolvedValue(STATUS)
  vi.mocked(fetchSandboxConfig).mockResolvedValue(CONFIG)
  vi.mocked(reAuth)
    .mockResolvedValueOnce({ verified: true, token: 'office-consent-A', expires_in: 300 })
    .mockResolvedValueOnce({ verified: true, token: 'office-consent-B', expires_in: 300 })
})

describe('Office Settings refusal request ownership (SFH-03)', () => {
  it('FR-015a SFH-03: recovers only A without disturbing newer staged B, then recovers B from its own Save', async () => {
    const first = deferred<SandboxConfigResponse>()
    const second = deferred<SandboxConfigResponse>()
    vi.mocked(updateSandboxConfig).mockImplementationOnce(() => first.promise).mockImplementationOnce(() => second.promise)
    const editor = await mountEditor()
    try {
      await stage(A)
      expect(updateSandboxConfig).not.toHaveBeenCalled()
      await confirm(await openSave())
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(1))
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([{ allowed_paths: [SAVED, A] }, 'office-consent-A'])
      const pendingB = await stage(B)
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
      expect(pendingB.input).toHaveValue('')
      const draftPaths = () => screen.getAllByRole('button', { name: /^Delete path /, hidden: true }).map(button => button.getAttribute('aria-label'))
      expect(draftPaths(), 'instrument: B joined the newer staged list after A was sent').toEqual([
        `Delete path ${SAVED}`, `Delete path ${A}`, `Delete path ${B}`,
      ])
      await refuse(first, REASON_A)
      expect.soft(pendingB.input, 'FR-015a: only the unchanged refused A is recovered; newer B stays staged').toHaveValue(A)
      expect(draftPaths(), 'FR-015a: recover A without rolling back or relabelling the newer B').toEqual([
        `Delete path ${SAVED}`, `Delete path ${B}`,
      ])
      expect(listState()).toEqual({ paths: [SAVED, B], ssrf: [], pathInput: A, ssrfInput: null })
      expectInputReason('path', REASON_A)
      expect(within(listRow('path', B)).queryByText(REASON_A, { exact: false }), 'A refusal must not relabel B').toBeNull()
      expect(screen.getByRole('button', { name: 'Save changes' }), 'B remains explicitly saveable, not auto-saved').not.toHaveAttribute('aria-disabled', 'true')
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
      // The person clears recovered A before the next Save. No row is manually
      // deleted, and B's later refusal must never recover A instead of B.
      fireEvent.change(pendingB.input, { target: { value: '' } })
      await confirm(await openSave())
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(2))
      const [bodyB, tokenB] = vi.mocked(updateSandboxConfig).mock.calls[1]
      expect(bodyB).toEqual({ allowed_paths: [SAVED, B] })
      expect(tokenB).toBe('office-consent-B')
      const rowB = bodyB.allowed_paths?.indexOf(B) ?? -1
      expect(rowB, 'independent fixture order: saved row, then B; refused A is absent').toBe(1)
      await refuse(second, REASON_B, 1)
      expect(pendingB.input, 'SFH-03: B keeps its own recovery metadata after A completed').toHaveValue(B)
      expectInputReason('path', REASON_B)
      expect(screen.queryByRole('button', { name: `Delete path ${A}` })).toBeNull()
      expect(screen.queryByRole('button', { name: `Delete path ${B}` })).toBeNull()
      expect(screen.getByText(SAVED)).toBeVisible()
      expect(listState()).toEqual({ paths: [SAVED], ssrf: [], pathInput: B, ssrfInput: null })
      expect(reAuth).toHaveBeenCalledTimes(2)
    } finally { editor.close() }
  })

  it('FR-015a SFH-03: newer typed B prevents recovery of A and puts the refusal under the configuration', async () => {
    const first = deferred<SandboxConfigResponse>()
    vi.mocked(updateSandboxConfig).mockImplementationOnce(() => first.promise)
    const editor = await mountEditor()
    try {
      const pendingA = await stage(A)
      await confirm(await openSave())
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(1))
      fireEvent.change(pendingA.input, { target: { value: B } })
      expect(pendingA.input).toHaveValue(B)
      const response = await refuse(first, REASON_A)
      expect(pendingA.input, 'SFH-03: refusal of A cannot discard newer unsubmitted input B').toHaveValue(B)
      expect(listState()).toEqual({ paths: [SAVED, A], ssrf: [], pathInput: B, ssrfInput: null })
      expectConfigurationReason('path', A, REASON_A, response)
      expect(screen.getByText('1 change could not be saved')).toBeVisible()
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([{ allowed_paths: [SAVED, A] }, 'office-consent-A'])
    } finally { editor.close() }
  })

  it('FR-015a B09 control: after recovery the next explicit Save contains only the saved path and accepted addition', async () => {
    const first = deferred<SandboxConfigResponse>()
    const saved: SandboxConfigResponse = { ...CONFIG, allowed_paths: [SAVED, VALID] }
    vi.mocked(updateSandboxConfig).mockImplementationOnce(() => first.promise).mockResolvedValueOnce(saved)
    const editor = await mountEditor()
    try {
      await stage(A)
      await confirm(await openSave())
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(1))
      await refuse(first, REASON_A)
      expect(screen.getByRole('textbox', { name: 'New allowed path' })).toHaveValue(A)
      expectInputReason('path', REASON_A)
      expect(screen.queryByRole('button', { name: `Delete path ${A}` }), 'FR-015a: no manual row-deletion workaround').toBeNull()
      vi.mocked(fetchSandboxConfig).mockResolvedValue(saved)
      await stage(VALID)
      expect(updateSandboxConfig, 'B09: staging a corrected draft is not another Save').toHaveBeenCalledTimes(1)
      await confirm(await openSave())
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(2))
      expect(vi.mocked(updateSandboxConfig).mock.calls[1]).toEqual([{ allowed_paths: [SAVED, VALID] }, 'office-consent-B'])
      await screen.findByText(VALID)
      expect(screen.queryByRole('button', { name: `Delete path ${A}` })).toBeNull()
      expect(screen.getByText(SAVED)).toBeVisible()
      expect(await screen.findByText(/^Saved at /)).toBeVisible()
    } finally { editor.close() }
  })

  it.each(FAMILIES)('FR-015a $family: recovers a refused new entry but keeps the second valid draft from that same Save', async ({ family, kind, saved, refused, valid, label }) => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({ ...CONFIG, ssrf: { allow_internal: [SAVED_ADDRESS] } })
    const first = deferred<SandboxConfigResponse>()
    const accepted: SandboxConfigResponse = {
      ...CONFIG,
      allowed_paths: kind === 'path' ? [SAVED, VALID] : [SAVED],
      ssrf: { allow_internal: kind === 'ssrf' ? [SAVED_ADDRESS, VALID_ADDRESS] : [SAVED_ADDRESS] },
    }
    vi.mocked(updateSandboxConfig).mockImplementationOnce(() => first.promise).mockImplementationOnce(async () => {
      vi.mocked(fetchSandboxConfig).mockResolvedValue(accepted)
      return accepted
    })
    const editor = await mountEditor()
    try {
      const staged = await stageEntry(kind, refused)
      await stageEntry(kind, valid)
      expect(updateSandboxConfig).not.toHaveBeenCalled()
      await confirm(await openSave([label]))
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(1))
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([
        kind === 'path' ? { allowed_paths: [SAVED, A, VALID] } : { ssrf: { allow_internal: [SAVED_ADDRESS, ADDRESS_A, VALID_ADDRESS] } },
        'office-consent-A',
      ])
      const reason = `This Save refused ${refused}; the other staged entry remains valid.`
      await refuse(first, reason, 1, family)
      expect(staged.input, 'FR-015a: recover exactly the new entry refused by this Save').toHaveValue(refused)
      expect(listState(), 'FR-015a: only the refused entry leaves; the valid same-Save draft and saved entries survive').toEqual({
        paths: kind === 'path' ? [SAVED, VALID] : [SAVED],
        ssrf: kind === 'ssrf' ? [SAVED_ADDRESS, VALID_ADDRESS] : [SAVED_ADDRESS],
        pathInput: kind === 'path' ? A : '', ssrfInput: kind === 'ssrf' ? ADDRESS_A : '',
      })
      expectInputReason(kind, reason)
      expect(within(listRow(kind, saved)).queryByText(reason, { exact: false })).toBeNull()
      expect(within(listRow(kind, valid)).queryByText(reason, { exact: false }), 'the surviving draft must not inherit the refused entry message').toBeNull()
      expect(screen.getByText('1 change could not be saved')).toBeVisible()
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
      // Do not clear/re-add recovered input. FR-011/015a keep it out of the
      // next request while the surviving valid draft is explicitly saved.
      await confirm(await openSave([label]))
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(2))
      expect(vi.mocked(updateSandboxConfig).mock.calls[1]).toEqual([
        kind === 'path' ? { allowed_paths: [SAVED, VALID] } : { ssrf: { allow_internal: [SAVED_ADDRESS, VALID_ADDRESS] } },
        'office-consent-B',
      ])
      expect(staged.input).toHaveValue(refused)
      expect(await screen.findByText(/^Saved at /)).toBeVisible()
      expect(reAuth).toHaveBeenCalledTimes(2)
    } finally { editor.close() }
  })

  it.each(FAMILIES)('FR-015a $family: an already-saved refused entry stays in its row with its reason', async ({ family, kind, refused, valid, label }) => {
    // The refused entry is in the initial server response, not merely a new
    // draft occupying a low index. Another addition makes this field dirty.
    vi.mocked(fetchSandboxConfig).mockResolvedValue({
      ...CONFIG, allowed_paths: kind === 'path' ? [SAVED, A] : [SAVED],
      ssrf: { allow_internal: kind === 'ssrf' ? [SAVED_ADDRESS, ADDRESS_A] : [SAVED_ADDRESS] },
    })
    const request = deferred<SandboxConfigResponse>()
    vi.mocked(updateSandboxConfig).mockImplementationOnce(() => request.promise)
    const editor = await mountEditor()
    try {
      expect(listRow(kind, refused)).toBeVisible()
      await stageEntry(kind, valid)
      expect(updateSandboxConfig).not.toHaveBeenCalled()
      await confirm(await openSave([label]))
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(1))
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([
        kind === 'path' ? { allowed_paths: [SAVED, A, VALID] } : { ssrf: { allow_internal: [SAVED_ADDRESS, ADDRESS_A, VALID_ADDRESS] } },
        'office-consent-A',
      ])
      const expected = {
        paths: kind === 'path' ? [SAVED, A, VALID] : [SAVED],
        ssrf: kind === 'ssrf' ? [SAVED_ADDRESS, ADDRESS_A, VALID_ADDRESS] : [SAVED_ADDRESS],
        pathInput: '', ssrfInput: '',
      }
      expect(listState(), 'instrument: saved and new entries really differ in their origin').toEqual(expected)
      const reason = `The already-saved ${refused} is refused in this Save.`
      await refuse(request, reason, 1, family)
      expect(listState(), 'FR-015a: a saved refusal never moves a row back into Add').toEqual(expected)
      expect(within(listRow(kind, refused)).getByText(reason)).toBeVisible()
      expect(within(listRow(kind, valid)).queryByText(reason, { exact: false })).toBeNull()
      expect(within(addArea(kind)).queryByText(reason, { exact: false })).toBeNull()
      expect(screen.getByText('1 change could not be saved')).toBeVisible()
      expect(updateSandboxConfig).toHaveBeenCalledTimes(1)
      expect(reAuth).toHaveBeenCalledTimes(1)
    } finally { editor.close() }
  })

  it.each(FAMILIES.flatMap(fixture => [
    { ...fixture, guard: 'newer input' as const },
    { ...fixture, guard: 'replaced row' as const },
  ]))('FR-015a $family: $guard preserves both exact lists and inputs and shows only a configuration refusal', async ({ family, kind, refused, replacement, label, guard }) => {
    vi.mocked(fetchSandboxConfig).mockResolvedValue({ ...CONFIG, ssrf: { allow_internal: [SAVED_ADDRESS] } })
    const request = deferred<SandboxConfigResponse>()
    vi.mocked(updateSandboxConfig).mockImplementationOnce(() => request.promise)
    const editor = await mountEditor()
    try {
      await stageEntry(kind, refused)
      expect(updateSandboxConfig).not.toHaveBeenCalled()
      await confirm(await openSave([label]))
      await waitFor(() => expect(updateSandboxConfig).toHaveBeenCalledTimes(1))
      expect(vi.mocked(updateSandboxConfig).mock.calls[0]).toEqual([
        kind === 'path' ? { allowed_paths: [SAVED, A] } : { ssrf: { allow_internal: [SAVED_ADDRESS, ADDRESS_A] } },
        'office-consent-A',
      ])
      if (guard === 'replaced row') {
        fireEvent.click(screen.getByRole('button', { name: kind === 'path' ? `Delete path ${refused}` : `Delete SSRF entry ${refused}` }))
        await stageEntry(kind, replacement)
      }
      // Keep an unrelated newer list draft and its typed Add input too.
      await stageEntry(kind === 'path' ? 'ssrf' : 'path', kind === 'path' ? VALID_ADDRESS : VALID)
      const typedPath = '/srv/office-request/unadded'
      const typedAddress = 'unadded.office.internal'
      const pathInput = guard === 'replaced row' && kind === 'path' ? '' : typedPath
      const ssrfInput = guard === 'replaced row' && kind === 'ssrf' ? '' : typedAddress
      fireEvent.change(screen.getByRole('textbox', { name: 'New allowed path' }), { target: { value: pathInput } })
      fireEvent.change(screen.getByRole('textbox', { name: 'New SSRF allow entry' }), { target: { value: ssrfInput } })
      const current = guard === 'replaced row' ? replacement : refused
      const expected = {
        paths: kind === 'path' ? [SAVED, current] : [SAVED, VALID],
        ssrf: kind === 'ssrf' ? [SAVED_ADDRESS, current] : [SAVED_ADDRESS, VALID_ADDRESS],
        pathInput, ssrfInput,
      }
      expect(listState(), 'instrument: the guard state exists before the older request rejects').toEqual(expected)
      const reason = `Request A refused ${refused}; this does not describe newer data.`
      const response = await refuse(request, reason, 1, family)
      expect(listState(), 'FR-015a: neither list/order nor either input changes on a guarded refusal').toEqual(expected)
      expectConfigurationReason(kind, current, reason, response)
      expect(updateSandboxConfig, 'no automatic resend on the late refusal or newer edits').toHaveBeenCalledTimes(1)
      expect(reAuth).toHaveBeenCalledTimes(1)
    } finally { editor.close() }
  })
})
