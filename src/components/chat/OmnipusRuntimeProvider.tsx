// Wraps the app with AssistantUI's runtime context and manages the WebSocket lifecycle.
// Lives in AppShell so the WebSocket stays connected across all screens.

import { useEffect, useRef } from "react";
import { AssistantRuntimeProvider } from "@assistant-ui/react";
import { useOmnipusRuntime } from "@/lib/omnipus-runtime";
import { useChatStore } from "@/store/chat";
import { useConnectionStore } from "@/store/connection";
import { startMemoryObserver, addMemoryObserver } from "@/lib/memory-observer";
import { useSessionStore, resetChatBucketForReplay } from "@/store/session";
import { WsConnection } from "@/lib/ws";
import { queryClient } from "@/lib/queryClient";
import type { Session, SessionDetail } from "@/lib/api";
import { TerminalOutputUI } from "./tools/TerminalOutput";
import { FileReadPreviewUI, FileReadAliasDotUI } from "./tools/FileReadPreview";
import { FileWriteConfirmUI, FileWriteAliasDotUI, EditFileConfirmUI, AppendFileConfirmUI } from "./tools/FileWriteConfirm";
import { FileTreeViewUI, FileListAliasDotUI } from "./tools/FileTreeView";
import { WebSearchResultUI } from "./tools/WebSearchResult";
import { WebFetchPreviewUI, WebFetchLegacyUI } from "./tools/WebFetchPreview";
import { BrowserNavigateUI, BrowserNavigateUnderscoreUI } from "./tools/BrowserNavigate";
import { WebServeUI } from "./tools/WebServeUI";
import { ServeWorkspaceUI } from "./tools/ServeWorkspaceUI";
import { RunInWorkspaceUI } from "./tools/RunInWorkspaceUI";
import { WorkspaceShellUI, WorkspaceShellBgUI, WorkspaceShellLegacyUI, WorkspaceShellBgLegacyUI } from "./tools/WorkspaceShellUI";
import {
  BrowserClickUI, BrowserClickUnderscoreUI,
  BrowserTypeUI, BrowserTypeUnderscoreUI,
  BrowserScreenshotUI, BrowserScreenshotUnderscoreUI,
  BrowserGetTextUI, BrowserGetTextUnderscoreUI,
  BrowserWaitUI, BrowserWaitUnderscoreUI,
  BrowserEvaluateUI, BrowserEvaluateUnderscoreUI,
} from "./tools/BrowserTool";

