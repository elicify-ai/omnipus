/**
 * RED contract — sandboxed mail HTML frame. Spec §16, MC-10 iframe block, MC-39, D13, D17.
 *
 * Implement src/components/workspaces/mail/MailHtmlFrame.tsx exporting MailHtmlFrame.
 * Props: { tokenUrl: string; onLoadImages: () => void }.
 * The frame uses src (never srcdoc), sandbox exactly
 * "allow-popups allow-popups-to-escape-sandbox", referrerPolicy no-referrer, allow="".
 * "Load images" calls onLoadImages (the parent re-mints with load_remote).
 */
import React from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'

const BANNED = [
  'allow-scripts', 'allow-same-origin', 'allow-forms', 'allow-top-navigation',
  'allow-downloads', 'allow-modals', 'allow-pointer-lock',
]

async function loadFrame(): Promise<React.ComponentType<{ tokenUrl: string; onLoadImages: () => void }>> {
  const specifier = './' + 'MailHtmlFrame'
  try {
    const mod = await import(/* @vite-ignore */ specifier) as {
      MailHtmlFrame?: React.ComponentType<{ tokenUrl: string; onLoadImages: () => void }>
    }
    if (typeof mod.MailHtmlFrame !== 'function') throw new Error('MailHtmlFrame is not a function export')
    return mod.MailHtmlFrame
  } catch (err) {
    const detail = err instanceof Error ? err.message : String(err)
    if (detail.startsWith('BLOCKED:')) throw err
    throw new Error('BLOCKED: MailHtmlFrame not implemented — required by spec §16 / MC-10. ' + detail)
  }
}

describe('Mail HTML frame (MC-10, MC-39, D17)', () => {
  it('mirrors the normative iframe attribute token for token', async () => {
    const MailHtmlFrame = await loadFrame()
    render(<MailHtmlFrame tokenUrl="http://mail.example.test/mail-preview/html/abc" onLoadImages={vi.fn()} />)
    const frame = screen.getByTitle(/mail/i)
    expect(frame.tagName).toBe('IFRAME')
    expect(frame).toHaveAttribute('src', 'http://mail.example.test/mail-preview/html/abc')
    expect(frame).not.toHaveAttribute('srcdoc')
    expect(frame).toHaveAttribute('sandbox', 'allow-popups allow-popups-to-escape-sandbox')
    expect(frame).toHaveAttribute('referrerPolicy', 'no-referrer')
    expect(frame.getAttribute('allow')).toBe('')
    const sandbox = frame.getAttribute('sandbox') ?? ''
    for (const token of BANNED) {
      expect(sandbox, token).not.toContain(token)
    }
  })

  it('asks to load images only when the human clicks (D17)', async () => {
    const MailHtmlFrame = await loadFrame()
    const onLoadImages = vi.fn()
    render(<MailHtmlFrame tokenUrl="http://mail.example.test/mail-preview/html/abc" onLoadImages={onLoadImages} />)
    expect(onLoadImages).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: /load images/i }))
    expect(onLoadImages).toHaveBeenCalledTimes(1)
  })
})
