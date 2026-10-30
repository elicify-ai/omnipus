/**
 * Regression lock (FR-005 / A7): an omnipus:// image src must not become an image
 * or a Library link. isSafeHref already rejects the scheme, so this may already
 * be green. It is not RED proof of the new file-card button.
 */
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { MarkdownImage } from './markdown-shared'

describe('MarkdownImage omnipus src', () => {
  it('does not render an image or an anchor for omnipus://', () => {
    render(<MarkdownImage src="omnipus://file/W/pic.png" alt="pic" />)
    expect(screen.queryByRole('img')).toBeNull()
    expect(screen.queryByRole('link')).toBeNull()
    expect(screen.getByText('[image: pic]')).toBeInTheDocument()
  })
})
