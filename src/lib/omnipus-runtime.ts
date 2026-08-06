// AssistantUI runtime adapter — bridges the Zustand chat store + WebSocket protocol
// to useExternalStoreRuntime so AssistantUI primitives can render our messages.

import { useExternalStoreRuntime } from "@assistant-ui/react";
import type { ThreadMessageLike, AppendMessage } from "@assistant-ui/react";
import { useChatStore } from "@/store/chat";
import type { ChatMessage, MediaAttachment } from "@/store/chat";
import type { AssistantMessage, ToolCall } from "@/lib/api";
import { useUiStore } from "@/store/ui";
import { omnipusAttachmentAdapter, takeResolvedUpload } from "@/lib/attachment-adapter";
import { takeLibraryAttachments, mediaRefURL } from "@/lib/library-attachment";
import { isImageAttachment } from "@/components/chat/AttachmentCard";

type StoreToolCall = ToolCall & { call_id: string }; // not-wire-format: internal Zustand store type enriching ToolCall with a required call_id; never emitted to the backend

// ── Message conversion ────────────────────────────────────────────────────────

/**
 * Push text + history tool calls onto parts.
 *
 * When `textAtToolCallStart` carries a snapshot for any tool ID, interleave
 * the tool-call parts with text segments so the rendered order matches the
 * sequence in which the assistant streamed them. Without snapshots, fall
 * back to "text first, then tool calls" — the historical behavior used for
 * REST-loaded transcripts where snapshots are unavailable.
 *
 * This is what allows non-last (older) assistant turns to keep their tool
 * calls interleaved with the text after a new turn starts; the first-fix
 * baker preserves snapshots for previously-baked IDs precisely so this
 * function can use them.
 */
function pushHistoryParts(
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  parts: any[],
  text: string,
  historyToolCalls: NonNullable<ChatMessage["tool_calls"]>,
  toolCalls: Record<string, StoreToolCall>,
  textAtToolCallStart?: Record<string, string>,
): void {
  if (historyToolCalls.length === 0) {
    parts.push({ type: "text", text });
    return;
  }

  // Dedupe by tool-call id: @assistant-ui/react keys tool-call parts by
  // toolCallId, and a duplicate id triggers a hard React error
  // ("Duplicate key toolCallId-…"). Edge cases that produce duplicates
  // (e.g. replay re-emitting an id already baked into the message, or
  // a turn rebake during reconnect) shouldn't crash the page.
  {
    const seen = new Set<string>();
    historyToolCalls = historyToolCalls.filter((tc) => {
      if (seen.has(tc.id)) return false;
      seen.add(tc.id);
      return true;
    });
  }

  if (textAtToolCallStart) {
    const ordered = [...historyToolCalls].sort((a, b) => {
      const sa = textAtToolCallStart[a.id]?.length ?? Number.POSITIVE_INFINITY;
      const sb = textAtToolCallStart[b.id]?.length ?? Number.POSITIVE_INFINITY;
      return sa - sb;
    });
    let prevTextEnd = 0;
    for (const tc of ordered) {
      const segmentEnd = textAtToolCallStart[tc.id]?.length;
      if (segmentEnd !== undefined && segmentEnd > prevTextEnd && segmentEnd <= text.length) {
        parts.push({ type: "text", text: text.slice(prevTextEnd, segmentEnd) });
        prevTextEnd = segmentEnd;
      }
      const resolved: ToolCall = toolCalls[tc.id] ?? tc;
      parts.push({
        type: "tool-call",
        toolCallId: tc.id,
        toolName: tc.tool,
        args: tc.params,
        result: resolved.result,
      });
    }
    if (prevTextEnd < text.length) {
      parts.push({ type: "text", text: text.slice(prevTextEnd) });
    } else if (prevTextEnd === 0 && text.length === 0) {
      parts.push({ type: "text", text: "" });
    }
    return;
  }

  parts.push({ type: "text", text });
  for (const tc of historyToolCalls) {
    const resolved: ToolCall = toolCalls[tc.id] ?? tc;
    parts.push({
      type: "tool-call",
      toolCallId: tc.id,
      toolName: tc.tool,
      args: tc.params,
      result: resolved.result,
    });
  }
}

