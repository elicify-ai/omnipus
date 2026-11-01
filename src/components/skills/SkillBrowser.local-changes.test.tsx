// r15 §5.6/§13, FR-052/070, S-9.3. Real browser component and slug install
// HTTP adapter; only fetch and the other API edges are replaced. File upload
// is a separate API edge here. These tests do not claim server history proof.
// C-1 is present (2038b3bff): list fixtures carry the required generated
// has_local_changes field; no parallel wire type or generated-type edits.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const { addToast } = vi.hoisted(() => ({ addToast: vi.fn() }))
vi.mock('@/store/ui', () => ({ useUiStore: () => ({ addToast }) }))
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSkills: vi.fn(),
    fetchSkillMarketplaceStatus: vi.fn(),
    searchSkills: vi.fn(),
    installSkillFromFile: vi.fn(),
  }
})

import { fetchSkills, fetchSkillMarketplaceStatus, searchSkills, installSkillFromFile } from '@/lib/api'
import { SkillBrowser } from './SkillBrowser'

const revision = 'a'.repeat(64)
const confirmationCode = 'LOCAL_CHANGES_CONFIRMATION_REQUIRED'
const question = (slug: string) => `'${slug}' has local changes. Reinstalling replaces them; they stay in the version history.`
const refusal = (slug: string) => `'${slug}' has local changes that this install would replace. They stay in the version history. Confirm with replace_local_changes to continue.`

function installed(slug: string, changed: boolean) {
  return {
    id: slug, name: slug, source: 'global' as const, version: '1.0.0',
    status: 'active' as const, verified: false, revision, has_local_changes: changed,
  }
}

function response(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

let client: QueryClient
let network: ReturnType<typeof vi.fn<typeof fetch>>

beforeEach(() => {
  vi.clearAllMocks()
  document.cookie = 'csrf=skills-round4; path=/'
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } })
  vi.mocked(fetchSkills).mockResolvedValue([])
  vi.mocked(fetchSkillMarketplaceStatus).mockResolvedValue({ enabled: true, registries: [{ name: 'clawhub', enabled: true }] })
  vi.mocked(installSkillFromFile).mockResolvedValue(installed('web-search', false))
  network = vi.fn<typeof fetch>().mockResolvedValue(response({
    ...installed('web-search', false), revision: 'b'.repeat(64),
    persistence_status: 'complete', activation_status: 'active', changed_fields: ['skill'],
  }))
  vi.stubGlobal('fetch', network)
})

afterEach(() => {
  cleanup()
  client.clear()
  vi.unstubAllGlobals()
  document.cookie = 'csrf=; Max-Age=0; path=/'
})

function renderBrowser() {
  render(<QueryClientProvider client={client}><SkillBrowser open onOpenChange={vi.fn()} /></QueryClientProvider>)
}

async function chooseSlug(slug = 'web-search', changed = true) {
  vi.mocked(fetchSkills).mockResolvedValue([installed(slug, changed)])
  vi.mocked(searchSkills).mockResolvedValue([{ slug, display_name: slug, version: '2.0.0' }])
  renderBrowser()
  const user = userEvent.setup()
  await user.type(await screen.findByTestId('skill-search-input'), slug)
  await user.click(await screen.findByTestId(`skill-install-${slug}`))
  return user
}

async function confirmation(slug: string) {
  // §13 fixes the question and buttons, not a dialog title or ARIA role.
  expect(await screen.findByText(question(slug), { exact: true })).toBeVisible()
  expect(screen.getByRole('button', { name: 'Replace' })).toBeEnabled()
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled()
  return screen
}

function requestBody(index: number) {
  const [url, init] = network.mock.calls[index]
  expect(String(url)).toBe('/api/v1/skills/install')
  expect(init?.method).toBe('POST')
  return JSON.parse(String(init?.body))
}

