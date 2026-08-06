# Code intelligence

This project uses **GitNexus**, not graphify. `graphify` is retired — there is no
`graphify-out/` here, and `/graphify` is not a workflow for this repo (removed 2026-07-25 —
do not reintroduce it or any `graphify-out/` directory).

- **GitNexus MCP tools** — for codebase questions use `query`, `context`, `impact`, `trace`,
  `explain`, falling back to Read/Grep when the graph does not cover a file.
- **GitNexus skills** — installed globally in `~/.claude/skills/` and trigger on intent, not a
  slash command: `gitnexus-exploring` ("how does X work?"), `gitnexus-debugging`,
  `gitnexus-impact-analysis` ("what breaks if…"), `gitnexus-refactoring`, `gitnexus-pr-review`,
  `gitnexus-cli`, `gitnexus-guide`, `gitnexus-taint-analysis`, `gitnexus-pdg-query`.

See the "Code intelligence: GitNexus" and "GitNexus — Code Intelligence" sections in the root
`CLAUDE.md` for the query rules.
