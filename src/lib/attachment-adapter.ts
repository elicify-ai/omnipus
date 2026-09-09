// AssistantUI AttachmentAdapter for Omnipus.
//
// This replaces the old hand-rolled upload bridge (pendingFiles +
// handleSendWithFiles + a custom paperclip + a form-submit hack in
// ChatScreen). With this adapter wired into useExternalStoreRuntime, the
// native composer flow — ComposerPrimitive.AddAttachment / .Attachments and
// ComposerPrimitive.Send — carries files identically whether the user presses
// the Send button, hits Enter, drags-drops, or pastes.
//
// Flow:
//   add()    — register a pending attachment; DO NOT upload yet (the user may
//              remove it before sending).
//   send()   — called for each attachment when the composer sends. Ensures a
//              session exists, uploads the file to /api/v1/uploads, and stashes
//              the resulting media:// ref keyed by attachment id so onNew (in
//              omnipus-runtime.ts) can thread it into our WS transport.
//   remove() — drop any stashed ref.

import type {
  AttachmentAdapter,
  CompleteAttachment,
  PendingAttachment,
} from "@assistant-ui/react";

import { createSession, uploadFiles } from "@/lib/api";
import { mediaRefURL } from "@/lib/library-attachment";
import { useSessionStore } from "@/store/session";
import { useUiStore } from "@/store/ui";
import { useWorkspacesStore } from "@/store/workspacesStore";
import { isImageAttachment } from "@/components/chat/AttachmentCard";

/** A resolved upload: the agent-facing media ref plus display metadata. */
export interface ResolvedUpload { // not-wire-format: internal client-side upload state (media ref + display metadata) drained by onNew; never serialized across the gateway/SPA boundary
  ref: string;
  url: string;
  filename: string;
  contentType: string;
}

// attachment.id -> resolved upload. Populated in send(), drained by onNew via
// takeResolvedUpload(). A Map (not message content) is used because our backend
// resolves media:// refs itself; AssistantUI's attachment content is inert here.
//
// Capped at MAX_RESOLVED_UPLOADS entries (evict oldest on overflow) to prevent
// unbounded growth when onNew never drains an entry (e.g. partial multi-file
// failure or navigating away before sending).
const MAX_RESOLVED_UPLOADS = 50;
const resolvedUploads = new Map<string, ResolvedUpload>();

/** Pull (and remove) the resolved upload for a completed attachment id. */
export function takeResolvedUpload(id: string): ResolvedUpload | undefined {
  const r = resolvedUploads.get(id);
  if (r) resolvedUploads.delete(id);
  return r;
}

/** Stash a resolved upload, evicting the oldest entry if the cap is reached. */
function stashResolvedUpload(id: string, upload: ResolvedUpload): void {
  if (resolvedUploads.size >= MAX_RESOLVED_UPLOADS) {
    // Map iteration order is insertion order — delete the first (oldest) key.
    const oldestKey = resolvedUploads.keys().next().value;
    if (oldestKey !== undefined) resolvedUploads.delete(oldestKey);
  }
  resolvedUploads.set(id, upload);
}

// Ensure a session exists before uploading. send() runs once per attachment and
// AssistantUI dispatches them concurrently, so memoize the in-flight creation to
// avoid racing multiple createSession calls.
let sessionEnsure: Promise<string> | null = null;
async function ensureSession(): Promise<string> {
  const { activeSessionId, activeAgentId, setActiveSession } = useSessionStore.getState();
  if (activeSessionId && activeSessionId !== "__pending") return activeSessionId;
  if (!sessionEnsure) {
    sessionEnsure = (async () => {
      if (!activeAgentId) throw new Error("Select an agent before sending files");
      // U2: carry the chat's workspace into the session at birth.
      //
      // The session this mints becomes the ACTIVE one, and it is the session
      // the live browser panel would attach against if the user opened it
      // before sending anything. The gateway reads which workspace's browser
      // to show off that session's own meta, server-side (ADR-075 FR-017), so
      // a session created with no workspace gets a multi-workspace agent
      // refused as ambiguous — advised to open the panel from a workspace
      // chat, which is where they already are. Waiting for the first message
      // to stamp it is exactly the gap. `undefined` outside a workspace is
      // correct: no workspace is not a default one.
      const created = await createSession(
        activeAgentId,
        useWorkspacesStore.getState().activeWorkspaceId ?? undefined,
      );
      setActiveSession(created.id, created.agent_id, null);
      return created.id;
    })().finally(() => {
      sessionEnsure = null;
    });
  }
  return sessionEnsure;
}

