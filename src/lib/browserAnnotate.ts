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
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'

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

  const uploadResult = await uploadFiles(sessionId, [file])
  const uploaded = uploadResult.files[0]
  if (!uploaded?.ref) {
    throw new Error('Upload failed — no media reference was returned.')
  }

  // Best-effort element enrichment (D-B3): a failure here — network error,
  // cross-origin frame, detached node, timeout, or a plain ok:false result —
  // must never block delivering the image + comment. Only a genuinely
  // resolved element with non-empty text is appended.
  let finalComment = comment
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
        finalComment = `${comment}\n\nElement: ${tag ? `${tag} — ${text}` : text}`
      }
    }
  } catch {
    // Best-effort — swallow and proceed with the image + comment alone.
  }

  // sendMessage always targets whatever chat is CURRENTLY active — re-check
  // right before sending (the user could have switched chats during the
  // upload/inspect round-trips above) rather than silently posting this
  // browser's media into a chat it doesn't belong to.
  const { activeSessionId, activeAgentId } = useSessionStore.getState()
  if (activeSessionId !== sessionId || activeAgentId !== agentId) {
    throw new Error("The active chat has changed — switch back to this browser's chat to send the annotation.")
  }

  useChatStore.getState().sendMessage(finalComment, {
    mediaRefs: [uploaded.ref],
    attachments: [
      {
        type: 'image',
        url: `/api/v1/uploads/${sessionId}/${uploaded.name}`,
        filename: file.name,
        contentType: uploaded.content_type,
      },
    ],
  })
}
