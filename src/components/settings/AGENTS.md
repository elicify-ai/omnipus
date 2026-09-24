# Settings (UI)

One `*Section.tsx` file per settings area (Chat, Gateway, Memory, Providers,
Security, Sandbox, Data, Devices, Diagnostics, Integrations, …) plus the
dialogs/cards they share (`GatewayRestartModal`, `ReAuthDialog`,
`DefaultModelCard`). The shell `SettingsScreen.tsx` lives in
`src/components/screens/`. Per the module map, `src/components/providers/`
(picker/detail/sign-in panels) and the Usage screens belong to this module
too. A new setting goes into a new or existing section file wired into the
shell — not a new screen, and not inline markup grown inside the shell.

## Design system

Read `.claude/skills/omnipus-design-system/SKILL.md` before adding or changing any
control, color, spacing, or type value here — it states the CI-enforced rules and cites
the script or test for each.

## Cross-links that live elsewhere

- "Verbose chat" (`ChatSection.tsx`) governs chat thread/panel tool-call
  visibility — its meaning is defined in `src/components/chat/CLAUDE.md`;
  keep the label and the behaviour in sync when touching either.
- The shell deny-pattern editor and the exec-binary allowlist (ADR-036 §3.1)
  are retired (ADR-092 D2/D5) — do not reintroduce them. There is no
  three-mode selector: `bash` keeps an ordinary Allow/Ask/Deny tool policy,
  and Auto-approve is a separate on/off switch that only acts on tools set
  to Ask. It lives at three scopes: the global switch
  (`SecuritySection.tsx::AutoApproveControl`, re-auth gated, writes
  `sandbox.auto_approve`), the per-agent "Never auto-approve for this agent"
  checkbox (`src/components/agents/ToolsAndPermissions.tsx`, off-only), and
  the per-chat composer switch
  (`src/components/chat/composer/AutoApprovePicker.tsx`). God Mode stays in
  `GodModeControl.tsx`; the chat-header state is
  `src/components/chat/ChatModeBadge.tsx`. Operator command rules are
  config-file-only — no settings screen reads or writes them.

## Tests

CI group `components-agents-settings` (pattern includes
`src/components/settings/`). Local: `npx vitest run src/components/settings/`.
A test file matching no group pattern runs in NO CI job while CI stays green;
`scripts/check-vitest-coverage.mjs` is the tripwire.
