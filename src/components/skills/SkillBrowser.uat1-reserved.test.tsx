// UAT-1 F-11 (skills spec r15 §5.10): a file install refused with a 409 whose
// code is BUILTIN_NAME_RESERVED shows the server's text, not the generic
// "changed after you reviewed it" message that belongs to a changed installed
// skill. Real SkillBrowser; only the API edges are replaced.

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

import { fetchSkills, fetchSkillMarketplaceStatus, installSkillFromFile } from '@/lib/api'
import { ApiError } from '@/lib/api-error'
import { SkillBrowser } from './SkillBrowser'

const reserved = "'Plan' is the name of a built-in skill and cannot be used for another skill. Choose a different name."

let client: QueryClient

beforeEach(() => {
  vi.clearAllMocks()
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } })
  vi.mocked(fetchSkills).mockResolvedValue([])
  vi.mocked(fetchSkillMarketplaceStatus).mockResolvedValue({ enabled: true, registries: [{ name: 'clawhub', enabled: true }] })
})

afterEach(() => {
  cleanup()
  client.clear()
})

describe('SkillBrowser file install, reserved built-in name', () => {
  it('a 409 BUILTIN_NAME_RESERVED shows the server text, not the changed-after-review text', async () => {
    vi.mocked(installSkillFromFile).mockRejectedValue(
      new ApiError(409, reserved, { code: 'BUILTIN_NAME_RESERVED' }),
    )
    render(<QueryClientProvider client={client}><SkillBrowser open onOpenChange={vi.fn()} /></QueryClientProvider>)
    const user = userEvent.setup()
    await screen.findByText('Install from file', { exact: true })
    const input = document.querySelector('input[type="file"]')
    expect(input).toBeInstanceOf(HTMLInputElement)
    const file = new File([new Uint8Array([0x50, 0x4b])], 'plan.zip', { type: 'application/zip' })
    fireEvent.change(input!, { target: { files: [file] } })
    await user.click(await screen.findByTestId('confirm-install-btn'))

    await waitFor(() => expect(addToast).toHaveBeenCalledTimes(1))
    expect(installSkillFromFile).toHaveBeenCalledTimes(1) // instrument: the refusal came from the install call
    const toast = addToast.mock.calls[0][0] as { message: string; variant: string }
    expect(toast.variant).toBe('error')
    expect(toast.message).not.toContain('changed after you reviewed')
    expect(toast.message).toContain(reserved)
  })
})
