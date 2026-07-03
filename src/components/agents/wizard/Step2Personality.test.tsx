/**
 * Tests for Step2Personality wizard step.
 *
 * TDD plan items covered:
 * - T17 (Step2Personality.test.tsx): heartbeat removed from create wizard (FR-017 / US-5.AC3)
 * - T22 (Step2Personality.soulUpload.test.tsx): soul .md upload on add screen (FR-026 / US-10)
 *
 * Traces to: US-5.AC3, US-5.AC4, US-10.AC1, US-10.AC2, FR-017, FR-026
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { Step2Personality } from './Step2Personality'
import type { WizardSubmitPayload } from '../CreateAgentWizard'

// Step2Personality imports useUiStore for addToast — provide a noop.
vi.mock('@/store/ui', () => ({
  useUiStore: (selector: (s: { addToast: () => void }) => unknown) =>
    selector({ addToast: vi.fn() }),
}))

// VoiceProviderSub makes a query to detect the voice provider. Stub it to
// avoid wiring a query client into these unit tests.
vi.mock('../voice-provider-sub', () => ({
  VoiceProviderSub: ({ value, onChange }: { value: string | null; onChange: (v: string | null) => void }) => (
    <input
      data-testid="voice-provider-input"
      value={value ?? ''}
      onChange={(e) => onChange(e.target.value || null)}
    />
  ),
}))

const BASE_PAYLOAD: WizardSubmitPayload = {
  type: 'Main',
  name: '',
  description: '',
  color: '#ffffff',
  icon: 'Robot',
  model: '',
  soul: '',
  heartbeat: '',
  heartbeat_enabled: false,
  heartbeat_interval: 1800,
}

function makeSetField() {
  const payload = { ...BASE_PAYLOAD }
  const setField = vi.fn(<L extends keyof WizardSubmitPayload>(
    field: L,
    value: WizardSubmitPayload[L],
  ) => {
    ;(payload as WizardSubmitPayload)[field] = value
  })
  return { payload, setField }
}

function renderStep(overrides: Partial<WizardSubmitPayload> = {}, initialType: 'Main' | 'Subagent' | 'subagent_3p' = 'Main') {
  const { payload, setField } = makeSetField()
  const mergedPayload = { ...payload, ...overrides }
  render(
    <Step2Personality
      payload={mergedPayload}
      setField={setField}
      initialType={initialType}
    />
  )
  return { mergedPayload, setField }
}

// ── FR-017 / US-5.AC3 — no heartbeat fields in the create wizard ─────────────

describe('Step2Personality — heartbeat removed (FR-017 / US-5.AC3)', () => {
  it('does NOT render heartbeat-related fields for Main agents (FR-017)', () => {
    // Traces to: US-5.AC3 — create wizard Step 2 has no heartbeat fields.
    renderStep({}, 'Main')
    expect(screen.queryByTestId('wizard-heartbeat')).toBeNull()
    expect(screen.queryByText(/enable periodic heartbeat/i)).toBeNull()
    expect(screen.queryByText(/heartbeat body/i)).toBeNull()
    expect(screen.queryByText(/Heartbeat \(Main only\)/i)).toBeNull()
    expect(screen.queryByText(/Periodic instruction body/i)).toBeNull()
  })

  it('does NOT render heartbeat-related fields for Subagent type (FR-017)', () => {
    // Subagents never had heartbeat; confirm they still don't after the refactor.
    renderStep({}, 'Subagent')
    expect(screen.queryByTestId('wizard-heartbeat')).toBeNull()
    expect(screen.queryByText(/heartbeat/i)).toBeNull()
  })

  it('still renders the soul textarea (always required)', () => {
    renderStep({}, 'Main')
    expect(screen.getByTestId('wizard-soul')).toBeInTheDocument()
    // Parity refactor (P3, 2026-07-03): the step now renders the shared
    // BehaviorFields, whose Main-agent title is "Personality & instructions"
    // (the edit dialog's label) rather than the wizard's old bare "Soul".
    expect(screen.getByText(/Personality & instructions/i)).toBeInTheDocument()
    expect(screen.getByTestId('soul-minlength-hint')).toBeInTheDocument()
  })

  it('still renders voice field for Main agents', () => {
    renderStep({}, 'Main')
    expect(screen.getByTestId('wizard-voice')).toBeInTheDocument()
  })

  it('does NOT render voice field for Subagent / worker types', () => {
    renderStep({}, 'Subagent')
    expect(screen.queryByTestId('wizard-voice')).toBeNull()
  })
})

// ── FR-026 / US-10 — soul .md upload ────────────────────────────────────────

describe('Step2Personality — soul markdown upload (FR-026 / US-10)', () => {
  it('renders the Upload .md button for Main agents (FR-026 / US-10.AC1)', () => {
    // Traces to: US-10.AC1 — soul field offers an Upload .md control.
    renderStep({}, 'Main')
    expect(screen.getByTestId('wizard-soul-upload')).toBeInTheDocument()
  })

  it('renders the Upload .md button for Subagent type too', () => {
    // Parity: all agent types should be able to upload a soul file.
    renderStep({}, 'Subagent')
    expect(screen.getByTestId('wizard-soul-upload')).toBeInTheDocument()
  })

  it('file upload fills the soul field with file contents (FR-026 / US-10.AC2)', async () => {
    // Traces to: US-10.AC2 — picking a markdown file fills the soul field.
    //
    // Strategy:
    // 1. Replace globalThis.FileReader with a class-based mock BEFORE the
    //    component mounts so `new FileReader()` inside handleSoulUpload uses
    //    our stub. (vi.spyOn on a constructor is unreliable in jsdom.)
    // 2. Render the component so React's own document.createElement calls
    //    have already fired.
    // 3. THEN intercept the NEXT document.createElement('input') call (the
    //    one from handleSoulUpload) by installing the spy AFTER render.
    const soulContent = '# My Soul\n\nYou are Aria.'
    const mockFile = new File([soulContent], 'SOUL.md', { type: 'text/markdown' })

    // Replace FileReader with a class that resolves readAsText synchronously.
    const OriginalFileReader = globalThis.FileReader
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(globalThis as any).FileReader = class MockFileReader {
      result: string | null = null
      onload: ((e: ProgressEvent<FileReader>) => void) | null = null
      onerror: ((e: ProgressEvent<FileReader>) => void) | null = null

      readAsText(_file: File) {
        this.result = soulContent
        // Fire onload asynchronously (microtask) — mirrors real FileReader.
        Promise.resolve().then(() => {
          if (this.onload) {
            this.onload({ target: this } as unknown as ProgressEvent<FileReader>)
          }
        })
      }
    }

    // Render FIRST so React's own document.createElement calls are exhausted
    // before we install the one-shot spy.
    const { setField } = renderStep({ soul: '' }, 'Main')
    const uploadButton = screen.getByTestId('wizard-soul-upload')

    // NOW install the spy — the next createElement('input') will be our mock.
    const mockInput = {
      type: '',
      accept: '',
      onchange: null as ((e: Event) => void) | null,
      click: vi.fn(function (this: typeof mockInput) {
        if (this.onchange) {
          const event = { target: { files: [mockFile] } } as unknown as Event
          this.onchange(event)
        }
      }),
    }
    const origCreate = document.createElement.bind(document)
    const createElementSpy = vi
      .spyOn(document, 'createElement')
      .mockImplementationOnce((tag: string) => {
        if (tag === 'input') return mockInput as unknown as HTMLInputElement
        return origCreate(tag)
      })

    fireEvent.click(uploadButton)

    // FileReader.readAsText fires → onload (microtask) → setField('soul', soulContent)
    await waitFor(() => {
      expect(setField).toHaveBeenCalledWith('soul', soulContent)
    })

    createElementSpy.mockRestore()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(globalThis as any).FileReader = OriginalFileReader
  })

  it('Upload .md accepts .md, .markdown, .txt extensions', () => {
    // US-10.AC1 — the control accepts the correct extensions.
    renderStep({}, 'Main')
    const uploadButton = screen.getByTestId('wizard-soul-upload')
    // The button fires a programmatic file input — intercept createElement
    // and verify the accept attribute.
    let capturedAccept = ''
    const spy = vi.spyOn(document, 'createElement').mockImplementationOnce((tag) => {
      if (tag === 'input') {
        const el = { type: '', accept: '', onchange: null, click: vi.fn() }
        Object.defineProperty(el, 'accept', {
          set(v) { capturedAccept = v as string },
          get() { return capturedAccept },
        })
        return el as unknown as HTMLInputElement
      }
      return document.createElement.call(document, tag)
    })
    fireEvent.click(uploadButton)
    expect(capturedAccept).toBe('.md,.markdown,.txt')
    spy.mockRestore()
  })
})
