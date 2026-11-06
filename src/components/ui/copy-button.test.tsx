/**
 * CopyButton — W2-1 contract §2.8 (R41; #855 job 2).
 *
 * RED on 69aa07d9a: `src/components/ui/copy-button.tsx` does not exist yet
 * (contract §2.8 names it as a new file) — fails at import for the right
 * reason.
 *
 * Mock boundary: only `navigator.clipboard` (the browser API edge) is
 * doubleed; everything else — Button/IconButton, the Tooltip it composes, the
 * DOM — is real. Fake timers stand in for the clock (the 2000ms feedback
 * window). The 44px coarse-pointer row is asserted as the touch-minimum
 * token class; the computed px is the manifest pointer kind's job.
 */
import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { COPY_FEEDBACK_MS, CopyButton } from './copy-button'

// Walk every element in the container, normalizing Tailwind's underscore
// spaces, so token/mix assertions read as the contract writes them.
function classString(container: HTMLElement): string {
  let out = ''
  for (const el of container.querySelectorAll('*')) out += ' ' + (el.getAttribute('class') ?? '').replace(/_/g, ' ')
  return out
}

const writeText = vi.fn<(text: string) => Promise<void>>()

function setClipboard(value: unknown) {
  Object.defineProperty(navigator, 'clipboard', { value, configurable: true })
}

async function click(button: HTMLElement) {
  await act(async () => {
    fireEvent.click(button)
  })
}

beforeEach(() => {
  vi.useFakeTimers()
  writeText.mockReset()
  writeText.mockImplementation(() => Promise.resolve())
  setClipboard({ writeText })
})

afterEach(() => {
  // Remove the stub so later runs start from the platform default.
  delete (navigator as unknown as { clipboard?: unknown }).clipboard
  vi.useRealTimers()
})

