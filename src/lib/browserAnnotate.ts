// browserAnnotate — orchestrates the annotate-a-region-and-discuss submit
// flow (ADR-039 D-B1/B2/B3): upload the cropped-region image, best-effort
// enrich the comment with the element text/tag at that point, then send the
// image+comment to the agent over the EXISTING upload → media:// →
// resolveMediaRefs → provider-vision pipeline (zero new agent/vision code).
//
// Deliberately a plain function (not inline in BrowserLiveView) so the
// upload/inspect/send sequencing — including the best-effort inspect
// fallback — is unit-testable without mounting a component or touching
// canvas/Image (which jsdom cannot rasterise; see media-actions.test.ts).
//
// BrowserLivePanel is mounted OUTSIDE the AssistantRuntimeProvider (see
// AppShell.tsx), so `composerRuntime.addAttachment()` is not reachable from
// here — this calls `uploadFiles` + the chat store's `sendMessage` directly,
// exactly like the composer's own AttachmentAdapter does internally
// (src/lib/attachment-adapter.ts), just without needing the runtime.

import { uploadFiles, inspectBrowserElement } from '@/lib/api'
import { mediaRefURL } from '@/lib/library-attachment'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'

export interface SubmitAnnotationParams { // not-wire-format: local params for the annotate-submit orchestration, never serialized across the gateway/SPA boundary
  /** User's typed comment. Must be non-empty (caller validates before calling). */
  comment: string
  /** The cropped-region PNG, already built by the caller (canvas.toBlob → File). */
  file: File
  /** Device (CSS) pixel point — center of the annotated region — for the best-effort inspect call (ADR-039 D-B3). */
  point: { x: number; y: number }
  /** The live-browser panel's PINNED session/agent (BrowserLiveView's own
   *  props) — NOT re-read from useSessionStore, which reflects whatever
   *  chat is CURRENTLY active and may have changed since the panel was
   *  opened. Passed explicitly so the upload + inspect calls always target
   *  the browser being annotated. */
  sessionId: string
  agentId: string
}

/** Thrown when a send is attempted while the target session already has a
 *  turn streaming — `useChatStore.sendMessage` silently no-ops in that case
 *  (banner only, no send), so this must be checked BEFORE uploading/sending
 *  rather than letting the image upload and the message vanish together. */
export class AnnotationBusyError extends Error {}

/**
 * Uploads the annotation image, sends it (+ the comment) to the agent via
 * `sessionId`/`agentId` — the live-browser panel's pinned pair, passed by
 * the caller (BrowserLiveView already has them as props) rather than
 * re-read from useSessionStore — best-effort enriching the comment with
 * the inspected element's tag/text first.
 *
 * `useChatStore.sendMessage` itself has no session-override parameter: it
 * always posts to whatever chat is CURRENTLY active (this is the same
 * acknowledged "v1 limitation" as BrowserTool.tsx's handleWatchLive).
 * Sending anyway when the active chat has drifted away from `sessionId`
 * would silently attach media uploaded under one session to a DIFFERENT
 * session's transcript — so this refuses (throws) rather than misdirecting
 * when the two disagree at send time; the caller surfaces that to the user.
 *
 * Also refuses (throws AnnotationBusyError) when the target session already
 * has a turn in flight — sendMessage would otherwise silently no-op (banner
 * only) after the image has already been uploaded, producing a false
 * "Annotation sent" success.
 *
 * Throws if `sessionId`/`agentId` are empty — callers must surface that to
 * the user.
 */
