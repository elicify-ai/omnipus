// Tests for AttachmentCard, fileTypeMeta, useFilePreview, and isImageAttachment.
//
// Coverage:
//   - fileTypeMeta: all recognised extensions + MIME types + unknown fallback
//   - useFilePreview: creates an object URL for image Files, revokes on
//     unmount and on file change, returns undefined for non-image files
//   - AttachmentCard: renders <img> for images, icon+name+label card for files,
//     falls back to file-card when the <img> fails to load (onError)

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { renderHook, cleanup } from '@testing-library/react'
import {
  fileTypeMeta,
  isImageAttachment,
  useFilePreview,
  AttachmentCard,
} from './AttachmentCard'

// ── fileTypeMeta ──────────────────────────────────────────────────────────────

describe('fileTypeMeta', () => {
  it('pdf by extension', () => {
    const { label } = fileTypeMeta('report.pdf')
    expect(label).toBe('PDF')
  })

  it('pdf by MIME', () => {
    const { label } = fileTypeMeta('report', 'application/pdf')
    expect(label).toBe('PDF')
  })

  it('docx by extension', () => {
    const { label } = fileTypeMeta('doc.docx')
    expect(label).toBe('Word')
  })

  it('docx by MIME', () => {
    const { label } = fileTypeMeta('doc', 'application/vnd.openxmlformats-officedocument.wordprocessingml.document')
    expect(label).toBe('Word')
  })

  it('xlsx by extension', () => {
    const { label } = fileTypeMeta('sheet.xlsx')
    expect(label).toBe('Excel')
  })

  it('xlsx by MIME', () => {
    const { label } = fileTypeMeta('sheet', 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet')
    expect(label).toBe('Excel')
  })

  it('csv', () => {
    const { label } = fileTypeMeta('data.csv')
    expect(label).toBe('CSV')
  })

  it('pptx by extension', () => {
    const { label } = fileTypeMeta('slides.pptx')
    expect(label).toBe('Slides')
  })

  it('zip', () => {
    const { label } = fileTypeMeta('archive.zip')
    expect(label).toBe('Archive')
  })

  it('code extension (go)', () => {
    const { label } = fileTypeMeta('main.go')
    expect(label).toBe('GO')
  })

  it('code extension (ts)', () => {
    const { label } = fileTypeMeta('app.ts')
    expect(label).toBe('TS')
  })

  it('audio by extension', () => {
    const { label } = fileTypeMeta('track.mp3')
    expect(label).toBe('Audio')
  })

  it('audio by MIME', () => {
    const { label } = fileTypeMeta('track', 'audio/ogg')
    expect(label).toBe('Audio')
  })

  it('video by extension', () => {
    const { label } = fileTypeMeta('clip.mp4')
    expect(label).toBe('Video')
  })

  it('video by MIME', () => {
    const { label } = fileTypeMeta('clip', 'video/webm')
    expect(label).toBe('Video')
  })

  it('image by extension', () => {
    const { label } = fileTypeMeta('photo.png')
    expect(label).toBe('Image')
  })

  it('image by MIME', () => {
    const { label } = fileTypeMeta('photo', 'image/jpeg')
    expect(label).toBe('Image')
  })

  it('txt', () => {
    const { label } = fileTypeMeta('readme.txt')
    expect(label).toBe('TXT')
  })

  it('unknown extension uses uppercased extension as label', () => {
    const { label } = fileTypeMeta('data.xyz')
    expect(label).toBe('XYZ')
  })

  it('unknown with no extension falls back to "File"', () => {
    const { label } = fileTypeMeta('noext')
    expect(label).toBe('File')
  })

  it('all recognised entries return a non-null Icon', () => {
    const cases: Array<[string, string?]> = [
      ['f.pdf'],
      ['f.docx'],
      ['f.xlsx'],
      ['f.csv'],
      ['f.pptx'],
      ['f.zip'],
      ['f.py'],
      ['f.mp3'],
      ['f.mp4'],
      ['f.png'],
      ['f.txt'],
    ]
    for (const [filename, mime] of cases) {
      const { Icon } = fileTypeMeta(filename, mime)
      expect(Icon, `Icon for ${filename}`).toBeTruthy()
    }
  })
})

// ── isImageAttachment ─────────────────────────────────────────────────────────

describe('isImageAttachment', () => {
  it('returns true for image MIME type', () => {
    expect(isImageAttachment('photo', 'image/jpeg')).toBe(true)
  })

  it('returns true for .png extension', () => {
    expect(isImageAttachment('photo.png')).toBe(true)
  })

  it('returns true for .webp extension', () => {
    expect(isImageAttachment('photo.webp')).toBe(true)
  })

  it('returns false for pdf', () => {
    expect(isImageAttachment('doc.pdf', 'application/pdf')).toBe(false)
  })

  it('returns false for no extension and no MIME', () => {
    expect(isImageAttachment('noext')).toBe(false)
  })
})

// ── useFilePreview ────────────────────────────────────────────────────────────

describe('useFilePreview', () => {
  const fakeObjectUrl = 'blob:http://localhost/fake-object-url'
  let createObjectUrlSpy: ReturnType<typeof vi.spyOn>
  let revokeObjectUrlSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    createObjectUrlSpy = vi
      .spyOn(URL, 'createObjectURL')
      .mockReturnValue(fakeObjectUrl)
    revokeObjectUrlSpy = vi
      .spyOn(URL, 'revokeObjectURL')
      .mockImplementation(() => {})
  })

  afterEach(() => {
    createObjectUrlSpy.mockRestore()
    revokeObjectUrlSpy.mockRestore()
    cleanup()
  })

  it('creates an object URL for an image File and returns it', () => {
    const file = new File(['data'], 'photo.png', { type: 'image/png' })
    const { result } = renderHook(() => useFilePreview(file))
    expect(result.current).toBe(fakeObjectUrl)
    expect(createObjectUrlSpy).toHaveBeenCalledWith(file)
  })

  it('revokes the object URL on unmount', () => {
    const file = new File(['data'], 'photo.png', { type: 'image/png' })
    const { unmount } = renderHook(() => useFilePreview(file))
    expect(revokeObjectUrlSpy).not.toHaveBeenCalled()
    unmount()
    expect(revokeObjectUrlSpy).toHaveBeenCalledWith(fakeObjectUrl)
  })

  it('revokes the old URL and creates a new one when file changes', () => {
    const file1 = new File(['a'], 'img1.png', { type: 'image/png' })
    const file2 = new File(['b'], 'img2.jpg', { type: 'image/jpeg' })
    const secondUrl = 'blob:http://localhost/second'
    createObjectUrlSpy
      .mockReturnValueOnce(fakeObjectUrl)
      .mockReturnValueOnce(secondUrl)

    const { result, rerender } = renderHook(({ file }: { file: File }) => useFilePreview(file), {
      initialProps: { file: file1 },
    })

    expect(result.current).toBe(fakeObjectUrl)

    rerender({ file: file2 })

    // Old URL must be revoked before the new one is created.
    expect(revokeObjectUrlSpy).toHaveBeenCalledWith(fakeObjectUrl)
    expect(result.current).toBe(secondUrl)
  })

  it('returns undefined for a non-image File', () => {
    const file = new File(['data'], 'doc.pdf', { type: 'application/pdf' })
    const { result } = renderHook(() => useFilePreview(file))
    expect(result.current).toBeUndefined()
    expect(createObjectUrlSpy).not.toHaveBeenCalled()
  })

  it('returns undefined when file is undefined', () => {
    const { result } = renderHook(() => useFilePreview(undefined))
    expect(result.current).toBeUndefined()
  })
})

