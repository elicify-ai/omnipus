// Office-preview spec §5.2 and §5.3 Retry states; docs/documents.md
// "When some or all page images are missing" and "After you select Retry".
//
// PreviewRetry.uat.test.tsx already pins storage_failed beside live pages,
// reload dropping an unsaved batch, replay of a saved middle call, and a
// later batch replacing rather than accumulating. This file does not repeat
// those. It drives the card: keyboard and pointer, the in-progress sentence,
// one request while that sentence is up, and the documented failure sentences.
// A saved success is the new media frame the spec says arrives later; the
// group is re-rendered with that frame.

import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError } from '@/lib/api-error'
import type { MediaPreview } from '@/lib/api/generated/asyncapi-types'
import type { MediaAttachment } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { PreviewThumbnailGroup } from './PreviewThumbnailGroup'
import { PreviewUnavailableCard } from './PreviewUnavailableCard'
import type { PreviewGroup } from './previewGroups'

const SESSION = 'sess-1911'
const CALL = 'call-retry'
const RETRYING = 'Retrying — rendering the pages again…'
const PIXEL = 'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7'

const retrySessionPreview = vi.hoisted(() => vi.fn())

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, retrySessionPreview }
})

type Reason = NonNullable<MediaPreview['unavailable_reason']>

function marker(reason: Reason): MediaAttachment {
  return {
    type: 'image',
    url: '',
    filename: 'report.docx',
    contentType: '',
    toolCallId: CALL,
    presentation: 'page_thumbnails',
    unavailableReason: reason,
  }
}

function renderCard(reason: Reason) {
  return render(
    <PreviewUnavailableCard
      sessionId={SESSION}
      toolCallId={CALL}
      sourceName="report.docx"
      reason={reason}
      marker={marker(reason)}
    />,
  )
}

function hangRetry() {
  let resolveRetry: (value: { status: 'started' }) => void = () => {
    throw new Error('retry resolve was not installed')
  }
  retrySessionPreview.mockImplementation(
    () => new Promise((resolve) => {
      resolveRetry = resolve
    }),
  )
  return () => resolveRetry({ status: 'started' })
}

async function finishStart(resolveRetry: () => void) {
  await act(async () => {
    resolveRetry()
  })
}

// Only the failure tests install this. A passing Retry must not hide warnings.
let retryWarn: ReturnType<typeof vi.spyOn> | undefined

function watchRetryWarning() {
  retryWarn = vi.spyOn(console, 'warn').mockImplementation(() => {})
}

function expectRetryFailureWasReported(times: number) {
  expect(retryWarn).toHaveBeenCalledTimes(times)
  expect(retryWarn).toHaveBeenCalledWith(
    '[PreviewUnavailableCard] preview retry failed',
    expect.objectContaining({ toolCallId: CALL }),
  )
}

beforeEach(() => {
  retrySessionPreview.mockReset()
  useSessionStore.setState({ activeSessionId: SESSION, activeAgentId: null, activeAgentType: null })
})

afterEach(() => {
  retryWarn?.mockRestore()
  retryWarn = undefined
})

