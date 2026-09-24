# Chat (UI)

`ChatScreen.tsx` and its pieces. The screen shell is reached via the workspace
chat tab (`src/routes/_app/workspaces.$workspaceId.chat.tsx`) — chat is a tab of
Workspace, not a sibling product (module map, "Product vs kernel").

## Design system

Read `.claude/skills/omnipus-design-system/SKILL.md` before adding or changing any
control, color, spacing, or type value here — it states the CI-enforced rules and cites
the script or test for each.

## Tests

- CI group `components-chat` (pattern `src/components/chat/` in
  `.github/workflows/pr.yml`). Local: `npx vitest run src/components/chat/`.
  A test file matching no group pattern runs in NO CI job while CI stays green;
  `scripts/check-vitest-coverage.mjs` is the tripwire.
- Chat STATE does not live here: `src/store/chat.ts` is a barrel over
  `src/store/chat/{frames,messages,store,types}.ts` — edit the owning module,
  never the barrel. Those store tests run in the `lib-store` CI group, not
  `components-chat`.

## Tool-call visibility

- One render filter for the chat THREAD, `src/lib/toolVisibility.ts`'s
  `shouldRenderToolCall`. Never hardcode a second hide-list inside a
  component — that is how the filter's ground truth drifts. ADR-091 D7/D10
  deleted `shouldRenderSubagentSpan` (the SubagentBlock delegation-card gate)
  along with SubagentBlock itself, and deleted `shouldRenderToolCallInPanel` /
  ToolCallBadge's `surface="panel"` prop along with the ActivityPanel step
  list they gated — a child's own frames never arrive in the parent's bucket
  any more (I-4), so there is no span/step surface left anywhere. The
  `delegate` tool-call chip (`shouldRenderToolCall`'s `delegate` case) is now
  the parent chat's ONLY delegation surface, and ADR-091 D7/AC-7 requires it
  to carry that job: a `run` action (the default) is visible in the normal,
  non-verbose thread; only `status` (polling) stays hidden.
- Hiding is render-only: hidden calls still exist in the persisted session
  transcript. Do not "fix" persistence to match what the UI shows.
- The ActivityPanel slide-out (`ActivityPanel.tsx`) is a separate, unrelated
  surface: a flat per-child status row (status line + open control targeting
  the child's own session), not a gated list of the child's own tool-call
  steps — see `src/hooks/useRunningActivity.ts` and its
  `RECENTLY_FINISHED_CAP`.
- "Verbose chat" (Settings → Chat, `chat-verbose-switch` in
  `src/components/settings/ChatSection.tsx`) reveals everything in the
  thread, `delegate`'s `status` case included.

## Register

- Tool calls visible by default, collapsible. Rich content renders inline, no
  separate canvas.
- No emoji in stored data or UI chrome; the emoji→Phosphor translator in chat
  OUTPUT is the sanctioned one place emoji get converted — do not remove it as
  an "emoji violation".