export async function submitAnnotation({ comment, file, point, sessionId, agentId }: SubmitAnnotationParams): Promise<void> {
  if (!sessionId || !agentId) {
    throw new Error('No active chat session — open a chat before sending an annotation.')
  }
  if (useChatStore.getState().isStreaming) {
    throw new AnnotationBusyError('The agent is busy — wait for it to finish, then send.')
  }

  // The D18 client-side vision pre-send warning was removed with the
  // GET /providers/model-capabilities endpoint (ADR-067). It is re-sourced
  // from GET /providers/catalog in the ADR-068 B5 / T067-13 SPA slice; the
  // reactive server-side explanation (loop_media.go) remains the backstop.

  const uploadResult = await uploadFiles(sessionId, [file], useWorkspacesStore.getState().activeWorkspaceId ?? undefined)
  const uploaded = uploadResult.files[0]
  if (!uploaded?.ref) {
    throw new Error('Upload failed — no media reference was returned.')
  }

  // Best-effort element enrichment (D-B3): a failure here — network error,
  // cross-origin frame, detached node, timeout, or a plain ok:false result —
  // must never block delivering the image + comment. Only a genuinely
  // resolved element with non-empty text contributes the extra context
  // clause folded into the framing note below.
  let autoContext = ''
  try {
    const inspectResult = await inspectBrowserElement({
      session_id: sessionId,
      agent_id: agentId,
      x: point.x,
      y: point.y,
    })
    if (inspectResult.ok) {
      const tag = inspectResult.tag?.trim()
      const text = inspectResult.text?.trim()
      if (text) {
        // UAT finding: the previous raw `Element: h1 — <text>` suffix read as
        // authoritative — the agent quoted the whole label (prefix included) as
        // "the exact text in the region" instead of reading the image. Framed
        // explicitly as auto-detected *context* below, folded into the SAME
        // framing block as every send (see the blank-region note below)
        // rather than its own separate bracket, and capped in length so a
        // large element doesn't dump a wall of text into the chat comment.
        const snippet = text.length > 280 ? `${text.slice(0, 280)}…` : text
        autoContext = ` Auto-detected context for this region${tag ? ` (<${tag}>)` : ''}: "${snippet}".`
      }
    }
  } catch (err) {
    // Best-effort — proceed with the image + comment alone, but trace it so
    // a systemic failure (e.g. contract drift on BrowserInspectRequest/
    // Response) doesn't degrade silently with zero diagnostic signal.
    console.debug('[browser] inspect enrichment failed:', err)
  }

  // UAT finding (Tester 2, blank-region false negative): a low-content crop
  // (a mostly-blank background/whitespace region) paired with a short
  // comment and no auto-detected element text left the model with nothing
  // but a bare image attachment — it sometimes replied "I don't see an
  // attached image" instead of describing the (genuinely sparse) region.
  // Every send now carries this short, constant framing note — appended
  // AFTER the user's own comment (never prepended/rewritten) — telling the
  // model an image WAS sent, IS a cropped screenshot region, and MAY be
  // mostly blank on purpose, so it should describe what it actually sees
  // rather than deny the attachment.
  //
  // Backend-investigation fix (live gateway-log proof, 2026-07-28): the
  // server can reject/strip the image before it ever reaches the model
  // (loop.go's outcome_fallback downgrade path logs "provider rejected
  // media input — retrying with downgraded media block") and replaces it
  // with its own honest "[attachment unavailable: ...]" marker
  // (media_downgrade.go). The client sends this note at upload time and
  // genuinely cannot know whether the image will survive that later,
  // server-side step — so the note must NOT assert the image arrived (the
  // old wording's "The attached image is the source of truth" was false
  // whenever the server had to strip it, and directly contradicted the
  // server's own marker in the very same message, producing a confused
  // model response). The wording below stays true in BOTH outcomes: it
  // only claims an image was sent, and defers to whichever signal the
  // model actually observes (the image itself, or the server's
  // unavailable-attachment note) rather than predicting which one wins.
  const finalComment = `${comment}\n\n[This is a cropped screenshot region from the live browser — it may be mostly blank.${autoContext} An image was sent with this message. If you can see it, treat it as the source of truth and describe exactly what you see rather than saying no image was attached. If instead this message contains a note that the attachment is unavailable, trust that note instead.]`

  // sendMessage always targets whatever chat is CURRENTLY active — re-check
  // right before sending (the user could have switched chats during the
  // upload/inspect round-trips above) rather than silently posting this
  // browser's media into a chat it doesn't belong to.
  const { activeSessionId, activeAgentId } = useSessionStore.getState()
  if (activeSessionId !== sessionId || activeAgentId !== agentId) {
    throw new Error("The active chat has changed — switch back to this browser's chat to send the annotation.")
  }

  // D16 fix: build the preview URL from the returned media ref (mediaRefURL),
  // not a hardcoded `/api/v1/uploads/${sessionId}/${uploaded.name}` string.
  // When this panel is opened from a workspace-scoped chat, uploadFiles()
  // above routes the file into the workspace media library (HandleUpload,
  // pkg/gateway/rest.go) and returns a `media://workspace/<ws>/<id>` ref —
  // the legacy uploads/{session}/{filename} path is never written for that
  // case, so the hardcoded URL 404s (HandleServeUpload only resolves
  // uploads/<session>/<file>). mediaRefURL derives the correct route
  // (/api/v1/media/workspace/<ws>/<id> or /api/v1/media/<id>) from the ref
  // itself, mirroring the Go-side mediaRefURL in webchat_channel.go.
  useChatStore.getState().sendMessage(finalComment, {
    mediaRefs: [uploaded.ref],
    attachments: [
      {
        type: 'image',
        url: mediaRefURL(uploaded.ref),
        filename: file.name,
        contentType: uploaded.content_type,
      },
    ],
  })
}
