// Harmful-file upload two-stage confirm (replaces the native window.confirm pair).
// Non-harmful files attach immediately; harmful files open a stage-1 warning that
// must be confirmed (Continue → stage 2), and only a stage-2 Upload attaches them.
// Cancel/Escape at either stage attaches nothing, and a subsequent harmful upload
// restarts at stage 1 (no stale stage leaks across confirmations).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'

import { OmnipusComposer } from './ChatScreen'

// addAttachment is the attachment/runtime sink we assert against. It must return
// a promise because commitFiles chains `.catch()` on the result.
const addAttachment = vi.fn(() => Promise.resolve())

// Reference the module-level `React` import (not an in-factory `require('react')`)
// — the latter was corrupting the shared React module binding across test files
// sharing a reused worker process (`pool: 'forks'` in vite.config.ts), causing an
// intermittent `ReferenceError: useRef is not defined` in unrelated sibling test
// files run in the same batch.
vi.mock('@assistant-ui/react', () => {
  return {
    useThreadViewportStore: () => ({ getState: () => ({ isAtBottom: true }) }),
    ThreadPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Viewport: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Messages: () => null,
    },
    MessagePrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Parts: () => null,
    },
    ComposerPrimitive: {
      Root: ({ children, className, onSubmit }: { children: React.ReactNode; className?: string; onSubmit?: (e: React.FormEvent) => void }) =>
        React.createElement('form', { className, onSubmit, 'data-testid': 'composer-form' }, children),
      Input: ({ disabled, placeholder, className, onChange, onKeyDown, onBlur }: {
        disabled?: boolean; placeholder?: string; className?: string;
        onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
        onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
        onBlur?: () => void;
      }) =>
        React.createElement('textarea', {
          disabled, placeholder, className, onChange, onKeyDown, onBlur,
          'data-testid': 'composer-input',
        }),
      Send: ({ disabled, children, className, 'data-testid': testId, 'aria-label': ariaLabel }: {
        disabled?: boolean; children?: React.ReactNode; className?: string;
        'data-testid'?: string; 'aria-label'?: string
      }) =>
        React.createElement('button', {
          type: 'button', disabled, className,
          'data-testid': testId ?? 'chat-send', 'aria-label': ariaLabel,
        }, children),
      AddAttachment: ({ disabled, children, className, 'aria-label': ariaLabel }: {
        disabled?: boolean; children?: React.ReactNode; className?: string; 'aria-label'?: string
      }) =>
        React.createElement('button', { type: 'button', disabled, className, 'aria-label': ariaLabel, 'data-testid': 'add-attachment' }, children),
      Attachments: () => null,
    },
    AttachmentPrimitive: {
      Root: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Name: () => null,
      Remove: () => null,
      Thumb: () => null,
    },
    MessagePartPrimitive: { InProgress: () => null },
    ActionBarPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Copy: ({ children }: { children: React.ReactNode }) => React.createElement('span', {}, children),
    },
    AuiIf: () => null,
    useComposerRuntime: vi.fn(() => ({
      getState: () => ({ text: '' }),
      setText: vi.fn(),
      addAttachment,
    })),
    useMessage: () => ({ id: 'msg_1', role: 'assistant', status: { type: 'complete' }, content: [] }),
    makeAssistantToolUI: () => () => null,
  }
})

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: () => ({ data: [], isError: false, refetch: vi.fn() }),
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  }
})

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/lib/api', () => ({
  fetchAgents: vi.fn().mockResolvedValue([]),
  fetchSessionMessages: vi.fn().mockResolvedValue([]),
  createSession: vi.fn(),
  uploadFiles: vi.fn(),
  fetchProviders: vi.fn().mockResolvedValue([]),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./SessionPanel', () => ({ SessionPanel: () => null }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))

