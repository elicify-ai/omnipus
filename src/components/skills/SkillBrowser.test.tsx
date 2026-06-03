/**
 * SkillBrowser.test.tsx — Issue #340 ACs (US-E4, SkillBrowser part).
 *
 * Covers:
 * 1. Installing a file triggers the confirm dialog with unverified notice.
 * 2. Capabilities from SKILL.md frontmatter are shown in the confirm dialog.
 * 3. Cancelling the confirm does not install.
 * 4. Confirming the dialog calls installSkillFromFile.
 * 5. A non-hash install error shows a toast (not silent).
 * 6. A hash-mismatch error shows the hash-mismatch dialog (not a toast).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast }),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    installSkillFromFile: vi.fn(),
  }
})

import { installSkillFromFile } from '@/lib/api'
import { SkillBrowser } from './SkillBrowser'

const SKILL_MD_WITH_CAPS = `---
name: Test Skill
version: 1.0.0
capabilities:
  - Read files from the workspace
  - Execute shell commands
  - Access the web
---
# Test Skill
This is a test skill.
`

const SKILL_MD_NO_CAPS = `---
name: Simple Skill
version: 0.1.0
---
# Simple Skill
`

function renderBrowser(open = true) {
  const onOpenChange = vi.fn()
  render(<SkillBrowser open={open} onOpenChange={onOpenChange} />)
  return { onOpenChange }
}

/** Helper: create a File object from text content. */
function makeFile(name: string, content: string): File {
  return new File([content], name, { type: 'text/markdown' })
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(installSkillFromFile).mockResolvedValue(undefined as never)
})

describe('SkillBrowser — install confirm flow (US-E4, #340)', () => {
  it('shows the install-from-file button', () => {
    renderBrowser()
    expect(screen.getByText(/Install from file/i)).toBeInTheDocument()
  })

  it('shows the confirm dialog with unverified notice after file selection', async () => {
    renderBrowser()
    const file = makeFile('my-skill.md', SKILL_MD_WITH_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => {
      expect(screen.getByTestId('skill-install-confirm-dialog')).toBeInTheDocument()
      expect(screen.getByTestId('unverified-notice')).toBeInTheDocument()
    })
  })

  it('shows declared capabilities from SKILL.md frontmatter', async () => {
    renderBrowser()
    const file = makeFile('my-skill.md', SKILL_MD_WITH_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => {
      expect(screen.getByText('Read files from the workspace')).toBeInTheDocument()
      expect(screen.getByText('Execute shell commands')).toBeInTheDocument()
      expect(screen.getByText('Access the web')).toBeInTheDocument()
    })
  })

  it('shows "no capabilities declared" when frontmatter has none', async () => {
    renderBrowser()
    const file = makeFile('simple.md', SKILL_MD_NO_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => {
      expect(screen.getByText(/No capabilities declared/i)).toBeInTheDocument()
    })
  })

  it('shows file name in the confirm dialog', async () => {
    renderBrowser()
    const file = makeFile('my-skill.md', SKILL_MD_WITH_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => {
      expect(screen.getByText('my-skill.md')).toBeInTheDocument()
    })
  })

  it('cancelling the confirm dialog does not call installSkillFromFile', async () => {
    renderBrowser()
    const file = makeFile('my-skill.md', SKILL_MD_WITH_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => screen.getByTestId('skill-install-confirm-dialog'))
    await userEvent.click(screen.getByText('Cancel'))
    await waitFor(() => {
      expect(vi.mocked(installSkillFromFile)).not.toHaveBeenCalled()
    })
  })

  it('confirming the dialog calls installSkillFromFile', async () => {
    renderBrowser()
    const file = makeFile('my-skill.md', SKILL_MD_WITH_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => screen.getByTestId('skill-install-confirm-dialog'))
    await userEvent.click(screen.getByTestId('confirm-install-btn'))
    await waitFor(() => {
      expect(vi.mocked(installSkillFromFile)).toHaveBeenCalledWith(SKILL_MD_WITH_CAPS, 'my-skill.md')
    })
  })

  it('shows a success toast after successful install', async () => {
    renderBrowser()
    const file = makeFile('my-skill.md', SKILL_MD_WITH_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => screen.getByTestId('skill-install-confirm-dialog'))
    await userEvent.click(screen.getByTestId('confirm-install-btn'))
    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success' })
      )
    })
  })
})

describe('SkillBrowser — error handling (US-E4, #340)', () => {
  it('shows a toast for a non-hash install error (not silent)', async () => {
    vi.mocked(installSkillFromFile).mockRejectedValue(new Error('network timeout'))
    renderBrowser()
    const file = makeFile('my-skill.md', SKILL_MD_WITH_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => screen.getByTestId('skill-install-confirm-dialog'))
    await userEvent.click(screen.getByTestId('confirm-install-btn'))
    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error', message: expect.stringContaining('network timeout') })
      )
    })
  })

  it('shows the hash-mismatch dialog (not a toast) for a hash error', async () => {
    vi.mocked(installSkillFromFile).mockRejectedValue(
      new Error('409: {"expected":"abc123","got":"def456"}')
    )
    renderBrowser()
    const file = makeFile('my-skill.md', SKILL_MD_WITH_CAPS)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await waitFor(() => screen.getByTestId('skill-install-confirm-dialog'))
    await userEvent.click(screen.getByTestId('confirm-install-btn'))
    await waitFor(() => {
      expect(screen.getByTestId('skill-hash-mismatch-dialog')).toBeInTheDocument()
      // toast should NOT have been called for hash errors
      expect(addToast).not.toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error', message: expect.stringContaining('409') })
      )
    })
  })
})