function buildContentParts(
  msg: ChatMessage,
  toolCalls: Record<string, StoreToolCall>,
  toolCallOrder: string[],
  textAtToolCallStart: Record<string, string>,
  isLastAssistant: boolean
): ThreadMessageLike["content"] {
  try {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const parts = [] as any;
    const historyTCs = msg.tool_calls ?? [];

    // Non-last or non-assistant messages: interleave history tool calls with text
    // segments using the global `textAtToolCallStart` snapshots so previously-baked
    // turns keep their original streamed order across new turns.
    if (!isLastAssistant || msg.role !== "assistant") {
      pushHistoryParts(parts, msg.content, historyTCs, toolCalls, textAtToolCallStart);
      return parts;
    }

    // Last assistant message: check for live (in-progress) tool calls to interleave.
    const seenIds = new Set(historyTCs.map((tc) => tc.id));
    const liveIds = toolCallOrder.filter((id) => !seenIds.has(id) && toolCalls[id]);

    if (liveIds.length === 0) {
      pushHistoryParts(parts, msg.content, historyTCs, toolCalls, textAtToolCallStart);
      return parts;
    }

    // Interleave: emit text segments between tool calls using snapshots.
    // textAtToolCallStart[callId] = assistant content when tool_call_start arrived.
    let prevTextEnd = 0;
    const fullText = msg.content ?? "";

    // Emit history tool calls (if any) interleaved with the text using snapshots.
    if (historyTCs.length > 0) {
      pushHistoryParts(parts, fullText, historyTCs, toolCalls, textAtToolCallStart);
      prevTextEnd = fullText.length;
    }

    // Interleave live tool calls with text segments
    for (const id of liveIds) {
      const tc = toolCalls[id];
      if (!tc) continue;
      const segmentEnd = (textAtToolCallStart[id] ?? "").length;
      if (segmentEnd > prevTextEnd) {
        parts.push({ type: "text", text: fullText.slice(prevTextEnd, segmentEnd) });
      }
      prevTextEnd = segmentEnd;
      parts.push({
        type: "tool-call",
        toolCallId: id,
        toolName: tc.tool,
        args: tc.params,
        result: tc.result,
      });
    }

    // Emit any remaining text after the last tool call
    if (prevTextEnd < fullText.length) {
      parts.push({ type: "text", text: fullText.slice(prevTextEnd) });
    } else if (parts.length === 0) {
      // Ensure at least one text part (needed for streaming placeholder)
      parts.push({ type: "text", text: fullText });
    }

    return parts;
  } catch (err) {
    console.error('[omnipus-runtime] buildContentParts failed:', err);
    return [{ type: "text", text: msg.content ?? "[Error rendering message]" }];
  }
}

// buildMessageStatus is only ever called for assistant messages (see convertMessage below).
// Tightening the parameter to AssistantMessage makes the role-narrowing real at the call
// site and removes the dead user/system arms that were typed but never reachable.
function buildMessageStatus(msg: AssistantMessage & { isStreaming?: boolean }): ThreadMessageLike["status"] {
  if (msg.isStreaming) return { type: "running" };
  if (msg.status === "error") return { type: "incomplete", reason: "error" };
  if (msg.status === "interrupted") return { type: "incomplete", reason: "cancelled" };
  return { type: "complete", reason: "stop" };
}

// Per-turn model record (FR-014). The renderer (MessageItem.tsx and
// VirtualAssistantMessageRow in ChatScreen.tsx) reads `msg.model`
// directly off the ChatMessage — that's the only consumer surface for
// the per-turn model. There is no `metadata.custom.model` write here
// because no renderer reads it (orphan fields on the ThreadMessageLike
// that AssistantUI never round-tripped back to the renderer).
//
// Legacy turns (no `model` field recorded) must NOT show any model info
// (spec §18 Q6: no placeholder text). The renderer's own trim-and-
// length-check handles that — see MessageItem.tsx and
// VirtualAssistantMessageRow.

export function convertMessage(
  msg: ChatMessage,
  toolCalls: Record<string, StoreToolCall>,
  toolCallOrder: string[],
  textAtToolCallStart: Record<string, string>,
  isLastAssistant: boolean
): ThreadMessageLike {
  return {
    id: msg.id,
    role: msg.role,
    content: buildContentParts(msg, toolCalls, toolCallOrder, textAtToolCallStart, isLastAssistant),
    ...(msg.role === "assistant" ? { status: buildMessageStatus(msg) } : {}),
  };
}

// ── Runtime hook ──────────────────────────────────────────────────────────────

