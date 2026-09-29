// RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-032 —
// the move_incomplete error must show a Retry action calling retry-move.
// FR-VA-036 explicitly says trash_incomplete has NO pending id or Retry.
//
// FINDING: the generated endpoint and types exist, but no frontend runtime
// consumes them. A fresh positive-control search finds moveLibraryEntry in
// src/lib/api/library.ts and LibraryExplorer.tsx; the same non-generated
// src/ search for retry-move, retryMove and pending_move_id returns zero hits.
//
//   - No API client function calls POST /library/{workspace_id}/retry-move
//     (grep confirms no such call site — only the generated request/
//     response TYPES exist, never a function wrapping the endpoint, unlike
//     e.g. moveLibraryEntry/copyLibraryEntry for the transfer endpoints).
//   - LibraryTransferDialog's error banner (`error?: string`,
//     LibraryErrorBanner) has no action-button slot at all today — it
//     renders a single message string, confirmed by direct read
//     (src/components/library/LibraryTransferDialog.tsx), so there is
//     nowhere for a "Retry" button to attach even if a retry-move client
//     function existed.
//   - getLibraryErrorMessage (src/components/library/
//     libraryErrorMessage.ts) never reads `pending_move_id` off the parsed
//     body, so the id a Retry action would need to call retry-move with is
//     also dropped today (same root cause as this file's sibling test,
//     libraryErrorMessage.viewTrackedRefusal.red.test.ts).
//
// BLOCKED per the qa-lead RED protocol: a spec element the dispatch cites
// (a frontend Retry action calling retry-move) that does not exist
// anywhere in the codebase — not the API client function, not a UI action
// slot to mount it in, not the pending_move_id plumbing it would need.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	npx vitest run src/components/library/LibraryTransferDialog.retryMoveAction.red.test.ts
import { describe, it } from 'vitest'

describe('Library transfer "incomplete" error — Retry action calling retry-move (FR-VA-032)', () => {
  it('is BLOCKED: no retry-move API client function, no error-banner action slot, and no pending_move_id plumbing exist anywhere in src/', () => {
    throw new Error(
      'BLOCKED: FR-VA-032 requires a move_incomplete error to show a Retry action ' +
        'calling retry-move; FR-VA-036 trash_incomplete must not invoke Retry. No frontend function calls ' +
        'POST /library/{workspace_id}/retry-move (only the generated request/response TYPES exist, ' +
        'per this file\'s header grep evidence), LibraryTransferDialog\'s error banner has no action-' +
        'button slot at all (single message string only), and getLibraryErrorMessage never reads ' +
        'pending_move_id off the error body. All three seams are missing.',
    )
  })
})
