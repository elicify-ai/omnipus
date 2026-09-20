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
- Chat STATE does not live here: `src/store/chat.ts` is a barrel over
  `src/store/chat/{frames,messages,store,types}.ts` — edit the owning module,
  never the barrel. Those store tests run in the `lib-store` CI group, not
  `components-chat`.

## Tool-call visibility

- One render filter, `src/lib/toolVisibility.ts`, governs BOTH surfaces: the
  thread (`shouldRenderToolCall`, `shouldRenderSubagentSpan`) and the
  ActivityPanel (`shouldRenderToolCallInPanel`, whose default INVERTS the
  thread's). Never hardcode a second hide-list inside a component — that is how
  the filter's ground truth drifts.
- Hiding is render-only: hidden calls still exist in the persisted session
  transcript. Do not "fix" persistence to match what the UI shows.
- The ActivityPanel fallback is narrower than "fully transparent": subagent
  spans and background bash sessions only, capped at the 8 most-recently-finished
  (`RECENTLY_FINISHED_CAP`, `src/hooks/useRunningActivity.ts`). A delegation
  DENIED at dispatch time never opens a span, so it never reaches the panel —
  the calling agent's own narration is the only surface for it.
- "Verbose chat" (Settings → Chat, `chat-verbose-switch` in
  `src/components/settings/ChatSection.tsx`) reveals everything, thread and
  panel alike.

## Register

- Tool calls visible by default, collapsible. Rich content renders inline, no
  separate canvas.
- No emoji in stored data or UI chrome; the emoji→Phosphor translator in chat
  OUTPUT is the sanctioned one place emoji get converted — do not remove it as
  an "emoji violation".
