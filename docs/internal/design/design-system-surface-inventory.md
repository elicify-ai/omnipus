# Design-system catalog and application surface inventory

This inventory is the closed classification boundary for the migration. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/design-system/catalog.json` classifies every production source in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/src/components/ui` as exactly one of foundations, primitive, composite, domain, or application. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/design-system/surfaces.json` assigns every production route and every named tab, modal, or dialog surface to one delivery lane and at least one verification method.

## Catalog

Current source counts: {'application': 2, 'composite': 10, 'domain': 5, 'foundations': 7, 'primitive': 25}.

`AlertDialog` is an internal implementation used by the public `ConfirmDialog` contract during migration. It remains consumable inside the application until C5 and is intentionally absent from the public library entrypoint.

The actual publish boundary today is the root `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/package.json` named `@omnipus/ui`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/vite.lib.config.ts`, and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/dist/lib`. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/packages/ui/package.json` is a separate stale stub: its local build command has no matching configuration and its dependency list reflects the older application package. C5 must settle that duplicate package boundary and ensure the published library does not require application stores, routing, or other app dependencies. This inventory records the distinction; it does not modify either package file.

Every module export from a cataloged source is recorded in `exports`, including private values and types. `publicExports` and `publicTypes` remain the separate reviewed package boundary; catalog completeness never makes an internal name public automatically.

The catalog shape is enforced by `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/design-system/catalog.schema.json` before semantic source and export checks run.

## Surfaces

Current surface counts by kind: {'modal': 81, 'redirect': 3, 'route': 28, 'screen': 8, 'tab': 58}. Lane counts follow the approved responsibilities: {1: 30, 2: 5, 3: 7, 4: 23, 5: 33, 6: 58, 7: 15, 8: 7}.

Each surface supplies a route URL or source-qualified activation entry plus typed `verification`. Each verification has an `executed` or `planned` status; planned evidence deliberately fails the validator. Routes, redirects, and screens require unit, browser, and reflow coverage; tabs and modal/dialog surfaces require unit, interaction, and browser coverage. The validator rejects missing or duplicate verification kinds. These mappings state required future evidence and do not claim the evidence already passed.

The inventory shape is enforced by `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/design-system/surfaces.schema.json`; semantic validation then checks uniqueness, source ownership, lane agreement, discovery completeness, and the evidence matrix.

## Validation

Run `node scripts/design-system/catalog.mjs` for the fail-closed check or `node --test scripts/design-system/catalog.test.mjs` for its seeded negative fixtures. The validator rejects a missing `.ts` or `.tsx` UI classification, omitted or stale module export, missing or wrongly classified public export, duplicate source/surface/check, missing source file, invalid lane, incomplete verification matrix, unowned production helper, and omitted route or dynamic tab family.

## Current expected gaps

The validator remains red while any component manifest is incomplete and while surface evidence is planned. The structural catalog and inventory test filters only those explicit delivery gaps; it does not weaken their reporting in the command-line validator.

## Manual navigation review

The syntax inventory is supplemented by reviewed navigation entries. `WorkspaceTabBar.WORKSPACE_TABS` records Chat, Tasks, Calendar, Library/Media, Team, and the separate workspace-settings entry with `/workspaces/:workspaceId/...` paths. `WorkspaceTasksTab` records its Board and List in-screen switch; graph is a separate workspace route and tab. `SettingsScreen.SettingsTab` records all eleven `?tab=` selectors. Mapped JSX tabs are recorded as dynamic tab families, including workspace navigation, ask-user choices, Library search filters, and server-provided Base preview views. Sheet-based slide-overs are discovered from their JSX primitive even when their component filename ends in `Panel` or `SlideOver`.

`sourceOwners` assigns one repair lane to each of the 329 production application `.ts` and `.tsx` sources, including non-visual helpers that may be edited during a surface repair. Multiple surfaces may share a source, but they cannot receive different lane owners; the validator rejects that conflict to prevent parallel edits to one file.

There are currently 534 planned verification mappings: three for each of 178 surfaces. They identify target check IDs, kinds, files, and test names, and remain validator failures until C6 supplies executable evidence.
