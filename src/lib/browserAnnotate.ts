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
}

/**
 * Uploads the annotation image, sends it (+ the comment) to the agent via
 * the active chat session, best-effort enriching the comment with the
 * inspected element's tag/text first.
 *
 * Targets whatever session/agent is CURRENTLY active in useSessionStore at
 * call time (read fresh, not passed in) — this deliberately mirrors the
 * chat store's own `sendMessage`, which always posts to the active session
 * with no override parameter. Using the same source for the upload's
 * session id and the eventual message keeps the two consistent (the
 * uploaded media:// ref belongs to the same session the message posts to).
 * This is the same acknowledged "v1 limitation" as BrowserTool.tsx's
 * handleWatchLive: if the user switches chats while the live browser panel
 * stays open, this targets the newly-active chat, not necessarily the
 * chat that originally opened the panel. Throws if there is no active
 * session/agent — callers must surface that to the user.
 */
export async function submitAnnotation({ comment, file, point }: SubmitAnnotationParams): Promise<void> {
  const { activeSessionId, activeAgentId } = useSessionStore.getState()
  if (!activeSessionId || !activeAgentId) {
    throw new Error('No active chat session — open a chat before sending an annotation.')
  }

  const uploadResult = await uploadFiles(activeSessionId, [file])
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
      session_id: activeSessionId,
      agent_id: activeAgentId,
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

  useChatStore.getState().sendMessage(finalComment, {
    mediaRefs: [uploaded.ref],
    attachments: [
      {
        type: 'image',
        url: `/api/v1/uploads/${activeSessionId}/${uploaded.name}`,
        filename: file.name,
        contentType: uploaded.content_type,
      },
    ],
  })
}
