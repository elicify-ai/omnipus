import type { ReactNode } from 'react'
import type { MessagePartStatus } from '@assistant-ui/react'
import type { ToolCall } from '@/lib/api'
import type { DelegationEvent } from '@/lib/delegationEvents.types'
import { shouldRenderToolCall } from '@/lib/toolVisibility'
import { GenericToolCall } from './GenericToolCall'
import { WebServeBlock } from './WebServeUI'
import { SetGoalCardBlock } from './SetGoalToolUI'
import { BrowserToolReplayBlock, isReplayBrowserToolName } from './BrowserTool'
import { BashOutputBlock, isBashToolName } from './BashOutput'
import { FileReadBlock } from './FileReadPreview'
import { FileTreeBlock } from './FileTreeView'
import { WebSearchBlock } from './WebSearchResult'
import { WebFetchBlock } from './WebFetchPreview'
import { GoalSetupFailureLine } from './GoalSetupFailureLine'
import { delegationSlotted } from '../DelegationEventLine'

/**
 * F2: derives the replay MessagePartStatus object from the store's resolved
 * ToolCall.status, instead of the old hardcoded `{type:'complete'}`.
 * BrowserToolReplayBlock and GenericToolCall both consult `status` (via
 * isCancelledStatus) to decide whether to render the cancelled treatment —
 * hardcoding 'complete' meant isCancelledStatus could never be true on
 * replay, so a genuinely cancelled call rendered as a plain success. The
 * `isError` prop each of those two components also takes is passed
 * separately and explicitly at each call site (issue #617), so widening
 * `status.type` to 'incomplete' here does not reopen #617 — it is never
 * consulted for the error bit once an explicit `isError` prop is supplied.
 */
function replayPartStatus(status: 'running' | 'success' | 'error' | 'cancelled'): MessagePartStatus {
  return status === 'cancelled' ? { type: 'incomplete', reason: 'cancelled' } : { type: 'complete' }
}

