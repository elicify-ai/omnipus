# Settings (UI)

One `*Section.tsx` file per settings area (Chat, Gateway, Memory, Providers,
Security, Sandbox, Data, Devices, Diagnostics, Integrations, …) plus the
dialogs/cards they share (`GatewayRestartModal`, `ReAuthDialog`,
`DefaultModelCard`). The shell `SettingsScreen.tsx` lives in
`src/components/screens/`. Per the module map, `src/components/providers/`
(picker/detail/sign-in panels) and the Usage screens belong to this module
too. A new setting goes into a new or existing section file wired into the
shell — not a new screen, and not inline markup grown inside the shell.

## Cross-links that live elsewhere

- "Verbose chat" (`ChatSection.tsx`) governs chat thread/panel tool-call
  visibility — its meaning is defined in `src/components/chat/CLAUDE.md`;
  keep the label and the behaviour in sync when touching either.
- Per-agent shell deny patterns are edited in
  `src/components/agents/ShellDenyPatternsEditor.tsx`, not here.

## Tests

CI group `components-agents-settings` (pattern includes
`src/components/settings/`). Local: `npx vitest run src/components/settings/`.
A test file matching no group pattern runs in NO CI job while CI stays green;
`scripts/check-vitest-coverage.mjs` is the tripwire.