// Manages the memory pressure observer lifecycle.
// Starts the polling loop on mount and wires heap-level transitions into the connection store.
function MemoryObserverLifecycle() {
  const setLiteMode = useConnectionStore((s) => s.setLiteMode)

  useEffect(() => {
    const observer = startMemoryObserver()
    const unsubscribe = addMemoryObserver((snap) => {
      // Activate lite mode at 250 MiB; deactivate if heap drops back below 200 MiB.
      if (snap.level === 'lite_mode' || snap.level === 'critical') {
        setLiteMode(true)
      } else if (snap.level === 'ok') {
        setLiteMode(false)
      }
      // dev_warn: no liteMode change — just the console.info already emitted
      // by the observer itself (in memory-observer.ts).
    })
    return () => {
      observer.dispose()
      unsubscribe()
    }
    // setLiteMode is a stable Zustand reference.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return null
}

// Resolves the last-active agent id for a session when re-seeding the
// header/composer on WS (re)connect. Always prefers active_agent_id
// (post-handover) over the creating agent_id, and never downgrades a known
// live value to the creating agent.
//
// Resolution order:
//   1. The LIVE store value when the session matches — it already reflects any
//      live handover (set by the agent_switched frame). Preferring it first
//      means a transient reconnect during a live handover can never be clobbered
//      by a stale session-detail cache.
//   2. The cached ['session-detail', id] — set fresh by the route loader on a
//      full reload (the case this fix targets); active_agent_id ?? agent_id.
//   3. The cached ['sessions'] list entry; active_agent_id ?? agent_id.
//   4. null — leave the live store untouched.
export function resolveLastActiveAgentId(sessionId: string): string | null {
  const session = useSessionStore.getState();
  if (session.activeSessionId === sessionId && session.activeAgentId) {
    return session.activeAgentId;
  }

  const detail = queryClient.getQueryData<SessionDetail>(['session-detail', sessionId]);
  if (detail?.session) {
    return detail.session.active_agent_id ?? detail.session.agent_id ?? null;
  }

  const list = queryClient.getQueryData<Session[]>(['sessions']);
  const fromList = list?.find((s) => s.id === sessionId);
  if (fromList) {
    return fromList.active_agent_id ?? fromList.agent_id ?? null;
  }

  return null;
}

// Minimal surface of WsConnection used by reattachActiveSession — just the
// send method — so the reattach logic can be unit-tested without a real
// WebSocket. Pick keeps the signature exactly in sync with WsConnection.send
// (typed against ClientFrame) so a real connection is assignable.
export type ReattachSender = Pick<WsConnection, 'send'>;

// Re-binds the active session to a freshly-opened WS connection (the onConnected
// path). Extracted from the inline handler so the failed-reattach branch can be
// tested directly: a FAILED send must PRESERVE the existing transcript bucket
// (no replay will rebuild it), while a SUCCESSFUL send resets the bucket so the
// gateway's replay repopulates it from scratch.
//
// setConnectionError is injected so the helper can surface the "please reload"
// error without reaching into the connection store directly.
// Returns true when the attach frame was sent (replay in flight), false otherwise.
export function reattachActiveSession(
  conn: ReattachSender,
  setConnectionError: (msg: string | null) => void,
): boolean {
  const activeSessionId = useSessionStore.getState().activeSessionId;
  if (!activeSessionId) {
    return false;
  }
  // Re-seed the header/composer agent from the authoritative last-active agent
  // BEFORE replay starts. After a handover (e.g. Mia → Jim) a full reload must
  // keep the header on Jim (active_agent_id), not revert to the creating agent.
  const seedAgentId = resolveLastActiveAgentId(activeSessionId);
  if (seedAgentId) {
    useSessionStore.getState().setActiveSession(activeSessionId, seedAgentId);
  }
  // The gateway will replay the entire transcript. Mark the session as replaying
  // symmetrically here (same as in attachToSession).
  useChatStore.setState({ isReplaying: true });
  // Pass the since-cursor so the gateway only replays frames the SPA hasn't seen.
  const since =
    useChatStore.getState().sessionsById[activeSessionId]?.lastReceivedEventTime ?? undefined;
  // Reset the bucket ONLY on the success path, where the gateway's replay is in
  // flight and will repopulate it from scratch (preventing duplicate
  // "Browse to … / Browse to …" bubbles). If send() fails, the reattach never
  // happens and no replay will rebuild the transcript, so wiping the bucket
  // would leave the user a blank chat behind the "please reload" error. Preserve
  // the existing transcript instead.
  const sent = conn.send({ type: "attach_session", session_id: activeSessionId, since });
  if (!sent) {
    // send() returned false — socket closed between onopen and here. Preserve
    // local state (do not wipe bucket) and surface an error. Clear the replaying
    // flag we optimistically set above so the composer re-enables instead of
    // wedging on a replay that will never arrive.
    useChatStore.setState({ isReplaying: false });
    setConnectionError('Failed to reattach session — please reload');
    return false;
  }
  resetChatBucketForReplay(activeSessionId);
  return true;
}

// Manages WebSocket connection lifecycle — renders nothing, only side effects.
function WsLifecycle() {
  const handleFrame = useChatStore((s) => s.handleFrame);
  const setConnection = useConnectionStore((s) => s.setConnection);
  const setConnected = useConnectionStore((s) => s.setConnected);
  const setConnectionError = useConnectionStore((s) => s.setConnectionError);
  const setReconnectState = useConnectionStore((s) => s.setReconnectState);
  const connectionRef = useRef<WsConnection | null>(null);

  useEffect(() => {
    const conn = new WsConnection({
      onFrame: handleFrame,
      onConnected: async () => {
        setConnected(true);
        setConnectionError(null);
        // Drain any messages that were buffered while the connection was down.
        useChatStore.getState().drainOutboundQueue();
        // Re-bind any in-flight session to the freshly-opened WS so the
        // gateway's per-connection sessionID is restored. Without this, a
        // browser-suspended WS that auto-reconnects on focus would cause the
        // next user message to spawn a new session and the agent loses
        // transcript context. The reattach logic (seed agent, success/failure
        // bucket handling) lives in reattachActiveSession so it can be unit
        // tested directly.
        reattachActiveSession(conn, setConnectionError);
      },
      onDisconnected: () => {
        setConnected(false);
        // C8: a socket close mid-turn means the terminal (done/error) frame for
        // any in-flight turn will never arrive on THIS connection. Clear the
        // streaming/in-flight state now so the composer re-enables and the
        // "thinking" spinner resolves instead of wedging. If the turn is still
        // alive server-side, the reconnect + attach_session replay rebuilds the
        // correct state (and the gateway re-emits a terminal frame).
        useChatStore.getState().clearStreamingState();
      },
      onError: setConnectionError,
      // Fix 2: wire reconnect phase changes into the connection store so the
      // chat header banner can show live progress (attempt N / gave_up CTA).
      onReconnectStateChange: (phase, attempt) => setReconnectState(phase, attempt),
    });
    conn.connect();
    connectionRef.current = conn;
    setConnection(conn);

    return () => {
      conn.disconnect();
      connectionRef.current = null;
      setConnection(null);
    };
    // Store methods are stable Zustand references — intentional empty deps
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return null;
}

export function OmnipusRuntimeProvider({ children }: { children: React.ReactNode }) {
  const runtime = useOmnipusRuntime();

  return (
    <AssistantRuntimeProvider runtime={runtime}>
      {/*
       * Tool UI registrations — each component calls useAssistantToolUI on mount.
       * Tool name → UI component mapping. Underscore names match pkg/sysagent/tools/ exports
       * (Omnipus convention); dot-notation names match BRD C.6.1.4 spec. Both registered
       * to handle either naming convention from the agent.
       *   exec              → TerminalOutputUI          (shell command execution)
       *   read_file         → FileReadPreviewUI         (read file content)
       *   file.read         → FileReadAliasDotUI        (BRD alias)
       *   write_file        → FileWriteConfirmUI        (create/overwrite file)
       *   file.write        → FileWriteAliasDotUI       (BRD alias)
       *   edit_file         → EditFileConfirmUI         (targeted string replacement)
       *   append_file       → AppendFileConfirmUI       (append to file)
       *   list_dir          → FileTreeViewUI            (legacy alias, directory listing)
       *   list_directory    → FileTreeViewUI            (canonical name)
       *   file.list         → FileListAliasDotUI        (BRD alias)
       *   search_web        → WebSearchResultUI         (canonical, search the web)
       *   web_search        → WebSearchResultUI         (legacy alias)
       *   fetch_url         → WebFetchPreviewUI         (canonical, fetch a URL)
       *   web_fetch         → WebFetchLegacyUI          (legacy alias)
       *   serve_web         → WebServeUI                (canonical: static or dev, kind field)
       *   web_serve         → WebServeUI                (legacy alias)
       *   serve_workspace   → ServeWorkspaceUI          (back-compat alias → WebServeUI)
       *   run_in_workspace  → RunInWorkspaceUI          (back-compat alias → WebServeUI)
       *   workspace_shell   → WorkspaceShellUI          (canonical, foreground shell)
       *   workspace.shell   → WorkspaceShellLegacyUI   (legacy alias)
       *   workspace_shell_bg → WorkspaceShellBgUI      (canonical, background shell)
       *   workspace.shell_bg → WorkspaceShellBgLegacyUI (legacy alias)
       *   browser_navigate  → BrowserNavigateUI         (canonical, browser navigation)
       *   browser.navigate  → BrowserNavigateUnderscoreUI (legacy dot alias)
       *   browser_click     → BrowserClickUI            (canonical)
       *   browser.click     → BrowserClickUnderscoreUI  (legacy dot alias)
       *   browser_type      → BrowserTypeUI             (canonical)
       *   browser.type      → BrowserTypeUnderscoreUI   (legacy dot alias)
       *   browser_screenshot → BrowserScreenshotUI      (canonical)
       *   browser.screenshot → BrowserScreenshotUnderscoreUI (legacy dot alias)
       *   browser_get_text  → BrowserGetTextUI          (canonical)
       *   browser.get_text  → BrowserGetTextUnderscoreUI (legacy dot alias)
       *   browser_wait      → BrowserWaitUI             (canonical)
       *   browser.wait      → BrowserWaitUnderscoreUI   (legacy dot alias)
       *   browser_evaluate  → BrowserEvaluateUI         (canonical)
       *   browser.evaluate  → BrowserEvaluateUnderscoreUI (legacy dot alias)
       */}
      <TerminalOutputUI />
      <FileReadPreviewUI />
      <FileReadAliasDotUI />
      <FileWriteConfirmUI />
      <FileWriteAliasDotUI />
      <EditFileConfirmUI />
      <AppendFileConfirmUI />
      <FileTreeViewUI />
      <FileListAliasDotUI />
      <WebSearchResultUI />
      <WebFetchPreviewUI />
      <WebFetchLegacyUI />
      <WebServeUI />
      <ServeWorkspaceUI />
      <RunInWorkspaceUI />
      <WorkspaceShellUI />
      <WorkspaceShellBgUI />
      <WorkspaceShellLegacyUI />
      <WorkspaceShellBgLegacyUI />
      <BrowserNavigateUI />
      <BrowserNavigateUnderscoreUI />
      <BrowserClickUI />
      <BrowserClickUnderscoreUI />
      <BrowserTypeUI />
      <BrowserTypeUnderscoreUI />
      <BrowserScreenshotUI />
      <BrowserScreenshotUnderscoreUI />
      <BrowserGetTextUI />
      <BrowserGetTextUnderscoreUI />
      <BrowserWaitUI />
      <BrowserWaitUnderscoreUI />
      <BrowserEvaluateUI />
      <BrowserEvaluateUnderscoreUI />
      <MemoryObserverLifecycle />
      <WsLifecycle />
      {children}
    </AssistantRuntimeProvider>
  );
}