export function renderHistoricalToolCall({
  tc,
  callId,
  inlineEvents,
  activeSessionId,
  verboseChatEnabled,
  goalRecordEmpty,
  liteMode,
}: {
  tc: ToolCall
  callId: string
  inlineEvents: readonly DelegationEvent[] | undefined
  activeSessionId: string | null
  verboseChatEnabled: boolean
  goalRecordEmpty: boolean
  liteMode: boolean
}): ReactNode {
  // Parity with the live AssistantUI dispatch in OmnipusRuntimeProvider:
  // web_serve / serve_workspace / run_in_workspace go through WebServeBlock
  // here too, so replayed sessions render the preview link (or the malformed
  // result block) instead of a collapsed generic badge.
  if (tc.tool === 'serve_workspace' || tc.tool === 'run_in_workspace' || tc.tool === 'web_serve') {
    return delegationSlotted(callId, inlineEvents, (
      <WebServeBlock
        key={callId}
        args={(tc.params ?? {}) as { path?: string; command?: string; port?: number; duration_seconds?: number }}
        result={tc.result ?? null}
        isRunning={false}
        // Issue #617: replay must reflect the tool call's real
        // outcome, mirroring GenericToolCall's `error={tc.error}`
        // below — previously this row always rendered "Done"
        // regardless of whether the call actually failed.
        isError={tc.status === 'error'}
        // F2: same fix for the CANCELLED case — WebServeBlock has no
        // `status` prop to derive cancellation from (unlike
        // BrowserToolReplayBlock/GenericToolCall), so it needs the
        // outcome threaded explicitly.
        isCancelled={tc.status === 'cancelled'}
        toolName={tc.tool}
      />
    ))
  }
  // B-fix: the six browser.*/browser_* tools also have a registered
  // live UI (BrowserToolBlock, dispatched via makeAssistantToolUI in
  // OmnipusRuntimeProvider) that parses the tool's result into a
  // screenshot/text/error card instead of showing raw JSON. Route
  // replay through the same block for live/replay parity — see
  // BrowserToolReplayBlock's doc comment for the full story.
  //
  // Issue #617: `status` used to be hardcoded to `{type:'complete'}`
  // with no error signal threaded at all, so every replayed browser
  // tool call rendered "OK" unconditionally — mirror the
  // GenericToolCall branch below (`error={tc.error}`) by passing the
  // store's real outcome as `isError`.
  //
  // #617 follow-up (F2): `status` itself was ALSO hardcoded to
  // `{type:'complete'}`, so a CANCELLED replayed call (tc.status ===
  // 'cancelled') fell through the isError check (false, correctly)
  // but still rendered as a plain success — isCancelledStatus(status)
  // can only ever be true when status.type is 'incomplete' with
  // reason 'cancelled', never 'complete'. replayPartStatus derives
  // the real status object so both BrowserToolReplayBlock's and
  // GenericToolCall's own isCancelledStatus checks see it.
  if (isReplayBrowserToolName(tc.tool)) {
    return delegationSlotted(callId, inlineEvents, (
      <BrowserToolReplayBlock
        key={callId}
        toolName={tc.tool}
        args={tc.params}
        result={tc.result}
        status={replayPartStatus(tc.status)}
        isError={tc.status === 'error'}
      />
    ))
  }
  // ADR-082 D9: set_goal renders its dedicated record card
  // (SetGoalCardBlock) at the call's own interleaved position,
  // built from the call's own result — never GenericToolCall
  // (which self-gates set_goal to null; see toolVisibility.ts's
  // `set_goal` case). Mirrors the live registration
  // (SetGoalToolUI, OmnipusRuntimeProvider.tsx) so replay and
  // live render identically.
  if (tc.tool === 'set_goal') {
    // `tc.result` is passed UNCHANGED (review S12): on replay it
    // is the persisted `{ text: "<payload json>" }` envelope,
    // which SetGoalCardBlock's parser unwraps itself. The store's
    // resolved outcome (`status`/`error`) is passed explicitly so
    // a failed registration renders its quiet trace (review S4).
    return delegationSlotted(callId, inlineEvents, (
      <SetGoalCardBlock
        key={callId}
        args={tc.params}
        result={tc.result}
        status={replayPartStatus(tc.status)}
        isRunning={tc.status === 'running'}
        isError={tc.status === 'error'}
        error={tc.error}
        durationMs={tc.duration_ms}
        sessionId={activeSessionId ?? ''}
      />
    ))
  }
  // toolui-analysis item 2 + item 4 (founder-approved 2026-09-26):
  // the dedicated tool rows route through the SAME components on
  // replay as live, so a reloaded session matches what was on
  // screen while the turn happened. These branches mirror the live
  // makeAssistantToolUI registrations (OmnipusRuntimeProvider.tsx);
  // everything they render is collapsed-by-default. They sit BEFORE
  // the GoalSetupFailureLine override below because live, a
  // registered dedicated UI pre-empts that fallback for these tools
  // too (FallbackToolUI is only reached by unregistered tools).
  if (isBashToolName(tc.tool)) {
    return delegationSlotted(callId, inlineEvents, (
      <BashOutputBlock
        key={callId}
        toolName={tc.tool}
        args={(tc.params ?? {}) as { command?: string; description?: string; action?: string }}
        result={tc.result}
        isRunning={false}
        isError={tc.status === 'error'} isCancelled={tc.status === 'cancelled'}
        error={tc.error} sessionId={activeSessionId ?? ''}
      />
    ))
  }
  if (tc.tool === 'read_file' || tc.tool === 'file.read') {
    return delegationSlotted(callId, inlineEvents, (
      <FileReadBlock
        key={callId}
        toolName={tc.tool}
        args={(tc.params ?? {}) as { path?: string }}
        result={tc.result}
        isRunning={false}
        isError={tc.status === 'error'} isCancelled={tc.status === 'cancelled'}
        error={tc.error} sessionId={activeSessionId ?? ''}
      />
    ))
  }
  if (tc.tool === 'list_dir' || tc.tool === 'list_directory' || tc.tool === 'file.list') {
    return delegationSlotted(callId, inlineEvents, (
      <FileTreeBlock
        key={callId}
        toolName={tc.tool}
        args={(tc.params ?? {}) as { path?: string }}
        result={tc.result}
        isRunning={false}
        isError={tc.status === 'error'} isCancelled={tc.status === 'cancelled'}
        error={tc.error} sessionId={activeSessionId ?? ''}
      />
    ))
  }
  if (tc.tool === 'web_search' || tc.tool === 'search_web') {
    return delegationSlotted(callId, inlineEvents, (
      <WebSearchBlock
        key={callId}
        toolName={tc.tool}
        args={(tc.params ?? {}) as { query?: string }}
        result={tc.result}
        isRunning={false}
        isError={tc.status === 'error'} isCancelled={tc.status === 'cancelled'}
        error={tc.error} sessionId={activeSessionId ?? ''}
      />
    ))
  }
  if (tc.tool === 'fetch_url' || tc.tool === 'web_fetch') {
    return delegationSlotted(callId, inlineEvents, (
      <WebFetchBlock
        key={callId}
        toolName={tc.tool}
        args={(tc.params ?? {}) as { url?: string }}
        result={tc.result}
        isRunning={false}
        isError={tc.status === 'error'} isCancelled={tc.status === 'cancelled'}
        error={tc.error} sessionId={activeSessionId ?? ''}
      />
    ))
  }
  // Operator-reported UX fix, 2026-09-08: same narrow override as
  // the live path's FallbackToolUI — see GoalSetupFailureLine.tsx's
  // file doc comment. Only intercepts a call that would otherwise
  // render visibly (toolVisibility.ts's hidden-tool contract for
  // background delegate/bash calls is unaffected — they stay
  // hidden regardless), outside verbose chat, while a goal is
  // active with an empty record.
  if (
    tc.status === 'error' &&
    !verboseChatEnabled &&
    goalRecordEmpty &&
    shouldRenderToolCall(tc.tool, tc.params as Record<string, unknown> | undefined, false, true)
  ) {
    return delegationSlotted(
      callId,
      inlineEvents,
      <GoalSetupFailureLine key={callId} toolName={tc.tool} result={tc.result} error={tc.error} />,
    )
  }
  return delegationSlotted(callId, inlineEvents, (
    <GenericToolCall
      key={callId}
      toolName={tc.tool}
      args={tc.params}
      result={tc.result}
      status={replayPartStatus(tc.status)}
      error={tc.error}
      // Issue #617: pass the store's real outcome explicitly rather
      // than letting GenericToolCall infer it from `status`/`error`
      // — see GenericToolCallProps.isError's doc comment for why the
      // inferred fallback misses a real failure whose `result` was
      // offloaded/replaced server-side with `error` left empty.
      isError={tc.status === 'error'}
      durationMs={tc.duration_ms}
      defaultCollapsed={liteMode}
      sessionId={activeSessionId ?? ''}
    />
  ))
}