function resetStores() {
  act(() => {
    useChatStore.setState({
      messages: [], isStreaming: false, isReplaying: false,
      toolCalls: {}, sessionTokens: 0, sessionCost: 0,
    })
    useConnectionStore.setState({ connection: null, isConnected: true, connectionError: null })
    useSessionStore.setState({
      activeSessionId: 'sess_harmful_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  resetStores()
})

// Drops files onto the composer dropzone (the OmnipusComposer root <div>), which
// drives handleFilesSelected via onDrop reading dataTransfer.files.
function dropFiles(files: File[]) {
  const dropzone = screen.getByTestId('composer-form').parentElement as HTMLElement
  fireEvent.drop(dropzone, { dataTransfer: { files } })
}

const safeFile = () => new File(['hi'], 'photo.png', { type: 'image/png' })
const harmfulFile = (name = 'malware.exe') => new File(['x'], name, { type: 'application/octet-stream' })

describe('ChatScreen — harmful-file upload confirmation', () => {
  it('attaches a non-harmful file immediately with no dialog', async () => {
    render(<OmnipusComposer />)
    dropFiles([safeFile()])
    await waitFor(() => expect(addAttachment).toHaveBeenCalledTimes(1))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('a harmful file opens stage 1 and does NOT attach', async () => {
    render(<OmnipusComposer />)
    dropFiles([harmfulFile()])
    const dialog = await screen.findByRole('alertdialog')
    // Stage 1 title (heading) — distinct from stage 2's "Confirm upload".
    expect(within(dialog).getByRole('heading', { name: /Potentially harmful file/i })).toBeInTheDocument()
    expect(addAttachment).not.toHaveBeenCalled()
  })

  it('stage-1 Continue advances to stage 2 and still does NOT attach', async () => {
    render(<OmnipusComposer />)
    dropFiles([harmfulFile()])
    const dialog = await screen.findByRole('alertdialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Continue' }))
    await screen.findByText(/Confirm upload/i)
    expect(addAttachment).not.toHaveBeenCalled()
  })

  it('stage-2 Upload attaches the file', async () => {
    render(<OmnipusComposer />)
    dropFiles([harmfulFile()])
    let dialog = await screen.findByRole('alertdialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Continue' }))
    await screen.findByText(/Confirm upload/i)
    dialog = screen.getByRole('alertdialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Upload' }))
    await waitFor(() => expect(addAttachment).toHaveBeenCalledTimes(1))
  })

  it('Cancel at stage 1 attaches nothing', async () => {
    render(<OmnipusComposer />)
    dropFiles([harmfulFile()])
    const dialog = await screen.findByRole('alertdialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(addAttachment).not.toHaveBeenCalled()
  })

  it('Escape at stage 2 attaches nothing', async () => {
    render(<OmnipusComposer />)
    dropFiles([harmfulFile()])
    const dialog = await screen.findByRole('alertdialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Continue' }))
    await screen.findByText(/Confirm upload/i)
    fireEvent.keyDown(screen.getByRole('alertdialog'), { key: 'Escape', code: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(addAttachment).not.toHaveBeenCalled()
  })

  it('a subsequent harmful upload restarts at stage 1 (no stale stage)', async () => {
    render(<OmnipusComposer />)
    // First harmful set: advance to stage 2, then cancel via Escape.
    dropFiles([harmfulFile('first.exe')])
    let dialog = await screen.findByRole('alertdialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Continue' }))
    await screen.findByText(/Confirm upload/i)
    fireEvent.keyDown(screen.getByRole('alertdialog'), { key: 'Escape', code: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())

    // Second harmful set must open at STAGE 1 again (warning, not the stage-2 confirm).
    dropFiles([harmfulFile('second.exe')])
    dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByRole('heading', { name: /Potentially harmful file/i })).toBeInTheDocument()
    expect(within(dialog).queryByRole('heading', { name: /Confirm upload/i })).not.toBeInTheDocument()
    expect(addAttachment).not.toHaveBeenCalled()
  })
})