describe('CopyButton — idle identity (§2.8)', () => {
  it('renders a button whose accessible name is the default label "Copy"', () => {
    render(<CopyButton value="hello" />)
    expect(screen.getByRole('button', { name: 'Copy' })).toBeInTheDocument()
  })

  it('the accessible name stays the label while feedback is showing (never becomes "Copied")', async () => {
    render(<CopyButton value="hello" />)
    await click(screen.getByRole('button', { name: 'Copy' }))
    expect(screen.getByRole('button', { name: 'Copy' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Copied' })).not.toBeInTheDocument()
  })

  it('label prop renames the button', () => {
    render(<CopyButton value="hello" label="Copy webhook URL" />)
    expect(screen.getByRole('button', { name: 'Copy webhook URL' })).toBeInTheDocument()
  })
})

describe('CopyButton — success flow (§2.8 Behaviour)', () => {
  it('writes the resolved string to the clipboard, announces "Copied", swaps to the success icon, and calls onCopied', async () => {
    const onCopied = vi.fn()
    render(<CopyButton value="webhook-secret" onCopied={onCopied} />)
    await click(screen.getByRole('button', { name: 'Copy' }))
    // Exact call argument: the clipboard receives the resolved value.
    expect(writeText).toHaveBeenCalledTimes(1)
    expect(writeText).toHaveBeenCalledWith('webhook-secret')
    expect(screen.getByRole('status')).toHaveTextContent('Copied')
    // The polite, sr-only status region carries the announcement.
    expect(screen.getByRole('status')).toHaveClass('sr-only')
    expect(screen.getByTestId('copy-button-success')).toBeInTheDocument()
    expect(onCopied).toHaveBeenCalledTimes(1)
  })

  it('resolves a function value before writing', async () => {
    render(<CopyButton value={() => 'resolved-fn-value'} />)
    await click(screen.getByRole('button', { name: 'Copy' }))
    expect(writeText).toHaveBeenCalledWith('resolved-fn-value')
  })

  it('returns to idle exactly at COPY_FEEDBACK_MS and not one ms earlier', async () => {
    render(<CopyButton value="hello" />)
    await click(screen.getByRole('button', { name: 'Copy' }))
    expect(screen.getByRole('status')).toHaveTextContent('Copied')

    act(() => vi.advanceTimersByTime(COPY_FEEDBACK_MS - 1))
    expect(screen.getByRole('status')).toHaveTextContent('Copied')
    expect(screen.getByTestId('copy-button-success')).toBeInTheDocument()

    act(() => vi.advanceTimersByTime(1))
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(screen.queryByTestId('copy-button-success')).not.toBeInTheDocument()
  })

  it('COPY_FEEDBACK_MS is the contract’s 2000ms', () => {
    expect(COPY_FEEDBACK_MS).toBe(2000)
  })

  it('a second click during feedback copies again and restarts the timer', async () => {
    render(<CopyButton value="hello" />)
    const button = screen.getByRole('button', { name: 'Copy' })
    await click(button)
    act(() => vi.advanceTimersByTime(1500))
    await click(button)
    expect(writeText).toHaveBeenCalledTimes(2)
    // 1500ms after the SECOND click the feedback must still be showing.
    act(() => vi.advanceTimersByTime(1500))
    expect(screen.getByRole('status')).toHaveTextContent('Copied')
    // 2000ms after the second click it is idle again.
    act(() => vi.advanceTimersByTime(500))
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })
})

describe('CopyButton — failure flows, no silent failure (§2.8)', () => {
  it('a clipboard rejection announces "Copy failed", shows the error state, and hands the error to onCopyError', async () => {
    const failure = new Error('write rejected')
    writeText.mockImplementation(() => Promise.reject(failure))
    const onCopyError = vi.fn()
    render(<CopyButton value="hello" onCopyError={onCopyError} />)
    await click(screen.getByRole('button', { name: 'Copy' }))
    expect(screen.getByRole('status')).toHaveTextContent('Copy failed')
    expect(onCopyError).toHaveBeenCalledTimes(1)
    expect(onCopyError).toHaveBeenCalledWith(failure)
    expect(screen.queryByTestId('copy-button-success')).not.toBeInTheDocument()
    // Icon colours with --color-error (the WarningCircle swap is pixel data
    // for the screenshot gate; the colour token is the assertable contract).
    expect(classString(document.body)).toContain('text-[var(--color-error)]')
  })

  it('a missing navigator.clipboard (plain-HTTP LAN) behaves exactly like a rejection', async () => {
    const onCopyError = vi.fn()
    setClipboard(undefined)
    render(<CopyButton value="hello" onCopyError={onCopyError} />)
    await click(screen.getByRole('button', { name: 'Copy' }))
    expect(screen.getByRole('status')).toHaveTextContent('Copy failed')
    expect(onCopyError).toHaveBeenCalledTimes(1)
    expect(writeText).not.toHaveBeenCalled()
  })

  it('failedLabel renames the failure announcement', async () => {
    writeText.mockImplementation(() => Promise.reject(new Error('no')))
    render(<CopyButton value="hello" failedLabel="Could not copy" />)
    await click(screen.getByRole('button', { name: 'Copy' }))
    expect(screen.getByRole('status')).toHaveTextContent('Could not copy')
  })
})

describe('CopyButton — props', () => {
  it('disabled blocks the clipboard entirely', async () => {
    render(<CopyButton value="hello" disabled />)
    const button = screen.getByRole('button', { name: 'Copy' })
    expect(button).toBeDisabled()
    await click(button)
    expect(writeText).not.toHaveBeenCalled()
  })

  it('size="sm" (default) renders the small icon button; size="default" the regular one', () => {
    const sm = render(<CopyButton value="hello" />)
    expect(screen.getByRole('button', { name: 'Copy' })).toHaveClass('h-8')
    sm.unmount()
    render(<CopyButton value="hello" size="default" />)
    expect(screen.getByRole('button', { name: 'Copy' })).toHaveClass('h-9')
  })

  it('grows to the 44px touch minimum on a coarse pointer (token class)', () => {
    render(<CopyButton value="hello" />)
    expect(classString(document.body)).toContain('min-h-[var(--target-touch-minimum)]')
    expect(classString(document.body)).toMatch(/pointer-coarse:min-h-\[var\(--target-touch-minimum\)\]/)
  })
})
