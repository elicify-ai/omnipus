# Unit-3 legacy follow-up — oracle freeze and case ledger

Work branch begins at f265de831be9c60e26830f69925f7a89432096b2; production base remains d701bcbd05bb64ce004f076f03ebf29b01675441. First fetch found 774ba486c, which does not contain step 2. Second fetch pinned and tested **105cfabb696e4697051ebb8f6e0f23439f820936**. The final steer adds support-code lint cleanup and pins **c07a0f9ede7275b7b520e019dc9bb976e30a83ec**; the final seven-file comparison is re-run there. Scratch HEAD is explicitly checked (the first checkout lacked the newly fetched object; its mislabeled typecheck receipt was rejected and retained separately). Do not let a moving remote branch silently change the comparison.

## Specification authority (before editing assertions)

/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/L1-AMENDMENTS-20261013.md §1.2 R6:

> "Card renders ... chat whose session id equals root_session_id (or session_id when root_session_id is omitted) ... Elsewhere ... 'not this chat' and points to the root chat."

> "The card header names the acting agent ... U13's 'Open run' link to the acting session is kept."

> "resolves only through POST ... the card never takes focus on appearance ... Escape does not resolve"

Section §1.5 deliberately replaces modal-placement oracles. The same amendment is in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/docs/internal/specs/omnipus-system-agent-spec.md::BDD-11.3 and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/docs/internal/specs/session-core-spec.md::FR-034, BDD-10.2, D06. B3 remains out of scope.

Behavior: an inline root-chat approval preserves acting-session attribution, and the app-wide notice counts approvals in other root chats rather than other workspaces.

Unit boundary: for U13, real approval/session/UI stores and QueryClient, real component and transport; only fetch/socket boundaries doubled. For the summary, real exported helper and a plain fixture chat-description function passed through its documented public parameter (no spies/mocks). In the five cleanup files, preserve every pre-existing test double/assertion outside the obsolete modal mock: dispatch explicitly limits these to cleanup plus one AppShell banner oracle.

Public signatures only were extracted at 105cfabb6 (no implementer test or function body read). ToolApprovalPanel has no required props. summarizeApprovalsElsewhere takes queue, activeChatId and describeChat(approval); its public result carries count, chatIds, targetChatId, target and label. The fixture description callback maps the independently computed root = rootSessionId ?? sessionId to user-provided chat labels. Expected identifiers/counts come from the fixture/spec, not the helper's observed output.

## Old → new, one row per deliberately changed case

| File/case | Old oracle | New oracle | Preserved unrelated checks |
|---|---|---|---|
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/src/components/agents/u13_approval-acting-run.test.tsx::names the acting execution | One dialog with acting Open control | Exactly one inline panel in the root chat, no dialog; Open control still targets acting session, not root | Exact approval-open-run testid and data-session-id; single card/queue identity; no automatic approval POST |
| Same file::still names acting agent | Raw-id fallback in modal when no name is cached | Same raw-id fallback in inline panel, foreground = owning root; real stores/cache/transport | Acting identity is not silently replaced by the root owner; exact raw actor text retained |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/src/lib/crossWorkspaceApprovals.test.ts::empty queue | null | null | Exact empty behavior |
| Same file::every approval in active workspace | null based on workspace | null only when each approval's root equals active chat, even across workspaces | Exact all-current exclusion |
| Same file::no workspaceId | null everywhere | A different chat without workspace still contributes; direct session used when root omitted | Explicit undefined workspace fixture; fallback-ID and count proof |
| Same file::no active workspace | null, modal global | No active chat means all queued roots are elsewhere; exact counts/targets | Both distinct queued scopes retained |
| Same file::singular | 1 + workspace name/id/target | 1 + root-chat descriptor/id/target | Exact count, unique scope list, first target and singular label |
| Same file::plural same scope | Count only out-of-workspace entries | Count only out-of-chat entries; two helpers below one root give two approvals in one distinct chat | In-scope/no-workspace controls, exact count 2, one unique scope and plural label |
| Same file::multiple scopes | 3 approvals / 2 other workspaces | 3 approvals / 2 other root chats | Exact count, unique ordered scope list and aggregate plural wording |
| Same file::first target | First encountered workspace | First encountered root chat | Queue-order target selection |
| Same file::unresolved name | Raw workspace id | Raw root-chat id from the caller's unresolved-name descriptor | Explicit honest name fallback |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/src/components/layout/AppShell.test.tsx::coexisting banners | Exact workspace-only "1 approval waiting in UAT-T2" | Notice still says 1 approval waiting, now names a chat in UAT-T2 (agent-name or unknown-agent description) | Both banners visible, alert semantics, neither contains the other, document order, main-content reachability; other 28 cases intact |

## Dead mocks only — no behavioral oracle changes

Remove exactly one vi.mock for the deleted ToolApprovalModal module in each:

| File | Case(s) kept unchanged |
|---|---|
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/src/components/layout/AppShell.test.tsx | All 29; only the banner row above changes oracle |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/src/components/layout/AppShell.editorHost.test.tsx | Exactly one store-driven editor dialog |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/src/components/layout/AppShell.quiet-disconnect.test.tsx | No alert, gateway/close-code text or connectionError through drop/retry/give-up |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/src/components/layout/AppShell.navFreshness.test.tsx | Three focus/visible/hidden refresh cases |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/src/components/workspaces/mail/MailPanel.attachmentDownloadError.test.tsx | Failed attachment download shows exact error class in real alert/toast |

No replacement mock of the inline panel is added. No deleted production module is restored.

## Avoid duplicating banner integration rows

The existing /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/src/components/layout/CrossWorkspaceApprovalBanner.test.tsx already covers same-workspace/no-workspace other chats, real navigation to root, Omnipus workspace preservation, ambient focus and resolution disappearance. Do not add another navigation/focus/browser row here. The old pure-helper cases above are retargeted in place; no new case count is needed.

## Support-code lint-only correction

/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red2/tests/l1-ui-red-u23/support.tsx::BoundarySocket.constructor: unused `_url` becomes explicitly discarded `url`; the socket's open event and timing are unchanged.

Same file::toasts: unused destructured `_id` becomes explicitly discarded `id`; the same id-free toast payload is returned. No assertion, skip, timeout, mock boundary, linter configuration or suppression changes. ESLint was first observed red with exactly these two errors, then run on this file to exit=0. Final typechecks and seven-file receipts use this support version.

## Proof and limits

Run each of the seven scoped files separately on base production plus tests and on the pinned implementation clone. Save commands, revisions, test hashes, counts and raw exit codes. Missing new export/component must be an explicit BLOCKED test failure, not a collection error/skip or a fallback to the obsolete modal/helper. Run npm run typecheck on both trees; report any unrelated implementation errors without changing production or unrelated tests. Comparison observations are not CHECK. Mutations, GREEN acceptance and Proof-of-failability remain deferred to an independent CHECK instance.

Potential faults covered: acting/root ID swapped; workspace-only exclusion; grouping by helper instead of root; null-active hides approvals; duplicate scope list; target order reversed; stale modal mock disguises a deleted production import. No mutation is applied in this RED lane.
