// libraryPreviewVariant.ts — the one inline/pane layout switch shared by every
// renderer that mounts inside both the full-screen Library preview pane AND
// an inline embed inside a knowledge-base note (ADR-083 spec, EMB-027/028).
//
// EMB-027: "There MUST be exactly one renderer per kind, shared between the
// full-screen pane and the inline embed. No inline-only copy may be created."
// EMB-028: "A renderer's layout variant MUST change layout only — height,
// overflow, and whether chrome is shown. It MUST NOT change what is fetched,
// which states exist, or how any non-happy state is rendered."
//
// So `variant` is the ONLY prop that may vary a renderer's container classes.
// Every fetch, every state, every non-happy render stays byte-for-byte the
// same regardless of which value this takes — see each renderer's own
// `*.variant.test.tsx` for the cross-variant assertion that proves it.

export type LibraryPreviewVariant = 'pane' | 'inline'

/**
 * Shared height bound for an inline embed whose content can run arbitrarily
 * long (a PDF's pages, a saved view's rows) — a fixed, self-determined box
 * with its own internal scroll, so the embed sits in the note's text flow
 * instead of trying to fill an ancestor's height the way `h-full`/`flex-1`
 * do. `min-h-0` is required alongside it so the flex children inside CAN
 * shrink to scroll rather than overflowing the box.
 *
 * Deliberately NOT used by the image renderer — a picture is content that
 * should size to its own aspect ratio, not be boxed into a fixed height.
 */
export const INLINE_PREVIEW_BOX_CLASS = 'h-[28rem] max-h-[70vh] min-h-0'
