/**
 * Follow-up 4: tel: is not a chat link and is not on the markdown allow-list.
 * isSafeHref still lists tel:, so this stays red until that entry is removed.
 * mailto stays allowed (BS-3.2). javascript stays rejected (SEC-4).
 */
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { createLinkRenderer } from '@/components/chat/markdown-shared'
import { isSafeHref } from './url-safe'

const MarkdownLink = createLinkRenderer(null)

describe('tel links (follow-up 4)', () => {
  it('does not treat tel: as a safe navigable href', () => {
    expect(isSafeHref('mailto:a@b.com')).toBe(true)
    expect(isSafeHref('javascript:alert(1)')).toBe(false)
    expect(isSafeHref('tel:+15551234567')).toBe(false)
    render(<MarkdownLink href="tel:+15551234567">call</MarkdownLink>)
    expect(document.querySelector('a[href^="tel:"]')).toBeNull()
    expect(screen.getByText('call').tagName).not.toBe('A')
  })
})
