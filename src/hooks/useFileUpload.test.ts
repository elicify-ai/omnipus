// useFileUpload.test.ts — drag/drop + paste + harmful-file double-confirm.
// Extracted out of OmnipusComposer's own inline state; ChatScreen's existing
// harmful-upload integration tests exercise this through the rendered
// composer's real DOM events. These tests isolate the hook's own state
// machine (stage transitions, commit/dismiss, and the error-toast paths on
// a failed attach or an unmaterializable pasted image).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { ComposerRuntime } from '@assistant-ui/react'
import { useFileUpload } from './useFileUpload'
import { useUiStore } from '@/store/ui'
import { ApiError } from '@/lib/api'
import * as telemetry from '@/lib/telemetry'

function safeFile(name = 'photo.png') {
  return new File(['hi'], name, { type: 'image/png' })
}
function harmfulFile(name = 'malware.exe') {
  return new File(['x'], name, { type: 'application/octet-stream' })
}

function makeComposerRuntime(addAttachment = vi.fn(() => Promise.resolve())) {
  return { addAttachment } as unknown as ComposerRuntime
}

function dropEvent(files: File[]): React.DragEvent {
  return {
    preventDefault: vi.fn(),
    dataTransfer: { files },
  } as unknown as React.DragEvent
}

beforeEach(() => {
  act(() => { useUiStore.setState({ toasts: [] }) })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('useFileUpload — drag state', () => {
  it('onDragOver sets isDragging true and prevents default', () => {
    const { result } = renderHook(() => useFileUpload(makeComposerRuntime()))
    const e = { preventDefault: vi.fn() } as unknown as React.DragEvent

    act(() => result.current.onDragOver(e))

    expect(e.preventDefault).toHaveBeenCalled()
    expect(result.current.isDragging).toBe(true)
  })

  it('onDragLeave sets isDragging false', () => {
    const { result } = renderHook(() => useFileUpload(makeComposerRuntime()))
    act(() => result.current.onDragOver({ preventDefault: vi.fn() } as unknown as React.DragEvent))
    expect(result.current.isDragging).toBe(true)

    act(() => result.current.onDragLeave())
    expect(result.current.isDragging).toBe(false)
  })
})

describe('useFileUpload — non-harmful files attach immediately', () => {
  it('onDrop with a safe file calls addAttachment and never opens the confirm dialog', () => {
    const addAttachment = vi.fn(() => Promise.resolve())
    const { result } = renderHook(() => useFileUpload(makeComposerRuntime(addAttachment)))

    act(() => result.current.onDrop(dropEvent([safeFile()])))

    expect(addAttachment).toHaveBeenCalledTimes(1)
    expect(result.current.harmfulConfirm).toBeNull()
    expect(result.current.isDragging).toBe(false)
  })

  it('a failed addAttachment surfaces an error toast and logs telemetry', async () => {
    const addAttachment = vi.fn(() => Promise.reject(new Error('network down')))
    const logErrorSpy = vi.spyOn(telemetry, 'logError')
    const { result } = renderHook(() => useFileUpload(makeComposerRuntime(addAttachment)))

    await act(async () => {
      result.current.onDrop(dropEvent([safeFile('report.pdf')]))
      await Promise.resolve()
      await Promise.resolve()
    })

    const toasts = useUiStore.getState().toasts
    expect(toasts).toHaveLength(1)
    expect(toasts[0].variant).toBe('error')
    // Plain (non-ApiError) errors fall through getErrorMessage to err.message —
    // unchanged behavior for this class of error.
    expect(toasts[0].message).toBe('network down')
    expect(logErrorSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        event: 'attachmentUploadFailed',
        fileName: 'report.pdf',
        message: 'network down',
      })
    )
  })

  it('a failed addAttachment with an ApiError shows the clean userMessage, not the legacy "status: message" string', async () => {
    // uploadFiles() throws ApiError on a real 413 — userMessage is the clean,
    // human-facing string; the inherited Error.message is the legacy
    // "413: The request is too large." form that must NOT reach the toast.
    const apiErr = new ApiError(413, 'The request is too large.')
    const addAttachment = vi.fn(() => Promise.reject(apiErr))
    const logErrorSpy = vi.spyOn(telemetry, 'logError')
    const { result } = renderHook(() => useFileUpload(makeComposerRuntime(addAttachment)))

    await act(async () => {
      result.current.onDrop(dropEvent([safeFile('huge.zip')]))
      await Promise.resolve()
      await Promise.resolve()
    })

    const toasts = useUiStore.getState().toasts
    expect(toasts).toHaveLength(1)
    expect(toasts[0].variant).toBe('error')
    expect(toasts[0].message).toBe('The request is too large.')
    expect(toasts[0].message).not.toContain('413:')
    expect(logErrorSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        event: 'attachmentUploadFailed',
        fileName: 'huge.zip',
        message: '413: The request is too large.',
      })
    )
  })
})