/** G16: Maximum file size accepted by the upload endpoint. */
export const MAX_UPLOAD_BYTES = 100 * 1024 * 1024; // 100 MB

/**
 * G15: Returns true if the file looks like an audio file.
 * Detects by MIME prefix ("audio/") or common audio extensions.
 * Audio is NOT in the ACCEPT_LIST — this check gates the tailored toast message.
 */
function isAudioFile(filename: string, mimeType: string): boolean {
  if (mimeType.toLowerCase().startsWith("audio/")) return true;
  return /\.(mp3|wav|ogg|flac|m4a|aac)$/i.test(filename);
}

// Accept images plus the document formats the backend can extract (docextract)
// or render natively (PDF). Roughly tracks docextract's supported set; note
// `.log` relies on the browser sending a text/* MIME (docextract has no .log
// extension rule), so a .log labelled application/octet-stream won't extract.
const ACCEPT_LIST = [
  "image/*",
  "application/pdf",
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  "application/vnd.openxmlformats-officedocument.presentationml.presentation",
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
  ".pdf", ".docx", ".pptx", ".xlsx", ".txt", ".md", ".csv", ".json", ".log", ".yaml", ".yml",
];

const ACCEPT = ACCEPT_LIST.join(",");

/**
 * Returns true if the file is allowed by the ACCEPT_LIST.
 * Handles three token forms:
 *   - `.ext`     → filename ends with that extension (case-insensitive)
 *   - `type/*`   → MIME starts with the prefix (wildcard, e.g. "image/")
 *   - exact MIME → MIME matches exactly (case-insensitive)
 */
export function isAcceptedFile(filename: string, mimeType: string): boolean {
  const lowerName = filename.toLowerCase();
  const lowerMime = mimeType.toLowerCase();
  for (const token of ACCEPT_LIST) {
    if (token.startsWith(".")) {
      // Extension token — case-insensitive filename suffix match.
      if (lowerName.endsWith(token.toLowerCase())) return true;
    } else if (token.endsWith("/*")) {
      // Wildcard MIME — e.g. "image/*" matches "image/png", "image/jpeg".
      const prefix = token.slice(0, -1); // "image/"
      if (lowerMime.startsWith(prefix)) return true;
    } else {
      // Exact MIME match.
      if (lowerMime === token.toLowerCase()) return true;
    }
  }
  return false;
}

let counter = 0;

