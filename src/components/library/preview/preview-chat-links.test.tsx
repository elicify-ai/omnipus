/**
 * BS-1.18 / FR-001 (C2, F4): chat links are only for chat message rendering.
 * A received mail body and a Library/knowledge preview must not turn
 * omnipus://file/W/x.md or notes/x.md into a Library anchor, even when a
 * chat view has recorded a workspace (that is what makes a bare link a
 * Library link in chat).
 */
import { afterEach, describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { LibraryEntry } from '@/lib/api'
import { registerChatView } from '@/store/chatView'
import { MailPreviewPane } from '@/components/workspaces/mail/MailPreviewPane'
import { KnowledgeMarkdown } from './kbMarkdownBase'
import { LibraryMarkdownPreview } from './LibraryMarkdownPreview'

const BODY = '[a](omnipus://file/W/x.md) and [b](notes/x.md)'

// The two hrefs a chat renderer rebuilds (libraryPageHref). A note preview may
// still address the Library at a path relative to the note's own folder; that
// is today's preview handling and is not either of these.
const CHAT_LIBRARY_HREFS = [
  '/#/library?workspace=W&path=x.md',
  '/#/library?workspace=W&path=notes%2Fx.md',
]

function chatLibraryHrefs(container: HTMLElement): string[] {
  const hrefs = [...container.querySelectorAll('a')].map((anchor) => anchor.getAttribute('href') ?? '')
  return CHAT_LIBRARY_HREFS.filter((href) => hrefs.includes(href))
}

describe('previews do not render chat Library links (BS-1.18)', () => {
  let release: (() => void) | undefined
  afterEach(() => {
    release?.()
    release = undefined
  })

  it('leaves both forms alone in a received mail body (MailPreviewPane)', () => {
    release = registerChatView('sess-mail', 'W')
    const { container } = render(
      <MailPreviewPane
        state="sent"
        subject="Hello"
        bodyMarkdown={BODY}
        to="ada@example.com"
        sentOn="today"
        onSave={() => {}}
        onSend={() => {}}
        onDiscard={() => {}}
      />,
    )
    expect(chatLibraryHrefs(container)).toEqual([])
  })

  it('leaves both forms alone in the Library markdown base (kbMarkdownBase)', () => {
    release = registerChatView('sess-kb', 'W')
    const { container } = render(<KnowledgeMarkdown content={BODY} />)
    expect(chatLibraryHrefs(container)).toEqual([])
    // A relative note link keeps today's preview handling: an ordinary href,
    // not a Library page.
    expect(container.querySelector('a[href="notes/x.md"]')).not.toBeNull()
  })

  it('leaves both forms alone in the Library markdown pane (LibraryMarkdownPreview)', () => {
    release = registerChatView('sess-pane', 'W')
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const entry = { name: 'x.md', path: 'notes/x.md' } as LibraryEntry
    const { container } = render(
      <QueryClientProvider client={client}>
        <LibraryMarkdownPreview workspaceId="W" entry={entry} content={BODY} />
      </QueryClientProvider>,
    )
    expect(chatLibraryHrefs(container)).toEqual([])
  })
})