describe('useFileUpload — harmful-file double-confirm', () => {
  it('opens stage 1 and does not attach', () => {
    const addAttachment = vi.fn(() => Promise.resolve())
    const { result } = renderHook(() => useFileUpload(makeComposerRuntime(addAttachment)))

    act(() => result.current.onDrop(dropEvent([harmfulFile()])))

    expect(addAttachment).not.toHaveBeenCalled()
    expect(result.current.harmfulConfirm?.harmfulNames).toEqual(['malware.exe'])
    expect(result.current.harmfulStage).toBe(1)
  })

  it('advanceHarmfulStage moves to stage 2 without attaching', () => {
    const addAttachment = vi.fn(() => Promise.resolve())
    const { result } = renderHook(() => useFileUpload(makeComposerRuntime(addAttachment)))
    act(() => result.current.onDrop(dropEvent([harmfulFile()])))

    act(() => result.current.advanceHarmfulStage())

    expect(result.current.harmfulStage).toBe(2)
    expect(addAttachment).not.toHaveBeenCalled()
  })

  it('confirmHarmfulUpload attaches the files and closes the dialog', () => {
    const addAttachment = vi.fn(() => Promise.resolve())
    const { result } = renderHook(() => useFileUpload(makeComposerRuntime(addAttachment)))
    act(() => result.current.onDrop(dropEvent([harmfulFile()])))
    act(() => result.current.advanceHarmfulStage())

    act(() => result.current.confirmHarmfulUpload())

    expect(addAttachment).toHaveBeenCalledTimes(1)
    expect(result.current.harmfulConfirm).toBeNull()
  })

  it('dismissHarmfulConfirm closes without attaching anything', () => {
    const addAttachment = vi.fn(() => Promise.resolve())
    const { result } = renderHook(() => useFileUpload(makeComposerRuntime(addAttachment)))
    act(() => result.current.onDrop(dropEvent([harmfulFile()])))

    act(() => result.current.dismissHarmfulConfirm())

    expect(result.current.harmfulConfirm).toBeNull()
    expect(addAttachment).not.toHaveBeenCalled()
  })

  it('a subsequent harmful upload restarts at stage 1 (no stale stage leak)', () => {
    const { result } = renderHook(() => useFileUpload(makeComposerRuntime()))
    act(() => result.current.onDrop(dropEvent([harmfulFile('first.exe')])))
    act(() => result.current.advanceHarmfulStage())
    expect(result.current.harmfulStage).toBe(2)
    act(() => result.current.dismissHarmfulConfirm())

    act(() => result.current.onDrop(dropEvent([harmfulFile('second.exe')])))

    expect(result.current.harmfulStage).toBe(1)
    expect(result.current.harmfulConfirm?.harmfulNames).toEqual(['second.exe'])
  })
})

describe('useFileUpload — paste', () => {
  it('pasted image data attaches via addAttachment and prevents default', () => {
    const addAttachment = vi.fn(() => Promise.resolve())
    const { result } = renderHook(() => useFileUpload(makeComposerRuntime(addAttachment)))
    const file = safeFile('pasted.png')
    const e = {
      preventDefault: vi.fn(),
      clipboardData: {
        items: [{ type: 'image/png', getAsFile: () => file }],
      },
    } as unknown as React.ClipboardEvent

    act(() => result.current.onPaste(e))

    expect(e.preventDefault).toHaveBeenCalled()
    expect(addAttachment).toHaveBeenCalledTimes(1)
  })

  it('an image clipboard item that cannot be materialized as a file shows an error toast', () => {
    const { result } = renderHook(() => useFileUpload(makeComposerRuntime()))
    const e = {
      preventDefault: vi.fn(),
      clipboardData: {
        items: [{ type: 'image/png', getAsFile: () => null }],
      },
    } as unknown as React.ClipboardEvent

    act(() => result.current.onPaste(e))

    expect(e.preventDefault).toHaveBeenCalled()
    const toasts = useUiStore.getState().toasts
    expect(toasts).toHaveLength(1)
    expect(toasts[0].variant).toBe('error')
  })

  it('a non-image paste is left alone (no preventDefault, no toast)', () => {
    const { result } = renderHook(() => useFileUpload(makeComposerRuntime()))
    const e = {
      preventDefault: vi.fn(),
      clipboardData: { items: [{ type: 'text/plain', getAsFile: () => null }] },
    } as unknown as React.ClipboardEvent

    act(() => result.current.onPaste(e))

    expect(e.preventDefault).not.toHaveBeenCalled()
    expect(useUiStore.getState().toasts).toHaveLength(0)
  })
})