describe('Preview unavailable card, interactive Retry (§5.3)', () => {
  it('offers Retry and an empty status while idle', () => {
    renderCard('renderer_failed')
    const card = screen.getByTestId('preview-unavailable-card')
    expect(within(card).getByRole('button', { name: 'Retry' })).toBeEnabled()
    expect(within(card).getByRole('status')).toHaveTextContent('')
    expect(retrySessionPreview).not.toHaveBeenCalled()
  })

  it('hides Retry when the renderer is not installed and shows how to install it', () => {
    renderCard('renderer_missing')
    const card = screen.getByTestId('preview-unavailable-card')
    expect(within(card).queryByRole('button', { name: 'Retry' })).toBeNull()
    expect(card).toHaveTextContent('re-run the Omnipus installer with the PDF renderer component')
    expect(card).toHaveTextContent('omnipus pdf-renderer register')
    expect(retrySessionPreview).not.toHaveBeenCalled()
  })

  it('Enter starts one retry, and a second Enter while it is still running does not start another', async () => {
    const user = userEvent.setup()
    const resolveRetry = hangRetry()
    renderCard('renderer_damaged')
    const button = screen.getByRole('button', { name: 'Retry' })
    button.focus()

    await user.keyboard('{Enter}')
    expect(await screen.findByText(RETRYING)).toBeInTheDocument()
    expect(retrySessionPreview).toHaveBeenCalledTimes(1)
    expect(retrySessionPreview).toHaveBeenCalledWith(SESSION, CALL)
    expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled()

    await finishStart(resolveRetry)
    expect(screen.getByText(RETRYING)).toBeInTheDocument()
    const running = screen.getByRole('button', { name: 'Retry' })
    running.focus()
    await user.keyboard('{Enter}')

    expect(retrySessionPreview).toHaveBeenCalledTimes(1)
    expect(screen.getByText(RETRYING)).toBeInTheDocument()
  })

  it('Space starts one retry, and a second Space while it is still running does not start another', async () => {
    const user = userEvent.setup()
    const resolveRetry = hangRetry()
    renderCard('renderer_failed')
    screen.getByRole('button', { name: 'Retry' }).focus()

    await user.keyboard(' ')
    expect(await screen.findByText(RETRYING)).toBeInTheDocument()
    expect(retrySessionPreview).toHaveBeenCalledTimes(1)

    await finishStart(resolveRetry)
    screen.getByRole('button', { name: 'Retry' }).focus()
    await user.keyboard(' ')

    expect(retrySessionPreview).toHaveBeenCalledTimes(1)
  })

  it('a click starts one retry, and a second click while it is still running does not start another', async () => {
    const user = userEvent.setup()
    const resolveRetry = hangRetry()
    renderCard('renderer_failed')

    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText(RETRYING)).toBeInTheDocument()
    expect(retrySessionPreview).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(retrySessionPreview).toHaveBeenCalledTimes(1)

    await finishStart(resolveRetry)
    expect(screen.getByText(RETRYING)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(retrySessionPreview).toHaveBeenCalledTimes(1)
  })

  it('shows "The file changed — ask the agent to preview again." and removes Retry', async () => {
    const user = userEvent.setup()
    watchRetryWarning()
    retrySessionPreview.mockRejectedValue(
      new ApiError(409, 'server said something else', { code: 'preview_source_changed' }),
    )
    renderCard('renderer_failed')

    await user.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByText('The file changed — ask the agent to preview again.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull()
    expect(screen.queryByText('server said something else')).toBeNull()
    expectRetryFailureWasReported(1)
  })

  it('shows "Retry failed: the PDF renderer is not available." and a second Retry runs again', async () => {
    const user = userEvent.setup()
    watchRetryWarning()
    retrySessionPreview.mockRejectedValue(
      new ApiError(503, 'renderer down', { code: 'preview_renderer_unavailable' }),
    )
    renderCard('renderer_damaged')

    await user.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByText('Retry failed: the PDF renderer is not available.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
    expect(screen.queryByText('renderer down')).toBeNull()
    expect(retrySessionPreview).toHaveBeenCalledTimes(1)

    // docs/documents.md: "You can try again" after "Retry failed: …".
    await user.click(screen.getByRole('button', { name: 'Retry' }))

    await waitFor(() => expect(retrySessionPreview).toHaveBeenCalledTimes(2))
    expect(screen.getByText('Retry failed: the PDF renderer is not available.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
    expectRetryFailureWasReported(2)
  })

  it('shows the Library refusal and removes Retry when the file is outside the Library', async () => {
    const user = userEvent.setup()
    watchRetryWarning()
    retrySessionPreview.mockRejectedValue(
      new ApiError(403, 'nope', { code: 'preview_source_not_in_library' }),
    )
    renderCard('renderer_failed')

    await user.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByText(
      "This file is outside the workspace Library, so its previews can't be retried here. Ask the agent to preview it again.",
    )).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull()
    expectRetryFailureWasReported(1)
  })

  it('shows "This preview is no longer available." and removes Retry', async () => {
    const user = userEvent.setup()
    watchRetryWarning()
    retrySessionPreview.mockRejectedValue(new ApiError(404, 'missing preview'))
    renderCard('storage_failed')

    await user.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByText('This preview is no longer available.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull()
    expect(screen.queryByText('missing preview')).toBeNull()
    expectRetryFailureWasReported(1)
  })

  it('replaces the card with the saved page images after Retry is running', async () => {
    const user = userEvent.setup()
    const resolveRetry = hangRetry()
    const unavailable: PreviewGroup = {
      key: CALL,
      toolCallId: CALL,
      pages: [],
      unavailable: { reason: 'renderer_failed', marker: marker('renderer_failed') },
      preview: {
        source_name: 'report.docx',
        source_version: 'a1b2c3d4e5f6',
        page_count: 4,
        first_page: 1,
        last_page: 4,
        outcome: 'unavailable',
        unavailable_reason: 'renderer_failed',
      },
    }
    // One saved page. The card does not swap itself for images: "started" only
    // means the retry is running (docs: the page images replace the card when
    // saving succeeds). That replacement is a new group prop from the parent.
    // A single page is the whole frame, so hiding groups of one page would
    // leave the card up.
    const saved: PreviewGroup = {
      key: CALL,
      toolCallId: CALL,
      pages: [{
        type: 'image' as const,
        url: PIXEL,
        filename: 'page-12.png',
        contentType: 'image/gif',
        caption: 'report.docx · page 12 of 30',
        pageNumber: 12,
        toolCallId: CALL,
        presentation: 'page_thumbnails' as const,
      }],
      preview: {
        source_name: 'report.docx',
        source_version: 'a1b2c3d4e5f6',
        page_count: 30,
        first_page: 12,
        last_page: 12,
        outcome: 'complete',
      },
    }
    const view = render(<PreviewThumbnailGroup group={unavailable} />)
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText(RETRYING)).toBeInTheDocument()
    await finishStart(resolveRetry)
    await waitFor(() => expect(retrySessionPreview).toHaveBeenCalledWith(SESSION, CALL))
    expect(screen.getByTestId('preview-unavailable-card')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'report.docx · page 12 of 30' })).toBeNull()

    view.rerender(<PreviewThumbnailGroup group={saved} />)

    expect(screen.queryByTestId('preview-unavailable-card')).toBeNull()
    expect(screen.queryByText(RETRYING)).toBeNull()
    expect(screen.getAllByRole('button').map((button) => button.getAttribute('aria-label'))).toEqual([
      'report.docx · page 12 of 30',
    ])
  })
})
