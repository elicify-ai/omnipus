# Workspaces (UI)

Workspace is the product; everything below is a tab of it (routes
`src/routes/_app/workspaces.$workspaceId.{chat,board,list,calendar,graph,media,team,settings}.tsx`).
Board/list (the "work" module's task/plan UI: `BoardView`, `CreateTaskSlideOver`,
`CreatePlanSlideOver`, `AcceptanceCriteriaEditor`, `DefinitionOfDoneEditor`)
lives here; Calendar has its own module folder with its own file.

## Tests

CI group `components-workspaces` (pattern `src/components/workspaces/` — the
largest single group, ~110 files). Local: `npx vitest run src/components/workspaces/`.
A test file matching no group pattern runs in NO CI job while CI stays green;
`scripts/check-vitest-coverage.mjs` is the tripwire.

## Retired surfaces — do not resurrect

- Command Center screen / Schedules UI (`src/components/command-center/`) is
  deleted. The `/tasks` and `/automations` route files are redirect stubs onto
  `DefaultWorkspaceRedirect` (tab "board" / "calendar") — they exist so old
  links resolve, not as places to grow UI. Scheduled/recurring work lives
  exclusively in the workspace Calendar tab.
- The global Delegation Graph screen at `/agents/trust` is deleted (ADR-037):
  it looked functional (saved with a confirmation) but had zero effect on real
  delegation. Delegation is configured ONLY on a workspace's Team tab
  (`WorkspaceTeamTab.tsx`, `team/WorkspaceTeamGraph`, `team/EdgeModeEditor`).
  Re-adding any per-agent or global delegation control here is a regression.
