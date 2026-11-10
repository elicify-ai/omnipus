// F11 regression: an SMTP send failure must not discard the user's compose input.
// Oracle: the F11 failure brief, not the current optimistic-close behavior.
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useUiStore } from '@/store/ui'

const {
  fetchAgents,
  fetchMailboxes,
  fetchMailFolders,
  fetchMailMessages,
  fetchMailSummary,
  sendMailMessage,
} = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailSummary: vi.fn(),
  sendMailMessage: vi.fn(),
}))

// Only the network boundary is faked. MailPanel, Compose, its editors, and
// the state transition after the rejected send all run as real components.
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAgents,
  fetchMailboxes,
}))
vi.mock('@/lib/api/mail', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api/mail')>()),
  fetchMailFolders,
  fetchMailMessages,
  fetchMailSummary,
  sendMailMessage,
}))

import { MailPanel } from './MailPanel'

const recipient = {
  to: 'ada@example.test',
  cc: 'beau@example.test',
  bcc: 'cy@example.test',
}
const subject = 'F11 retain my composed mail'
const message = 'This body took time to write. Do not lose it after SMTP fails.'
const attachmentName = 'f11-proof.txt'

beforeEach(() => {
  // Vitest does not set React's act environment flag without Jest globals.
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  sessionStorage.clear()
  fetchAgents.mockReset().mockResolvedValue([{ figure: 'Omnipus', role: 'general', id: 'mia', name: 'Mia' }])
  fetchMailboxes.mockReset().mockResolvedValue([
    { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mailbox@test.local' },
  ])
  fetchMailFolders.mockReset().mockResolvedValue({ folders: [] })
  fetchMailMessages.mockReset().mockResolvedValue({ messages: [], truncated: false, next_before_uid: null })
  fetchMailSummary.mockReset().mockResolvedValue({ items: [] })
  sendMailMessage.mockReset()
})

afterEach(() => {
  cleanup()
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
  vi.unstubAllGlobals()
})

describe('Compose after a failed send (F11)', () => {
  it('keeps recipients, subject, body and attachment available and editable after the failure', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><MailPanel workspaceId="ws-1" /></QueryClientProvider>)
    const composeButton = await screen.findByRole('button', { name: 'Compose' })
    await waitFor(() => expect(composeButton).toBeEnabled())
    fireEvent.click(composeButton)

    const dialog = await screen.findByRole('dialog', { name: 'Compose message' })
    for (const [field, address] of [
      ['To', recipient.to], ['Cc', recipient.cc], ['Bcc', recipient.bcc],
    ] as const) {
      const input = within(dialog).getByRole('textbox', { name: new RegExp(`^${field}$`) })
      fireEvent.change(input, { target: { value: address } })
      fireEvent.keyDown(input, { key: 'Enter' })
    }
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Subject' }), { target: { value: subject } })
    // The Tiptap-compatible value setter updates React before change dispatch.
    act(() => fireEvent.change(within(dialog).getByRole('textbox', { name: 'Message' }), { target: { value: message } }))
    fireEvent.change(within(dialog).getByLabelText('Attach files'), {
      target: { files: [new File(['F11 attachment proof'], attachmentName, { type: 'text/plain' })] },
    })
    expect(within(dialog).getByRole('button', { name: `Remove ${attachmentName}` })).toBeInTheDocument()

    // FileReader finishes asynchronously before sending. Keep its state
    // updates and the rejected network response within React's act boundary.
    let notifyRequestStarted: () => void = () => { throw new Error('Send never started') }
    const requestStarted = new Promise<void>((resolve) => { notifyRequestStarted = resolve })
    sendMailMessage.mockImplementation(() => {
      notifyRequestStarted()
      return Promise.reject(Object.assign(new Error('SMTP unavailable'), { code: 'connect_refused' }))
    })
    await act(async () => {
      fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }))
      await requestStarted
    })
    expect(sendMailMessage).toHaveBeenCalledTimes(1)
    expect(sendMailMessage).toHaveBeenCalledWith('ws-1', 'mia', expect.objectContaining({
      to: [recipient.to],
      cc: [recipient.cc],
      bcc: [recipient.bcc],
      subject,
      body_markdown: message,
      attachments: [{
        filename: attachmentName,
        content_type: 'text/plain',
        data_base64: 'RjExIGF0dGFjaG1lbnQgcHJvb2Y=',
      }],
    }))
    // Waiting for the error, not just the outgoing call, proves these checks
    // exercise the post-failure state rather than the pre-response state.
    await waitFor(() => expect(useUiStore.getState().toasts).toEqual(expect.arrayContaining([
      expect.objectContaining({ message: 'connect_refused', variant: 'error' }),
    ])))

    // Both legitimate fixes work: retain the open dialog, or restore its data
    // when reopened. A blank reopened composer is the bug.
    if (!screen.queryByRole('dialog', { name: 'Compose message' })) fireEvent.click(composeButton)
    const recovered = await screen.findByRole('dialog', { name: 'Compose message' })
    const fields = within(recovered)
    expect(fields.queryAllByTestId('recipient-chip').map((chip) => chip.textContent?.trim()))
      .toEqual([recipient.to, recipient.cc, recipient.bcc])
    expect(fields.getByRole('textbox', { name: 'Subject' })).toHaveValue(subject)
    expect(fields.getByRole('textbox', { name: 'Message' })).toHaveTextContent(message)
    expect(fields.getByRole('button', { name: `Remove ${attachmentName}` })).toBeInTheDocument()

    const editableSubject = fields.getByRole('textbox', { name: 'Subject' })
    fireEvent.change(editableSubject, { target: { value: 'Updated after failure' } })
    expect(editableSubject).toHaveValue('Updated after failure')
    const editableMessage = fields.getByRole('textbox', { name: 'Message' })
    act(() => fireEvent.change(editableMessage, { target: { value: 'Recovered and edited after failure.' } }))
    expect(editableMessage).toHaveTextContent('Recovered and edited after failure.')

    for (const [field, address] of [
      ['To', 'drew@example.test'], ['Cc', 'eve@example.test'], ['Bcc', 'fran@example.test'],
    ] as const) {
      const input = fields.getByRole('textbox', { name: new RegExp(`^${field}$`) })
      fireEvent.change(input, { target: { value: address } })
      fireEvent.keyDown(input, { key: 'Enter' })
    }
    expect(fields.getAllByTestId('recipient-chip').map((chip) => chip.textContent?.trim()))
      .toEqual([
        recipient.to, 'drew@example.test', recipient.cc, 'eve@example.test',
        recipient.bcc, 'fran@example.test',
      ])

    fireEvent.click(fields.getByRole('button', { name: `Remove ${attachmentName}` }))
    expect(fields.queryByRole('button', { name: `Remove ${attachmentName}` })).not.toBeInTheDocument()
    fireEvent.change(fields.getByLabelText('Attach files'), {
      target: { files: [new File(['replacement'], 'replacement.txt', { type: 'text/plain' })] },
    })
    expect(fields.getByRole('button', { name: 'Remove replacement.txt' })).toBeInTheDocument()
  })
})
