/**
 * CopyButton — W2-1 contract §2.8 (R41; #855 job 2).
 *
 * Mock boundary: only `navigator.clipboard` (the browser API edge) is
 * doubleed; everything else — Button/IconButton, the Tooltip it composes, the
 * DOM — is real. Fake timers stand in for the clock (the 2000ms feedback
 * window). The 44px coarse-pointer row is asserted as the touch-minimum
 * token class; the computed px is the manifest pointer kind's job.
 *
 * Announcement region (K2, team-lead decision, binding): the kit Button
 * (IconButton included) ALWAYS renders exactly one portalled sr-only
 * role="status" announcement region (button.tsx::ActionAnnouncement, pinned
 * by button.test.tsx "updates the same initially empty live region…").
 * CopyButton must announce THROUGH that one region — a second region is a
 * defect. So every feedback assertion here goes through the single-region
 * doorway below, and idle means the region is present but EMPTY, never
 * absent. RED on the developer's current tree, which still renders
 * CopyButton's own second status region during feedback; the developer
 * changes CopyButton after this commit.
 */
import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { COPY_FEEDBACK_MS, CopyButton } from './copy-button'

// Walk every element in the container, normalizing Tailwind's underscore
// spaces, so token/mix assertions read as the contract writes them. The
// class ATTRIBUTE is read (an SVG's className is an SVGAnimatedString).
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

// The single-region doorway (K2): exactly ONE role="status" region may exist
// for a rendered CopyButton — the kit Button's own announcement region. Any
// second region fails here before the text is even read.
function theAnnouncementRegion(): HTMLElement {
  const regions = screen.getAllByRole('status')
  expect(regions).toHaveLength(1)
  return regions[0]
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

describe('CopyButton — one announcement region, empty when idle (K2)', () => {
  it('exactly one role="status" region exists before any click, and it carries NO announcement text', () => {
    render(<CopyButton value="hello" />)
    expect(screen.getAllByRole('status')).toHaveLength(1)
    expect(screen.getByRole('status')).toBeEmptyDOMElement()
  })

  it('after COPY_FEEDBACK_MS the same single region is empty again (never absent)', async () => {
    render(<CopyButton value="hello" />)
    await click(screen.getByRole('button', { name: 'Copy' }))
    expect(theAnnouncementRegion()).toHaveTextContent('Copied')
    act(() => vi.advanceTimersByTime(COPY_FEEDBACK_MS))
    expect(screen.getAllByRole('status')).toHaveLength(1)
    expect(screen.getByRole('status')).toBeEmptyDOMElement()
  })
})

describe('CopyButton — success flow (§2.8 Behaviour)', () => {
  it('writes the resolved string to the clipboard and announces "Copied" through the single sr-only region', async () => {
    const onCopied = vi.fn()
    render(<CopyButton value="webhook-secret" onCopied={onCopied} />)
    await click(screen.getByRole('button', { name: 'Copy' }))
    // Exact call argument: the clipboard receives the resolved value.
    expect(writeText).toHaveBeenCalledTimes(1)
    expect(writeText).toHaveBeenCalledWith('webhook-secret')
    // The ONE status region carries the announcement, and it is sr-only.
    const region = theAnnouncementRegion()
    expect(region).toHaveTextContent('Copied')
    expect(region).toHaveClass('sr-only')
    expect(screen.getByTestId('copy-button-success')).toBeInTheDocument()
    expect(onCopied).toHaveBeenCalledTimes(1)
  })

  it('resolves a function value before writing', async () => {
    render(<CopyButton value={() => 'resolved-fn-value'} />)
    await click(screen.getByRole('button', { name: 'Copy' }))
    expect(writeText).toHaveBeenCalledWith('resolved-fn-value')
  })

  it('keeps "Copied" at COPY_FEEDBACK_MS - 1 and is empty again exactly at COPY_FEEDBACK_MS', async () => {
    render(<CopyButton value="hello" />)
    await click(screen.getByRole('button', { name: 'Copy' }))
    expect(theAnnouncementRegion()).toHaveTextContent('Copied')

    act(() => vi.advanceTimersByTime(COPY_FEEDBACK_MS - 1))
    expect(theAnnouncementRegion()).toHaveTextContent('Copied')
    expect(screen.getByTestId('copy-button-success')).toBeInTheDocument()

    act(() => vi.advanceTimersByTime(1))
    expect(theAnnouncementRegion()).toBeEmptyDOMElement()
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
    // 1500ms after the SECOND click the announcement must still be showing
    // (the restart re-arms the timer at 2000ms from THIS click).
    act(() => vi.advanceTimersByTime(1500))
    expect(theAnnouncementRegion()).toHaveTextContent('Copied')
    // 2000ms after the second click the region is empty again.
    act(() => vi.advanceTimersByTime(500))
    expect(theAnnouncementRegion()).toBeEmptyDOMElement()
  })
})

describe('CopyButton — failure flows, no silent failure (§2.8)', () => {
  it('a clipboard rejection announces "Copy failed" through the single region and hands the error to onCopyError', async () => {
    const failure = new Error('write rejected')
    writeText.mockImplementation(() => Promise.reject(failure))
    const onCopyError = vi.fn()
    render(<CopyButton value="hello" onCopyError={onCopyError} />)
    await click(screen.getByRole('button', { name: 'Copy' }))
    expect(theAnnouncementRegion()).toHaveTextContent('Copy failed')
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
    expect(theAnnouncementRegion()).toHaveTextContent('Copy failed')
    expect(onCopyError).toHaveBeenCalledTimes(1)
    expect(writeText).not.toHaveBeenCalled()
  })

  it('failedLabel renames the failure announcement', async () => {
    writeText.mockImplementation(() => Promise.reject(new Error('no')))
    render(<CopyButton value="hello" failedLabel="Could not copy" />)
    await click(screen.getByRole('button', { name: 'Copy' }))
    expect(theAnnouncementRegion()).toHaveTextContent('Could not copy')
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