describe('SkillBrowser — local changes confirmation, r15 S-9.3', () => {
  it('known local changes: Cancel closes the exact question without any install request', async () => {
    const user = await chooseSlug('web-search')
    const dialog = await confirmation('web-search')
    expect(network).not.toHaveBeenCalled()
    await user.click(dialog.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByText(question('web-search'), { exact: true })).not.toBeInTheDocument())
    expect(network).not.toHaveBeenCalled()
    expect(installSkillFromFile).not.toHaveBeenCalled()
    expect(screen.getByTestId('skill-install-web-search')).toBeEnabled()
  })

  it('known local changes: Replace submits once with the reviewed revision and explicit consent', async () => {
    const user = await chooseSlug('pdf-reader')
    const dialog = await confirmation('pdf-reader')
    expect(network).not.toHaveBeenCalled()
    await user.click(dialog.getByRole('button', { name: 'Replace' }))
    await waitFor(() => expect(network).toHaveBeenCalledTimes(1))
    expect(requestBody(0)).toEqual({ slug: 'pdf-reader', version: '2.0.0', revision, replace_local_changes: true })
    expect(await screen.findByText('Installed', { exact: true })).toBeInTheDocument()
    expect(screen.queryByText(question('pdf-reader'), { exact: true })).not.toBeInTheDocument()
    expect(addToast).toHaveBeenCalledExactlyOnceWith({ message: 'Skill "pdf-reader" installed successfully.', variant: 'success' })
  })

  it.each(['Cancel', 'Replace'] as const)('a real LOCAL_CHANGES_CONFIRMATION_REQUIRED 409 waits for %s before retrying', async (action) => {
    network.mockResolvedValueOnce(response({ code: confirmationCode, error: refusal('web-search') }, 409))
    const user = await chooseSlug('web-search', false) // read looked clean; the server found an outside edit.
    const dialog = await confirmation('web-search')
    expect(network).toHaveBeenCalledTimes(1)
    expect(requestBody(0)).toEqual({ slug: 'web-search', version: '2.0.0', revision })
    expect(addToast).not.toHaveBeenCalled()
    await user.click(dialog.getByRole('button', { name: action }))
    await waitFor(() => expect(screen.queryByText(question('web-search'), { exact: true })).not.toBeInTheDocument())
    if (action === 'Cancel') {
      expect(network).toHaveBeenCalledTimes(1)
      expect(screen.getByTestId('skill-install-web-search')).toBeEnabled()
      expect(addToast).not.toHaveBeenCalled()
    } else {
      await waitFor(() => expect(network).toHaveBeenCalledTimes(2))
      expect(requestBody(1)).toEqual({ slug: 'web-search', version: '2.0.0', revision, replace_local_changes: true })
      expect(await screen.findByText('Installed', { exact: true })).toBeInTheDocument()
      expect(addToast).toHaveBeenCalledExactlyOnceWith({ message: 'Skill "web-search" installed successfully.', variant: 'success' })
    }
  })

  it('a different coded 409 is an error, not permission to offer replacement', async () => {
    const message = "'plan' is the name of a built-in skill and cannot be used for another skill. Choose a different name."
    network.mockResolvedValueOnce(response({ code: 'BUILTIN_NAME_RESERVED', error: message }, 409))
    await chooseSlug('plan', false)
    await waitFor(() => expect(addToast).toHaveBeenCalledTimes(1))
    expect(network).toHaveBeenCalledTimes(1)
    expect(requestBody(0)).toEqual({ slug: 'plan', version: '2.0.0', revision })
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(screen.getByTestId('skill-install-plan')).toBeEnabled()
    // §5.10: the coded refusal must keep its exact remedy, not a generic 409.
    expect(addToast).toHaveBeenCalledExactlyOnceWith({
      message: `Failed to install "plan": ${message}`, variant: 'error',
    })
  })

  it.each(['Cancel', 'Replace'] as const)('file reinstall: local-change question requires %s after package review', async (action) => {
    vi.mocked(fetchSkills).mockResolvedValue([installed('web-search', true)])
    renderBrowser()
    const user = userEvent.setup()
    await screen.findByText('Install from file', { exact: true })
    const input = document.querySelector('input[type="file"]')
    expect(input).toBeInstanceOf(HTMLInputElement)
    const file = new File([new Uint8Array([0x50, 0x4b])], 'web-search.zip', { type: 'application/zip' })
    fireEvent.change(input!, { target: { files: [file] } })
    await user.click(await screen.findByTestId('confirm-install-btn'))
    const dialog = await confirmation('web-search')
    expect(installSkillFromFile).not.toHaveBeenCalled()
    expect(network).not.toHaveBeenCalled()
    await user.click(dialog.getByRole('button', { name: action }))
    await waitFor(() => expect(screen.queryByText(question('web-search'), { exact: true })).not.toBeInTheDocument())
    if (action === 'Cancel') {
      expect(installSkillFromFile).not.toHaveBeenCalled()
      expect(screen.getByTestId('skill-install-confirm-dialog')).toBeInTheDocument()
    } else {
      await waitFor(() => expect(installSkillFromFile).toHaveBeenCalledExactlyOnceWith(
        file, expect.any(String), revision, expect.any(AbortSignal), true,
      ))
      await waitFor(() => expect(screen.queryByTestId('skill-install-confirm-dialog')).not.toBeInTheDocument())
      expect(addToast).toHaveBeenCalledExactlyOnceWith({ message: 'Skill "web-search.zip" installed successfully.', variant: 'success' })
    }
  })
})
