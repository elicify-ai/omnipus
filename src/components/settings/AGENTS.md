# Settings (UI)

Settings has eleven sections (Settings redesign #1519,
`docs/internal/specs/settings-redesign-spec.md`): Providers, Memory & recaps,
System agents, Third-party agents, Limits, Protection, Sandbox & network,
Keys & secrets, Gateway, Data & retention and About. Each is one or more
`*Section.tsx` files plus the dialogs/cards they share (`GatewayRestartModal`,
`ReAuthDialog`, `DefaultModelCard`). The shell `SettingsScreen.tsx` lives in
`src/components/screens/`. Per the module map, `src/components/providers/`
(picker/detail/sign-in panels) and the Usage screens belong to this module
too.

## Adding a setting

A new setting goes into a new or existing section file wired into the
shell, not a new screen and not inline markup grown inside the shell. It
also needs:

- **The section list and dispatcher.** `settingsSections.ts::SETTINGS_SECTIONS`
  names the sections; `SettingsSectionBody.tsx::SettingsSectionBody` mounts
  each section's files.
- **The search index and its row.** Add the setting to
  `settingsSections.ts::SETTINGS_SEARCH_INDEX`, and put
  `{...settingsRow('<label>')}` on the element that shows it, so search and
  `?row=` links reach it (FR-075). A catalogued component that passes no extra
  attributes to the page (for example `ListGroup`) needs a wrapper element
  for the marker. A row inside a closed group must open that group first
  (see `LimitsSection.tsx::ROWS_IN_GROUP`).
- **The section Save.** Every value saves through the section's one Save bar,
  never on change. Use `useSaveBar.ts::useSaveBar` for a flat list of fields
  and `useFormSaveBar.ts::useFormSaveBar` when the section combines forms
  that each own a draft part. Do not use `@/hooks/useAutoSave` here.
- **The endpoint plan.** `savePlanner.ts` lists the one password-protected
  endpoint of each section (C-4); a password-marked value goes there, or the
  FR-030 guard test fails.
- **The agent write parity.** Every value a person can edit has an agent
  write path or a tested refusal; the Go test `TestSettingsAgentWriteParity`
  (FR-101) enumerates them.

`DevicesSection.tsx` is not mounted: device pairing moved out of Settings
(`docs/internal/specs/signed-in-browsers-spec.md`).

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
  to Ask. It lives at two scopes: the global switch
  (`SecuritySection.tsx::AutoApproveControl`, re-auth gated, writes
  `sandbox.auto_approve`) and
  the per-chat composer control
  (`src/components/chat/composer/AutoApprovePicker.tsx`, a labelled Auto button). God Mode stays in
  `GodModeControl.tsx`; the chat header no longer shows a mode badge. Operator command rules are
  config-file-only — no settings screen reads or writes them.

## Tests

CI group `components-agents-settings` (pattern includes
`src/components/settings/`). Local: `npx vitest run src/components/settings/`.
A test file matching no group pattern runs in NO CI job while CI stays green;
`scripts/check-vitest-coverage.mjs` is the tripwire.
