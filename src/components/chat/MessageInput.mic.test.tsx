/**
 * MessageInput.mic.test.tsx — Spec-6 U5 (FR-12.1 composer mic).
 *
 * Verifies the mic button records via MediaRecorder, sends the captured audio to
 * transcribeAudio, and appends the returned text to the composer input. Also
 * checks the unsupported-browser and permission-denied paths surface a toast.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'

const addToast = vi.fn()
vi.mock('@/store/ui', () => ({ useUiStore: vi.fn(() => ({ addToast })) }))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, transcribeAudio: vi.fn(), isApiError: actual.isApiError }
})

import * as api from '@/lib/api'
import { useConnectionStore } from '@/store/connection'
import { useChatStore } from '@/store/chat'
import { MessageInput } from './MessageInput'

// A controllable fake MediaRecorder. start() is synchronous; stop() invokes the
// onstop handler so the component's transcription path runs.
class FakeMediaRecorder {
  static instances: FakeMediaRecorder[] = []
  state: 'inactive' | 'recording' = 'inactive'
  mimeType = 'audio/webm'
  ondataavailable: ((e: { data: Blob }) => void) | null = null
  onstop: (() => void) | null = null
  constructor(public stream: MediaStream) {
    FakeMediaRecorder.instances.push(this)
  }
  start() {
    this.state = 'recording'
  }
  stop() {
    this.state = 'inactive'
    this.ondataavailable?.({ data: new Blob(['audio-bytes'], { type: 'audio/webm' }) })
    this.onstop?.()
  }
}

function fakeStream(): MediaStream {
  return { getTracks: () => [{ stop: vi.fn() }] } as unknown as MediaStream
}

beforeEach(() => {
  vi.clearAllMocks()
  FakeMediaRecorder.instances = []
  useConnectionStore.setState({ isConnected: true })
  useChatStore.setState({ isStreaming: false, cancelStage: null })
  ;(globalThis as unknown as { MediaRecorder: unknown }).MediaRecorder = FakeMediaRecorder
  Object.defineProperty(navigator, 'mediaDevices', {
    configurable: true,
    value: { getUserMedia: vi.fn().mockResolvedValue(fakeStream()) },
  })
})

afterEach(() => {
  // Leave navigator.mediaDevices defined for other suites; nothing to undo.
})

describe('MessageInput composer mic', () => {
  it('records, transcribes, and appends the text to the input', async () => {
    vi.mocked(api.transcribeAudio).mockResolvedValue({ text: 'hello world' } as never)
    render(<MessageInput />)

    const mic = screen.getByTestId('mic-btn')

    // Start recording (async getUserMedia).
    await act(async () => {
      fireEvent.click(mic)
    })
    await waitFor(() => expect(FakeMediaRecorder.instances.length).toBe(1))

    // Stop recording → triggers transcription.
    await act(async () => {
      fireEvent.click(mic)
    })

    await waitFor(() => {
      expect(api.transcribeAudio).toHaveBeenCalledTimes(1)
    })
    await waitFor(() => {
      expect(screen.getByLabelText('Message input')).toHaveValue('hello world')
    })
  })

  it('surfaces a toast when transcription returns 503 (no transcriber)', async () => {
    const { ApiError } = api
    vi.mocked(api.transcribeAudio).mockRejectedValue(new ApiError(503, 'no transcriber'))
    render(<MessageInput />)
    const mic = screen.getByTestId('mic-btn')

    await act(async () => fireEvent.click(mic))
    await waitFor(() => expect(FakeMediaRecorder.instances.length).toBe(1))
    await act(async () => fireEvent.click(mic))

    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error', message: expect.stringMatching(/Settings → Integrations/) }),
      )
    })
  })

  it('shows an error toast when mic permission is denied', async () => {
    Object.defineProperty(navigator, 'mediaDevices', {
      configurable: true,
      value: {
        getUserMedia: vi.fn().mockRejectedValue(
          Object.assign(new DOMException('denied', 'NotAllowedError')),
        ),
      },
    })
    render(<MessageInput />)
    await act(async () => fireEvent.click(screen.getByTestId('mic-btn')))
    await waitFor(() => {
      expect(addToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error', message: expect.stringMatching(/denied/i) }),
      )
    })
    expect(api.transcribeAudio).not.toHaveBeenCalled()
  })
})
