# Skills (UI)

Skill browse/install surface (`SkillBrowser.tsx`, opened from
`SkillsScreen.tsx` in `src/components/screens/`) and MCP server management
(`McpServerModal.tsx`).

## Not everything here is skills

- `ChannelConfigPanel.tsx` is the per-channel Configure slide-over for the
  CONNECTORS screen (ConnectorsScreen imports it from here), and
  `WhatsAppNativeNotice.tsx` / `WhatsAppPairingBody.tsx` / `whatsappChannelId.ts`
  are its WhatsApp pairing pieces. They live in this folder today; do not
  duplicate them, and search here (not `src/components/connectors/`) for
  channel config UI. See `src/components/connectors/CLAUDE.md`.

## Tests

CI group `components-agents-settings` (pattern includes
`src/components/skills/`). Local: `npx vitest run src/components/skills/`.
A test file matching no group pattern runs in NO CI job while CI stays green;
`scripts/check-vitest-coverage.mjs` is the tripwire.
