<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **omnipus** (47386 symbols, 167451 relationships, 300 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

> Index stale? Run `node .gitnexus/run.cjs analyze` from the project root — it auto-selects an available runner. No `.gitnexus/run.cjs` yet? `npx gitnexus analyze` (npm 11 crash → `npm i -g gitnexus`; #1939).

## Always Do

- **MUST run impact analysis before editing any symbol.** Before modifying a function, class, or method, run `impact({target: "symbolName", direction: "upstream"})` and report the blast radius (direct callers, affected processes, risk level) to the user.
- **MUST run `detect_changes()` before committing** to verify your changes only affect expected symbols and execution flows. For regression review, compare against the default branch: `detect_changes({scope: "compare", base_ref: "main"})`.
- **MUST warn the user** if impact analysis returns HIGH or CRITICAL risk before proceeding with edits.
- When exploring unfamiliar code, use `query({search_query: "concept"})` to find execution flows instead of grepping. It returns process-grouped results ranked by relevance.
- When you need full context on a specific symbol — callers, callees, which execution flows it participates in — use `context({name: "symbolName"})`.
- For security review, `explain({target: "fileOrSymbol"})` lists taint findings (source→sink flows; needs `analyze --pdg`).

## Never Do

- NEVER edit a function, class, or method without first running `impact` on it.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis.
- NEVER rename symbols with find-and-replace — use `rename` which understands the call graph.
- NEVER commit changes without running `detect_changes()` to check affected scope.

## Resources

| Resource | Use for |
|----------|---------|
| `gitnexus://repo/omnipus/context` | Codebase overview, check index freshness |
| `gitnexus://repo/omnipus/clusters` | All functional areas |
| `gitnexus://repo/omnipus/processes` | All execution flows |
| `gitnexus://repo/omnipus/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |
| Work in the Gateway area (2320 symbols) | `.claude/skills/generated/gateway/SKILL.md` |
| Work in the Agent area (2240 symbols) | `.claude/skills/generated/agent/SKILL.md` |
| Work in the Tools area (1325 symbols) | `.claude/skills/generated/tools/SKILL.md` |
| Work in the Browser area (484 symbols) | `.claude/skills/generated/browser/SKILL.md` |
| Work in the Ui area (226 symbols) | `.claude/skills/generated/ui/SKILL.md` |
| Work in the Providers area (217 symbols) | `.claude/skills/generated/providers/SKILL.md` |
| Work in the Runner area (212 symbols) | `.claude/skills/generated/runner/SKILL.md` |
| Work in the Task area (201 symbols) | `.claude/skills/generated/task/SKILL.md` |
| Work in the Settings area (193 symbols) | `.claude/skills/generated/settings/SKILL.md` |
| Work in the Config area (192 symbols) | `.claude/skills/generated/config/SKILL.md` |
| Work in the Audit area (190 symbols) | `.claude/skills/generated/audit/SKILL.md` |
| Work in the Security area (185 symbols) | `.claude/skills/generated/security/SKILL.md` |
| Work in the Sandbox area (181 symbols) | `.claude/skills/generated/sandbox/SKILL.md` |
| Work in the Skills area (170 symbols) | `.claude/skills/generated/skills/SKILL.md` |
| Work in the Chat area (152 symbols) | `.claude/skills/generated/chat/SKILL.md` |
| Work in the Channels area (142 symbols) | `.claude/skills/generated/channels/SKILL.md` |
| Work in the Session area (114 symbols) | `.claude/skills/generated/session/SKILL.md` |
| Work in the Commands area (113 symbols) | `.claude/skills/generated/commands/SKILL.md` |
| Work in the Workspaces area (98 symbols) | `.claude/skills/generated/workspaces/SKILL.md` |
| Work in the Cron area (85 symbols) | `.claude/skills/generated/cron/SKILL.md` |

<!-- gitnexus:end -->
