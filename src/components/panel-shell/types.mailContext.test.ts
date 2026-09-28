import { describe, expect, expectTypeOf, it } from 'vitest'
import type { BrowserPanelContext, WorkspacePanelContext } from './types'

describe('mail panel context contract', () => {
  it('accepts only the agreed folder values and preserves opaque message references', () => {
    const context = {
      mailboxId: null,
      folder: 'sent',
      messageRef: 'opaque /?&= reference',
    } satisfies WorkspacePanelContext

    expect(context).toEqual({
      mailboxId: null,
      folder: 'sent',
      messageRef: 'opaque /?&= reference',
    })
    expectTypeOf(context.folder).toEqualTypeOf<'sent'>()

    // @ts-expect-error Mail folders are a closed product vocabulary.
    const invalid: WorkspacePanelContext = { folder: 'archive' }
    expect(invalid).toBeDefined()

    const browser: BrowserPanelContext = { sessionId: 'session-1', agentId: 'agent-1' }
    // @ts-expect-error Browser context cannot carry Mail selection state.
    browser.messageRef = 'message-1'
    expect(browser.sessionId).toBe('session-1')
  })
})