// `satisfies` (not `: AttachmentAdapter`) so callers keep the concrete return
// types (add → Promise<PendingAttachment>) instead of the adapter interface's
// Promise|AsyncGenerator union, while still being checked for conformance.
export const omnipusAttachmentAdapter = {
  accept: ACCEPT,

  async add({ file }): Promise<PendingAttachment> {
    // G16: Reject files that exceed the server-side size limit before any upload
    // attempt, giving the user immediate feedback.
    if (file.size > MAX_UPLOAD_BYTES) {
      const tooLargeMsg = `"${file.name}" is too large (max 100 MB).`;
      useUiStore.getState().addToast({ message: tooLargeMsg, variant: "error" });
      throw new Error(tooLargeMsg);
    }

    // Reject unsupported types immediately so the drag-drop / paste / commitFiles
    // paths surface a user-facing error instead of silently throwing later.
    if (!isAcceptedFile(file.name, file.type)) {
      // G15: Audio files get a tailored message instead of the generic rejection.
      if (isAudioFile(file.name, file.type)) {
        const audioMsg = "Audio files aren't supported yet.";
        useUiStore.getState().addToast({ message: audioMsg, variant: "error" });
        throw new Error(audioMsg);
      }
      throw new Error(`Can't attach "${file.name}": file type not supported`);
    }

    // Defer the upload to send() — registering here only tracks the pending file.
    return {
      id: `att-${Date.now()}-${counter++}-${file.name}`,
      type: isImageAttachment(file.name, file.type) ? "image" : "file",
      name: file.name,
      contentType: file.type || "application/octet-stream",
      file,
      status: { type: "requires-action", reason: "composer-send" },
    };
  },

  async send(attachment): Promise<CompleteAttachment> {
    // CompleteAttachment with no ref stashed — used when upload/registration
    // fails so the composer doesn't throw and lose the user's typed text.
    const failedComplete: CompleteAttachment = {
      ...attachment,
      status: { type: "complete" },
      content: [],
    };

    let sessionId: string;
    try {
      sessionId = await ensureSession();
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      useUiStore.getState().addToast({ message: `Could not send "${attachment.name}": ${msg}`, variant: "error" });
      return failedComplete;
    }

    // No client-side vision pre-send warning here (ADR-068 T068-05): the SPA's
    // only model-capability source is the registry-fed catalog
    // (fetchProvidersCatalog → CatalogModel.input_modalities); the pre-send
    // check is re-added on that source in a later B5 task. The reactive
    // server-side explanation (loop_media.go) is the backstop meanwhile.

    let uploadResult: Awaited<ReturnType<typeof uploadFiles>>;
    try {
      const workspaceId = useWorkspacesStore.getState().activeWorkspaceId ?? undefined;
      uploadResult = await uploadFiles(sessionId, [attachment.file], workspaceId);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      useUiStore.getState().addToast({ message: `Upload failed for "${attachment.name}": ${msg}`, variant: "error" });
      return failedComplete;
    }

    // We pass a single-element array, so files[0] is this attachment's result.
    const uploaded = uploadResult.files[0];
    if (!uploaded || !uploaded.path) {
      useUiStore.getState().addToast({ message: `Upload failed for "${attachment.name}" — no path returned.`, variant: "error" });
      return failedComplete;
    }

    const ref = typeof uploaded.ref === "string" ? uploaded.ref : undefined;
    if (!ref) {
      // File was saved but media-store registration failed — the agent cannot
      // see it inline. Toast and return without stashing so onNew skips the ref
      // while the user's typed text still sends.
      useUiStore.getState().addToast({
        message: `"${attachment.name}" could not be registered for the agent — re-attach and try again.`,
        variant: "error",
      });
      return failedComplete;
    }

    stashResolvedUpload(attachment.id, {
      ref,
      // D16 fix: build the preview URL from the returned media ref
      // (mediaRefURL), not a hardcoded `/api/v1/uploads/${sessionId}/${name}`
      // string. A workspace-scoped upload (uploadFiles called with
      // workspaceId below) is routed into the workspace media library and
      // returns a `media://workspace/<ws>/<id>` ref — the legacy
      // uploads/{session}/{filename} path is never written for that case, so
      // the hardcoded URL 404s. mediaRefURL derives the correct route from
      // the ref itself, mirroring the Go-side mediaRefURL in
      // webchat_channel.go.
      url: mediaRefURL(ref),
      filename: attachment.name,
      contentType: uploaded.content_type,
    });

    return {
      ...attachment,
      status: { type: "complete" },
      // Inert for our transport (the backend resolves the media:// ref); a single
      // text part keeps the CompleteAttachment shape valid. NOT merged into the
      // message content (AssistantUI keeps content = typed text only).
      content: [{ type: "text", text: `[attached file: ${attachment.name}]` }],
    };
  },

  async remove(attachment): Promise<void> {
    resolvedUploads.delete(attachment.id);
  },
} satisfies AttachmentAdapter;
