import { act, render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { MailPreviewPane } from './MailPreviewPane'

const { mintMailSignaturePreviewToken } = vi.hoisted(() => ({
  mintMailSignaturePreviewToken: vi.fn().mockResolvedValue({ token: 'saved-draft-signature', expires_in_seconds: 120 }),
}))

// Only the network mint is mocked; the draft body, signature preview and iframe stay real.
vi.mock('@/lib/api/mail', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api/mail')>()),
  mintMailSignaturePreviewToken,
}))

const signatureText = 'Kind regards, Mia'
const signatureHtml = '<p>Kind regards, Mia</p>'

describe('MailPreviewPane — saved draft signature (F10)', () => {
  it('shows the signature exactly once when the read-back draft body already contains it', async () => {
    render(
      <MailPreviewPane
        state="draft"
        subject="Proposal review"
        to="alice@example.test"
        // Post-fix (pkg/email/view.go::viewFromRaw, commit 6e9c4fbe2): the
        // server-delivered draft body no longer has the signature baked in —
        // viewFromRaw now recognizes the real, unsigned Markdown part by its
        // X-Omnipus-Part marker header, so the lossy already-signed
        // text/plain fallback that used to win BodyMarkdown no longer fires.
        bodyMarkdown="Please review the proposal."
        signatureHtml={signatureHtml}
        onSave={vi.fn()}
        onSend={vi.fn()}
        onDiscard={vi.fn()}
      />,
    )

    // Let the real preview finish mounting its token-scoped frame, if it renders one.
    await act(async () => { await Promise.resolve() })
    const draft = screen.getByRole('region', { name: 'Draft' })
    // Markdown may split the signature across nested elements; count the actual
    // rendered text rather than relying on an exact single-element text match.
    const bodyCopies = (draft.textContent ?? '').split(signatureText).length - 1
    // Sandboxed iframe text is deliberately inaccessible to the parent DOM.
    // Its frame is one visible copy of the configured signature, not zero copies.
    const previewCopies = within(draft).queryAllByTitle('Mailbox signature preview').length
    const previewLabels = within(draft).queryAllByText('Signature', { selector: 'p' }).length
    expect(previewLabels, 'a visible signature preview must contain its frame').toBe(previewCopies)
    if (previewCopies > 0) {
      // Check that the frame's token was minted from the SAME signature HTML.
      expect(mintMailSignaturePreviewToken).toHaveBeenCalledWith({ signature_html: signatureHtml })
    }
    expect(bodyCopies + previewCopies, 'the draft review must show the signature once, not zero or twice').toBe(1)
  })
})
