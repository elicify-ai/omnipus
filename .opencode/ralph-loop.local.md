---
active: true
iteration: 0
maxIterations: 100
---

Implement the finalized hotfix/v0.1.1 agent-form refactor end-to-end. Follow the worktree fan-out plan:

1. Set up 7 worktrees from hotfix/v0.1.1: wt-contracts, wt-backend, wt-create, wt-profile, wt-runner, wt-tests, wt-silent.
2. Execute each worktree in merge order:
   - wt-contracts: Fix wire schemas (drop core/system/worker from create enum, drop worker from Agent.yaml/AgentToolsResponse, add updated_at, fix OpenAPI server URL, annotate voice-provider-detect.ts, regenerate contracts, add contract tests).
   - wt-backend: Add coreagent.ToWireType mapper, fix rest.go/rest_tool_registry.go response mapping, derive native executor for Subagent, coerce Main+external to native, deduplicate subagent_3p guards, implement updated_at 409, fix tool_feedback channel routing, backend silent failures, backend tests.
   - wt-create: Fix CreateAgentWizard/CreateAgentModal defaults and payload forwarding, fix shell_policy shape, enable Advanced tests.
   - wt-profile: Fix AgentProfile conditional formData for subagent_3p, wire VoiceProviderSub, hide voice for workers, restrict steering mode, add built-in roster to AgentListScreen, mobile accordions in AgentProfile, 409 conflict UI.
   - wt-runner: Implement Spec-4 external subagent JSON-streaming drivers, worktree isolation, consent routing, tests.
   - wt-tests: Add missing backend, unit, and Playwright tests.
   - wt-silent: Apply remaining error-handling/logging fixes and useAutoSave sendBeacon flush.
3. Merge each worktree back to hotfix/v0.1.1 via approved-PR discipline (but do not actually open PRs unless asked; merge locally after verification).
4. After everything is merged, re-run the 12 PR reviewers over the entire codebase.
5. Run all quality gates: typecheck, vitest, verify-contracts, targeted Go tests, Playwright tests.

Commit as the human operator using their GitHub no-reply email. Never force-merge. Keep git history clean. Work iteratively and verify each chunk before moving on.