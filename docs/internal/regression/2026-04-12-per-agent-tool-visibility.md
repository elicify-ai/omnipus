# End-to-End Regression Report: Per-Agent Tool Visibility

**Date:** 2026-04-12
**Branch:** `feature/next-wave`
**Issues:** #15 (Agent CRUD), #41 (Per-Agent Tool Visibility)
**Scope:** Validate that PR 2 (#41) — per-agent tool visibility with 3-layer scope filtering, Tools & Permissions UI, and accordion agent profile — works end-to-end on the correct embedded SPA with a real LLM provider.

## Test environment

- Host: Linux 6.8.0-107-generic, Go 1.26, Node 24
- Clean home dir: `/tmp/omnipus-vqa-home`
- Master key: auto-generated on first boot
- Binary: `CGO_ENABLED=0 go build -o /tmp/omnipus-test/omnipus ./cmd/omnipus`
- SPA: rebuilt via `npm run build`, synced to `pkg/gateway/spa/`, re-embedded in binary
- Provider: OpenRouter (real API key)
- Model: z-ai/glm-5-turbo (tool-use capable)
- Gateway port: 5000 (default 3000 was occupied by another app)

## Test gates

- `CGO_ENABLED=0 go build ./...` — clean
- `CGO_ENABLED=0 go vet ./...` — clean
- `go test ./pkg/tools/ ./pkg/config/ ./pkg/gateway/ ./pkg/agent/ ./pkg/sysagent/...` — all 6 suites pass
- `tsc --noEmit` — clean
- `npm run build` — clean (only pre-existing chunk size warnings)
- 7 review agents (code-reviewer, code-simplifier, comment-analyzer, pr-test-analyzer, silent-failure-hunter, type-design-analyzer, architect) — all findings resolved

## New tests added (26 total)

- `pkg/tools/compositor_test.go` — 8 tests: FilterToolsByVisibility (inherit/explicit modes, scope gates, empty visible, core agent, nil config)
- `pkg/config/config_test.go` — 6 tests: ResolveType (explicit, system, core, custom, nil callback), AgentToolsCfg JSON round-trip
- `pkg/gateway/rest_test.go` — 12 tests: HandleBuiltinTools (GET, POST rejection), HandleMCPTools (GET, POST rejection), getAgentTools (system, custom), updateAgentTools (system forbidden, not found, invalid mode, success), createAgent with tools_cfg, GuardedTool.Scope delegation

## Scenarios

### 1. Cold boot + onboarding — PASS

- Fresh home dir, no config
- Gateway starts with `--allow-empty`, auto-generates master key
- Onboarding wizard: Welcome → Provider (OpenRouter) → API Key → Model (z-ai/glm-5-turbo) → Admin Account → Complete
- All 4 steps render correctly, progress bar updates
- "Connected successfully" after API key entry
- Model search/filter works (350+ models, search "glm-5-turbo" finds it)

### 2. Real LLM chat — PASS

- Sent: "What is the square root of 144?"
- Response: "The square root of 144 is **12**." (correct)
- Token count: 3.8k, cost: $0.0125
- Session bar shows model name, live token/cost updates
- Message bubbles render with correct alignment (user right, agent left)
- Markdown formatting (bold, numbered lists) renders correctly

### 3. Create Agent with Tools & Permissions — PASS

- Create Agent modal shows **two tabs**: "General" and "Tools & Permissions"
- General tab: name, description, model selector, icon picker, color picker, Advanced toggle
- Tools & Permissions tab:
  - 5 preset buttons: Read-only Researcher, Developer, Task Manager, Unrestricted, Custom
  - "Custom" selected by default with gold border
  - Tool list grouped by category ("general 0/20")
  - Clicking "Read-only Researcher" → count updates to 5/20, preset highlights
  - MCP Servers section: "No MCP servers configured. Add servers in Settings."
- Created "Research Bot" with Researcher preset → appears in agents list with "draft" badge

### 4. Agent Profile — Accordion layout — PASS

- 7 collapsible sections: Identity (default open), Model Configuration, Rate Limits, Behavior, Tools & Permissions, Sessions, Activity
- All sections collapse/expand independently
- Identity section: name, description, avatar color picker, icon selector

### 5. Agent Profile — Tools & Permissions panel — PASS

- Header shows "5 selected" count (from Researcher preset)
- "Read-only Researcher" preset auto-detected with gold border (from saved config)
- Tool checkboxes: `web_fetch` and `web_search` checked, others unchecked
- Core-scope tools (`append_file`, `edit_file`, `exec`) show amber warning icon
- "Save Tool Permissions" button with "Selected: 5 tools" label
- MCP Servers empty state

### 6. System Agent Profile — No Tools section — PASS

- "Omnipus" with "system" type badge
- Only 3 read-only sections: Model Configuration, Sessions, Activity
- No Identity, Rate Limits, Behavior, or Tools & Permissions sections
- No Save button

### 7. All other screens — PASS

- **Agents list**: System agent (active, green dot) + custom agent (draft badge, description)
- **Command Center**: Gateway online, 2 agents, 1 channel, rate limits disabled, task board with tabs
- **Skills & Tools**: 4 tabs (Installed Skills, MCP Servers, Channels, Built-in Tools), empty states
- **Settings**: 8 tabs, OpenRouter "Connected" with Test/Edit
- **Sidebar**: 5 nav items with icons, active route highlighted, Settings + Sign out at bottom
- **Login**: Branded, mascot, username/password fields, "Login failed" on wrong credentials

### 8. Console errors — PASS

- Zero JS errors on port 5000 session
- No uncaught exceptions, no missing modules, no 404s on assets

## Known issues (pre-existing, not PR #41)

1. **Onboarding 401**: Provider connection endpoint requires auth before admin account exists. Workaround: `dev_mode_bypass: true` in config. Should be fixed by using `withOptionalAuth` on onboarding endpoints.
2. **SPA embed pipeline**: `npm run build` outputs to `dist/spa/` but `go:embed` reads from `pkg/gateway/spa/`. Must manually sync. Documented in CLAUDE.md.
3. **Port 3000 conflict**: Default port may conflict with other local apps. Check `lsof -i :3000 | grep LISTEN` before starting.

## Verdict

**PASS** — All PR #41 features verified end-to-end on the correct embedded SPA with real OpenRouter LLM chat. No regressions detected.
