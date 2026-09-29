// RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-031 —
// va-qa3 dispatch, P5 ("The FR-VA-031 refusal message is shown to the
// user").
//
// Oracle: FR-VA-031's contract (contracts/components/schemas/
// LibraryMoveConflictError.yaml; confirmed by direct read of the generated
// Go/TS types, va-qa3's backend research pass) — the 409 body is
// `{error: string, code: "view_tracked_transfer_refused", tracked_paths:
// string[]}`. `tracked_paths` is the wire field that names WHICH view(s)
// blocked the transfer; a refusal that shows only a generic sentence with
// no path is not "the FR-VA-031 refusal message" — a person retrying the
// move with no idea which file is tracked cannot act on it.
//
// getLibraryErrorMessage (src/components/library/libraryErrorMessage.ts)
// is the ONE channel a Library transfer's refusal reaches the user through
// today: LibraryExplorer's transferMutation.onError calls
// `setTransferError(getLibraryErrorMessage(err, 'Transfer failed'))`, and
// LibraryTransferDialog renders that single string verbatim via
// `<LibraryErrorBanner message={error} testId="library-transfer-error" />`
// — confirmed by direct read of both files. getLibraryErrorMessage's own
// body-parsing helper, parseServerErrorField, reads ONLY `error` (or
// `message`) off the parsed JSON body — never `code`, never
// `tracked_paths` (confirmed: zero references to either key anywhere in
// libraryErrorMessage.ts). So today, even once the backend starts sending
// a real LibraryMoveConflictError body, the tracked path(s) it names are
// silently dropped before they ever reach the banner.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	npx vitest run src/components/library/libraryErrorMessage.viewTrackedRefusal.red.test.ts
import { describe, it, expect } from 'vitest'
import { ApiError } from '@/lib/api'
import { getLibraryErrorMessage } from './libraryErrorMessage'

// The exact wire shape a real handleLibraryTransfer refusal sends, per
// contracts/components/schemas/LibraryMoveConflictError.yaml and the
// generated Go/TS types (LibraryMoveConflictError: error, code,
// tracked_paths?, paths?, pending_move_id?).
function trackedTransferRefusedBody(trackedPaths: string[]): string {
  return JSON.stringify({
    error: 'a tracked view cannot be moved out of its collection',
    code: 'view_tracked_transfer_refused',
    tracked_paths: trackedPaths,
  })
}

describe('getLibraryErrorMessage — FR-VA-031 refusal names the tracked path(s)', () => {
  it('names the single tracked view path for a one-view refusal', () => {
    const err = new ApiError(409, undefined, {
      body: trackedTransferRefusedBody(['vault-a/Open.view']),
    })
    const message = getLibraryErrorMessage(err, 'Transfer failed')
    expect(
      message,
      'FR-VA-031 requires the refusal to name the tracked path — ' +
        'getLibraryErrorMessage only ever surfaces the generic `error` string field ' +
        '(parseServerErrorField reads `error`/`message` only, never `tracked_paths`), so the ' +
        'server-provided path is dropped before it reaches the user-visible banner.',
    ).toContain('vault-a/Open.view')
  })

  it('names EVERY tracked path when a folder move blocks on more than one tracked view', () => {
    const err = new ApiError(409, undefined, {
      body: trackedTransferRefusedBody(['vault-a/Sub/Open.view', 'vault-a/Sub/Closed.view']),
    })
    const message = getLibraryErrorMessage(err, 'Transfer failed')
    expect(message).toContain('vault-a/Sub/Open.view')
    expect(message).toContain('vault-a/Sub/Closed.view')
  })
})