export function useOmnipusRuntime() {
  const messages = useChatStore((s) => s.messages);
  const toolCalls = useChatStore((s) => s.toolCalls);
  const toolCallOrder = useChatStore((s) => s.toolCallOrder);
  const textAtToolCallStart = useChatStore((s) => s.textAtToolCallStart);
  const isStreaming = useChatStore((s) => s.isStreaming);
  const sendMessage = useChatStore((s) => s.sendMessage);
  const cancelStream = useChatStore((s) => s.cancelStream);
  const addToast = useUiStore((s) => s.addToast);
  // Phase 1 / FR-008/009/010: the composer's model picker writes the
  // picker's value here. We read it in onNew so the next message is
  // routed via the chosen model. The store clears `nextModel` after
  // sendMessage completes, so the picker auto-derives a fresh default
  // on the next session reopen (per spec §18 Q3).
  const nextModel = useChatStore((s) => s.nextModel);

  return useExternalStoreRuntime<ChatMessage>({
    messages,
    isRunning: isStreaming,
    convertMessage: (msg) => {
      // Check if this is the last assistant message — live tool calls from
      // the store are attached only to the last assistant message.
      const lastAssistantIdx = messages.map((m) => m.role).lastIndexOf('assistant');
      const isLastAssistant = lastAssistantIdx >= 0 && messages[lastAssistantIdx].id === msg.id;
      return convertMessage(msg, toolCalls, toolCallOrder, textAtToolCallStart, isLastAssistant);
    },
    adapters: {
      // Native attachment handling — uploads files to /api/v1/uploads and yields
      // a media:// ref, which onNew threads into our WS transport. Replaces the
      // old hand-rolled upload bridge in ChatScreen.
      attachments: omnipusAttachmentAdapter,
    },
    onNew: async (message: AppendMessage) => {
      // message.content carries ONLY the user's typed text (AssistantUI keeps
      // attachments separate in message.attachments — verified in
      // base-composer-runtime-core). Join all text parts to be safe.
      const text = message.content
        .filter((p): p is { type: "text"; text: string } => p.type === "text")
        .map((p) => p.text)
        .join("\n");

      // Pull the media:// refs (and display metadata) the attachment adapter
      // stashed during send(), keyed by attachment id.
      const mediaRefs: string[] = [];
      const attachments: MediaAttachment[] = [];
      for (const att of message.attachments ?? []) {
        const resolved = takeResolvedUpload(att.id);
        if (resolved) {
          mediaRefs.push(resolved.ref);
          attachments.push({
            type: isImageAttachment(resolved.filename, resolved.contentType) ? "image" : "file",
            url: resolved.url,
            filename: resolved.filename,
            contentType: resolved.contentType,
          });
        } else {
          // send() returned a failedComplete for this attachment (upload or
          // registration error) — the ref was never stashed. Warn so the user
          // knows the file did not reach the agent.
          console.warn(`[omnipus-runtime] Attachment "${att.name}" (id=${att.id}) had no resolved ref — it will not be sent to the agent.`);
          addToast({ message: `Attachment "${att.name}" was not sent — it failed to upload or register.`, variant: "error" });
        }
      }

      // ADR-051 Rev 4 (Slice H): drain pending workspace-LIBRARY attachments
      // added via the composer picker. These are reused manifest entries
      // (media://workspace/<ws>/<id>, FR-022) — no upload, no File. Drain
      // (not read) so a single send cannot re-thread them into a later
      // message. mediaRefURL() derives the local preview URL from the ref
      // itself (not lib.mediaId) — a bare /api/v1/media/{id} route 404s for
      // workspace-scoped entries (HandleMedia is a legacy-registry lookup
      // that rejects any id containing "/"); see mediaRefURL's doc comment.
      for (const lib of takeLibraryAttachments()) {
        mediaRefs.push(lib.ref);
        attachments.push({
          type: isImageAttachment(lib.filename, lib.contentType) ? "image" : "file",
          url: mediaRefURL(lib.ref),
          filename: lib.filename,
          contentType: lib.contentType,
        });
      }

      // Attachment-only messages (no typed text) are valid — send as long as we
      // have either text or at least one resolved attachment.
      if (!text.trim() && mediaRefs.length === 0) {
        console.warn("[omnipus-runtime] Message received with no text and no attachments — skipping.");
        addToast({ message: "Could not send — message had no text or attachments.", variant: "error" });
        return;
      }

      if (mediaRefs.length > 0) {
        sendMessage(text, { mediaRefs, attachments, ...(nextModel ? { model_name: nextModel } : {}) });
      } else {
        sendMessage(text, nextModel ? { model_name: nextModel } : undefined);
      }
    },
    onCancel: async () => { cancelStream() },
  });
}
