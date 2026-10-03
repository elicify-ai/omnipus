/**
 * MailAttachmentsPane.test.tsx — the attachment action row set (w4 spec
 * US-1/US-2): over-cap Open/Save unavailable with the visible cap
 * explanation, accessible action names, the "Save result unknown" state and
 * its explicit SAME-token retry, and Download always available.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

const saveMock = vi.fn()
vi.mock('@/lib/api/mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api/mail')>()
  return { ...actual, saveMailAttachmentToLibrary: (...args: unknown[]) => saveMock(...args) }
})

import { MailAttachmentsPane } from './MailAttachmentsPane'
import type { MailMessage } from '@/lib/api/generated/openapi-types'

type Att = MailMessage['attachments'][number]

function att(overrides?: Partial<Att>): Att {
  return { content_type: 'application/pdf', filename: 'report.pdf', part_index: 0, size_bytes: 2048, ...overrides }
}

function renderPane(overrides?: Partial<Parameters<typeof MailAttachmentsPane>[0]>) {
  const onOpen = vi.fn()
  const onDownload = vi.fn()
  const utils = render(
    <MailAttachmentsPane
      workspaceId="ws_1"
      agentId="mia"
      folder="inbox"
      messageRef="uid:9:9"
      attachments={[att()]}
      onOpen={onOpen}
      onDownload={onDownload}
      {...overrides}
    />,
  )
  return { ...utils, onOpen, onDownload }
}

beforeEach(() => {
  saveMock.mockReset()
})

describe('MailAttachmentsPane', () => {
  it('names every action for its attachment', () => {
    renderPane()
    expect(screen.getByRole('button', { name: 'Open report.pdf' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Save to Library report.pdf' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Download report.pdf' })).toBeTruthy()
  })

  it('keeps Download available while over-cap Open/Save are disabled with a visible explanation', () => {
    renderPane({ attachments: [att({ size_bytes: 30 * 1024 * 1024 })] })
    const open = screen.getByRole('button', { name: 'Open report.pdf' }) as HTMLButtonElement
    const save = screen.getByRole('button', { name: 'Save to Library report.pdf' }) as HTMLButtonElement
    const dl = screen.getByRole('button', { name: 'Download report.pdf' }) as HTMLButtonElement
    expect(open.disabled).toBe(true)
    expect(save.disabled).toBe(true)
    expect(dl.disabled).toBe(false)
    // The explanation is rendered text (readable without hover), not a
    // tooltip-only hint.
    expect(screen.getByText(/Over the 25 MB preview cap/)).toBeTruthy()
    expect(dl.textContent).toContain('Download')
  })

  it('a failed save shows the specific reason and keeps the row', async () => {
    saveMock.mockRejectedValue(new Error('the attachment exceeds the 25 MiB save cap'))
    renderPane()
    fireEvent.click(screen.getByRole('button', { name: 'Save to Library report.pdf' }))
    await waitFor(() => {
      expect(screen.getByText(/Could not save to Library/)).toBeTruthy()
    })
  })

  it('a lost response enters "Save result unknown" and the explicit retry re-sends the SAME token', async () => {
    saveMock.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    renderPane()
    fireEvent.click(screen.getByRole('button', { name: 'Save to Library report.pdf' }))
    await waitFor(() => {
      expect(screen.getByText(/Save result unknown/)).toBeTruthy()
    })
    const firstToken = saveMock.mock.calls[0][5].save_operation_token as string
    expect(firstToken).toMatch(/^sv-/)
    // No automatic replay happened.
    expect(saveMock).toHaveBeenCalledTimes(1)
    saveMock.mockResolvedValueOnce({
      saved: true,
      workspace_id: 'ws_1',
      entry: { is_dir: false, is_hidden: false, is_text_editable: false, name: 'report.pdf', path: 'mail/x/2026-10/report.pdf', size: 2048, modified_at: '' },
      path: 'mail/x/2026-10/report.pdf',
      absolute_path: '/work/mail/x/2026-10/report.pdf',
      size_bytes: 2048,
      audit_status: 'recorded',
      warning_code: null,
    })
    fireEvent.click(screen.getByRole('button', { name: 'Retry save' }))
    await waitFor(() => {
      expect(screen.getByText(/Saved to Library/)).toBeTruthy()
    })
    const secondToken = saveMock.mock.calls[1][5].save_operation_token as string
    expect(secondToken).toBe(firstToken)
    expect(saveMock).toHaveBeenCalledTimes(2)
  })

  it('a fresh save after an unresolved one carries a NEW token (a different token is a new request)', async () => {
    saveMock.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    renderPane()
    fireEvent.click(screen.getByRole('button', { name: 'Save to Library report.pdf' }))
    await waitFor(() => expect(screen.getByText(/Save result unknown/)).toBeTruthy())
    const firstToken = saveMock.mock.calls[0][5].save_operation_token as string
    saveMock.mockRejectedValueOnce(new Error('the attachment exceeds the 25 MiB save cap'))
    fireEvent.click(screen.getByRole('button', { name: 'Save to Library report.pdf' }))
    await waitFor(() => expect(screen.getByText(/Could not save to Library/)).toBeTruthy())
    const secondToken = saveMock.mock.calls[1][5].save_operation_token as string
    expect(secondToken).not.toBe(firstToken)
  })
})
