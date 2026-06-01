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
import { useSessionStore } from "@/store/session";

/** A resolved upload: the agent-facing media ref plus display metadata. */
export interface ResolvedUpload {
  ref: string;
  url: string;
  filename: string;
  contentType: string;
}

// attachment.id -> resolved upload. Populated in send(), drained by onNew via
// takeResolvedUpload(). A Map (not message content) is used because our backend
// resolves media:// refs itself; AssistantUI's attachment content is inert here.
const resolvedUploads = new Map<string, ResolvedUpload>();

/** Pull (and remove) the resolved upload for a completed attachment id. */
export function takeResolvedUpload(id: string): ResolvedUpload | undefined {
  const r = resolvedUploads.get(id);
  if (r) resolvedUploads.delete(id);
  return r;
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
      const created = await createSession(activeAgentId);
      setActiveSession(created.id, created.agent_id, null);
      return created.id;
    })().finally(() => {
      sessionEnsure = null;
    });
  }
  return sessionEnsure;
}

function attachmentType(mime: string): "image" | "file" {
  return mime.startsWith("image/") ? "image" : "file";
}

// Accept images plus the document formats the backend can extract (docextract)
// or render natively (PDF). Mirrors docextract's supported set.
const ACCEPT = [
  "image/*",
  "application/pdf",
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  "application/vnd.openxmlformats-officedocument.presentationml.presentation",
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
  ".pdf", ".docx", ".pptx", ".xlsx", ".txt", ".md", ".csv", ".json", ".log", ".yaml", ".yml",
].join(",");

let counter = 0;

// `satisfies` (not `: AttachmentAdapter`) so callers keep the concrete return
// types (add → Promise<PendingAttachment>) instead of the adapter interface's
// Promise|AsyncGenerator union, while still being checked for conformance.
export const omnipusAttachmentAdapter = {
  accept: ACCEPT,

  async add({ file }): Promise<PendingAttachment> {
    // Defer the upload to send() — registering here only tracks the pending file.
    return {
      id: `att-${Date.now()}-${counter++}-${file.name}`,
      type: attachmentType(file.type),
      name: file.name,
      contentType: file.type || "application/octet-stream",
      file,
      status: { type: "requires-action", reason: "composer-send" },
    };
  },

  async send(attachment): Promise<CompleteAttachment> {
    const sessionId = await ensureSession();
    const { files } = await uploadFiles(sessionId, [attachment.file]);
    const uploaded = files.find((f) => f.name === attachment.name) ?? files[0];
    if (!uploaded || uploaded.path.length === 0) {
      throw new Error(`Upload failed for "${attachment.name}"`);
    }

    const ref = typeof uploaded.ref === "string" ? uploaded.ref : "";
    if (ref) {
      resolvedUploads.set(attachment.id, {
        ref,
        url: `/api/v1/uploads/${sessionId}/${uploaded.name}`,
        filename: uploaded.name,
        contentType: uploaded.content_type,
      });
    } else {
      // Path uploaded but media-store registration failed: the agent cannot see
      // it inline. Surface rather than silently drop — throw so the composer
      // marks the attachment errored instead of sending a file the agent ignores.
      throw new Error(`"${attachment.name}" could not be registered for the agent — re-attach and try again`);
    }

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
