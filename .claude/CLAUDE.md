# Code intelligence

- **GitNexus** — knowledge-graph code intelligence over MCP. Skills are installed globally in
  `~/.claude/skills/` and trigger on intent, not a slash command: `gitnexus-exploring`
  ("how does X work?"), `gitnexus-debugging`, `gitnexus-impact-analysis` ("what breaks if…"),
  `gitnexus-refactoring`, `gitnexus-pr-review`, `gitnexus-cli`, `gitnexus-guide`,
  `gitnexus-taint-analysis`, `gitnexus-pdg-query`.
- See the "Code intelligence — GitNexus" section in the root `CLAUDE.md` for the query rules.
- graphify was removed on 2026-07-25 — do not reintroduce it or any `graphify-out/` directory.
