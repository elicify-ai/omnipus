// Storage failure is not "unavailable". The card's headline for
// storage_failed is exactly "Page previews could not be saved". Every other
// reason keeps "Page previews unavailable — <reason>".

import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { MediaAttachment } from '@/store/chat'
import type { MediaPreview } from '@/lib/api/generated/asyncapi-types'
import { PreviewUnavailableCard } from './PreviewUnavailableCard'

function marker(reason: NonNullable<MediaPreview['unavailable_reason']>): MediaAttachment {
  return {
    type: 'image',
    url: '',
    filename: 'report.docx',
    contentType: '',
    toolCallId: 'call-storage',
    presentation: 'page_thumbnails',
    unavailableReason: reason,
  }
}

function renderCard(reason: NonNullable<MediaPreview['unavailable_reason']>) {
  return render(
    <PreviewUnavailableCard
      sessionId="sess-gatefix"
      toolCallId="call-storage"
      sourceName="report.docx"
      reason={reason}
      marker={marker(reason)}
    />,
  )
}

describe('PreviewUnavailableCard storage_failed headline', () => {
  it('says the pages could not be saved, with no unavailable prefix', () => {
    renderCard('storage_failed')
    expect(screen.getByText('Page previews could not be saved')).toBeTruthy()
    expect(screen.queryByText(/Page previews unavailable/)).toBeNull()
  })

  it('keeps the unavailable prefix for every other reason', () => {
    const headlines: Record<Exclude<NonNullable<MediaPreview['unavailable_reason']>, 'storage_failed'>, string> = {
      renderer_missing: 'Page previews unavailable — the PDF renderer is not installed',
      renderer_damaged: 'Page previews unavailable — the PDF renderer is damaged',
      renderer_failed: 'Page previews unavailable — rendering the pages failed',
    }
    for (const reason of Object.keys(headlines) as Array<keyof typeof headlines>) {
      const view = renderCard(reason)
      expect(screen.getByText(headlines[reason])).toBeTruthy()
      view.unmount()
    }
  })
})