// ── AttachmentCard ────────────────────────────────────────────────────────────

describe('AttachmentCard', () => {
  it('renders an <img> for an image attachment', () => {
    render(
      <AttachmentCard
        filename="photo.png"
        contentType="image/png"
        imageUrl="http://example.com/photo.png"
      />,
    )
    const img = screen.getByRole('img')
    expect(img).toBeInTheDocument()
    expect(img).toHaveAttribute('src', 'http://example.com/photo.png')
    expect(img).toHaveAttribute('alt', 'photo.png')
  })

  it('renders the file-card (icon + name + label) for a non-image file', () => {
    render(<AttachmentCard filename="report.pdf" contentType="application/pdf" />)
    // No <img>
    expect(screen.queryByRole('img')).not.toBeInTheDocument()
    // Filename is present
    expect(screen.getByText('report.pdf')).toBeInTheDocument()
    // Type label is present
    expect(screen.getByText('PDF')).toBeInTheDocument()
  })

  it('falls back to the file-card when the image fails to load (onError)', () => {
    render(
      <AttachmentCard
        filename="photo.png"
        contentType="image/png"
        imageUrl="http://broken.example.com/photo.png"
      />,
    )

    const img = screen.getByRole('img')
    expect(img).toBeInTheDocument()

    // Simulate a broken image load.
    act(() => {
      fireEvent.error(img)
    })

    // After the error, the <img> should be gone and the file-card should appear.
    expect(screen.queryByRole('img')).not.toBeInTheDocument()
    expect(screen.getByText('photo.png')).toBeInTheDocument()
    // Image files fall under the "Image" label in fileTypeMeta.
    expect(screen.getByText('Image')).toBeInTheDocument()
  })

  it('renders the remove button when provided', () => {
    render(
      <AttachmentCard
        filename="doc.pdf"
        removeButton={<button type="button" data-testid="remove-btn">Remove</button>}
      />,
    )
    expect(screen.getByTestId('remove-btn')).toBeInTheDocument()
  })

  it('shows filename in the title attribute for accessibility', () => {
    const { container } = render(<AttachmentCard filename="data.zip" />)
    const card = container.querySelector('[title="data.zip"]')
    expect(card).toBeInTheDocument()
  })

  it('G14 — isImage=true renders <img> even when contentType is blank and filename has no image extension', () => {
    // When the caller passes isImage=true, the internal isImageAttachment re-check is
    // bypassed. Even with contentType='' and a filename like 'upload' (no extension),
    // the thumbnail must render as an <img>.
    render(
      <AttachmentCard
        filename="upload"
        contentType=""
        imageUrl="http://example.com/upload"
        isImage={true}
      />,
    )
    const img = screen.getByRole('img')
    expect(img).toBeInTheDocument()
    expect(img).toHaveAttribute('src', 'http://example.com/upload')
  })

  it('G14 — isImage=false skips the thumbnail even for an image filename', () => {
    // When the caller explicitly passes isImage=false, the file-card renders even
    // though the filename would otherwise classify as an image.
    render(
      <AttachmentCard
        filename="photo.png"
        contentType="image/png"
        imageUrl="http://example.com/photo.png"
        isImage={false}
      />,
    )
    expect(screen.queryByRole('img')).not.toBeInTheDocument()
    expect(screen.getByText('photo.png')).toBeInTheDocument()
  })
})
