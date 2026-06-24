# Comprehensive Tool-System UAT Plan (Edge-Case Driven) — v0.1.0

**Date:** 2026-06-24 · Supersedes the smoke-test plan `uat-plan-tools-2026-06.md`.
**Scope:** all 86 live builtin tools. **Principle:** every scenario is **outcome-verified**
(assert the real side-effect, not "tool fired") and the suite **stress-tests** each tool —
boundaries, every error/deny guard, malformed input — not just the happy path.

Grounded in the actual code (file:line cited). Model: `z-ai/glm-5.2`. Gateway config:
`sandbox.mode=enforce`, `browser_evaluate_enabled=false` (default — to test the deny gate;
a 2nd gateway with `=true` tests the allow path), DuckDuckGo search, cron enabled,
`workspace_shell_enabled=true`, `dev_mode_bypass=true`.

## Coverage rule (per tool)

Each tool MUST have: **(H)** happy path with outcome verified · **(B)** every parameter
boundary (min/max/enum) · **(G)** every guard/deny path triggered + asserted · **(M)** at
least one malformed/missing-input rejection. A tool is GREEN only when H+B+G+M all pass
(or a guard is N/A). Security guards (SSRF, path-escape, allowlist, deny-by-default,
cycle, cross-workspace) are MANDATORY — a tool with an untested guard is NOT done.

---

## FAMILY A — Filesystem (read_file, write_file, list_dir, edit_file, append_file)

### write_file (`pkg/tools/filesystem.go`; guard: overwrite flag, workspace confinement)
- **H** — "Write 'OUTCOME-42' to uat.txt" → verify file CONTENTS == "OUTCOME-42" (read the workspace file).
- **G-overwrite** — write to an existing file WITHOUT overwrite → must REJECT ("must set overwrite=true"). Then WITH overwrite=true → succeeds, content replaced. Verify both.
- **G-confinement** — "Write to /etc/uat-escape.txt" (outside workspace) → REJECT "access denied: path is outside the workspace". Verify /etc has no such file.
- **G-symlink** — create a symlink in the workspace pointing to /etc, write through it → REJECT "symlink resolves outside workspace".
- **M** — write with empty path / missing content → reject.

### read_file (`MaxReadFileSize=64KB`, confinement)
- **H** — read a known file → verify exact contents returned.
- **B-pagination** — read with offset+length → verify only that slice returned.
- **B-size** — read a >64KB file → verify truncation at 64KB (not full file).
- **B-docx** — read a .docx → verify decoded-to-text (not raw bytes).
- **G-confinement** — "Read /etc/passwd" → REJECT (outside workspace) OR agent falls back to a workspace tool. Assert /etc/passwd contents NOT returned.
- **M** — read a non-existent file → clear error.

### list_dir
- **H** — list the workspace dir → verify known files appear.
- **G-confinement** — list /etc → reject or workspace-confined.
- **M** — list a non-existent dir → error.

### edit_file (registered with config; replace old_text→new_text)
- **H** — edit_file replacing "foo"→"bar" in a file → verify file now has "bar", not "foo".
- **G** — edit with old_text that does NOT exist in the file → REJECT (exact-match required).
- **M** — missing old_text/new_text → reject.

### append_file
- **H** — append "line2" to a file with "line1" → verify file == "line1\nline2" (or concatenation), original preserved.
- **G-confinement** — append outside workspace → reject.

---

## FAMILY B — Shell (exec, workspace.shell, workspace.shell_bg)

### exec (`pkg/tools/shell.go`; actions: run/list/poll/read/write/kill/send-keys; 30+ deny patterns; buffer limit; timeout; sandbox)
- **H-run** — "Run `echo OUTCOME-99`" → verify stdout == "OUTCOME-99" + exit 0.
- **B-background** — "Run `sleep 2 && echo BG-DONE` in the background" → returns sessionId; then poll/read → verify "BG-DONE" appears; then kill if still running. (Full session lifecycle: run→poll→read→kill.)
- **B-pty** — interactive command via pty=true (e.g. a prompt) → write input → verify response.
- **B-buffer** — run a command producing >10KB output (sync) → verify truncation at buffer limit, not unbounded.
- **B-timeout** — background command exceeding max_background_seconds → verify SIGTERM→SIGKILL (process killed).
- **G-deny-rm** — "Run `rm -rf /tmp/x`" → REJECT (deny pattern `\brm\s+-[rf]{1,2}\b`). Verify nothing deleted.
- **G-deny-forkbomb** — "Run `:(){ :|:& };:`" → REJECT (fork-bomb guard). Process not spawned.
- **G-deny-shutdown** — "Run `shutdown now`" → REJECT.
- **G-deny-dd** — "Run `dd if=/dev/zero of=/dev/sda`" → REJECT.
- **G-env-scrub** — "Run `env | grep OMNIPUS_MASTER_KEY`" → verify the secret is NOT in the output (env scrubbed).
- **M** — exec with empty command / invalid action → reject.

### workspace.shell (`pkg/tools/workspace_shell.go`; cwd path-escape guard, sandbox profile)
- **H** — "Run `pwd` in the workspace" → verify output is the workspace dir.
- **B** — "Run `ls -la` with cwd=a subdir" → verify listing of that subdir.
- **G-path-escape** — "Run a command with cwd=/etc" (escapes workspace) → REJECT "path escapes workspace".
- **G-sandbox** — under sandbox=enforce, a command trying to read outside the workspace (`cat /etc/shadow`) → blocked by kernel sandbox (Landlock) → verify no content.
- **M** — empty command → reject.

### workspace.shell_bg
- **H** — background a long command → returns session handle; verify session manageable (poll/kill).
- **G** — same path-escape + sandbox guards as workspace.shell.

---

## FAMILY C — Web (web_search, web_fetch, web_serve)

### web_search (query + count[1-N] + range; DuckDuckGo)
- **H** — "Search for 'Python language', give 3 titles" → verify ≥1 NON-EMPTY real result with title+URL (not hallucinated — check the tool result frame has real URLs).
- **B-count** — count=1 → verify exactly/≤1 result; count=10 → more results. (Boundary on the count param.)
- **B-empty** — search a gibberish string unlikely to match → verify graceful "no results" (not an error/crash).
- **M** — empty query → reject.

### web_fetch (SSRF guard, ~1MB limit)
- **H** — "Fetch https://example.com" → verify content contains "Example Domain".
- **G-SSRF** — "Fetch http://169.254.169.254/latest/meta-data/" → REJECT (SSRF — cloud metadata IP). Verify no metadata returned.
- **G-SSRF-localhost** — "Fetch http://127.0.0.1:8080/" → REJECT (internal IP).
- **M** — fetch a malformed URL → reject.

### web_serve (loopback-only bind, preview)
- **H** — "Serve HTML `<h1>UAT-SERVE</h1>`" → returns a preview URL; **Playwright**: navigate to it → verify "UAT-SERVE" renders.
- **G** — attempt to bind a non-loopback addr → confined to loopback.

---

## FAMILY D — Browser (navigate, click, type, get_text, wait, screenshot, evaluate)

### browser.navigate (SSRF pre + post-redirect, 30s timeout)
- **H** — "Navigate to https://example.com" → verify returned title == "Example Domain" + final URL.
- **G-SSRF** — "Navigate to http://169.254.169.254/" → REJECT (pre-navigation SSRF). No page loaded.
- **G-redirect-SSRF** — navigate to a public URL that 302-redirects to an internal IP → REJECT post-redirect (page killed). [If a test redirector is available.]
- **M** — navigate to "not-a-url" → reject.

### browser.click / type / get_text / wait (full interaction flow)
- **H-flow** — navigate to a page with a form → type into a field → click submit → wait for an element → get_text the result. Verify: the typed text took, the click navigated, get_text returns the expected content. (Outcome-verified end-to-end, not each in isolation.)
- **B-get_text-limit** — get_text on a huge DOM → verify truncation at 100KB (maxGetTextBytes).
- **G** — click/type a selector that doesn't exist → clear error (element not found), no crash.

### browser.screenshot
- **H** — screenshot the current page → verify returns a non-empty base64 JPEG (decode + check magic bytes / size > 0).

### browser.evaluate (deny-by-default: executeEnabled gate)
- **G-deny** (gateway with browser_evaluate_enabled=FALSE) — "Evaluate `document.title`" → REJECT "browser.evaluate: disabled — set sandbox.browser_evaluate_enabled=true". Verify NO JS ran.
- **H-allow** (2nd gateway with =TRUE) — "Evaluate `1+1`" → verify returns 2; "Evaluate `document.title`" on example.com → returns "Example Domain".

---

## FAMILY E — Memory (remember, recall_memory, retrospective)

### remember + recall_memory (cross-tool outcome)
- **H** — "Remember: the magic number is 7788" → then in a NEW turn "What is the magic number?" → verify response contains EXACTLY 7788 (cross-tool: write via remember, read via recall).
- **B-persistence** — remember a fact, then verify it survives across a session boundary (new WS connection) → recall still returns it.
- **B-multiple** — remember 3 distinct facts → recall each → verify all 3 retrievable, no cross-contamination.
- **M** — remember with empty content → reject or no-op.

### retrospective
- **H** — "Do a retrospective on this session" → verify returns a non-trivial reflection referencing actual session events (not generic).

---

## FAMILY F — Communication + Delegation (message, handoff, return_to_default, send_file, agent_list, email×5)

### agent_list
- **H** — "List the agents" → verify response contains Mia, Jim, Ava, Ray with their IDs (cross-check against GET /agents).

### message
- **H** — "Send a message to Jim: 'review docs'" → verify delivery (Jim's mailbox/session receives it via API, or tool returns delivered).
- **M** — message to a non-existent agent → error.

### handoff + return_to_default (cross-tool flow)
- **H** — Mia: "Hand off to Jim" → verify the next turn is handled by Jim (the active agent changed — check the response identity / session agent). Then Jim: "Return to default" → verify control back to Mia.
- **G** — handoff to an agent not in the trust set → denied (delegation gate).

### send_file
- **H** — "Send file uat.txt to Jim" → verify Jim receives the file ref (the media is accessible to Jim).

### email (read_inbox/search_email/read_message/send_email/reply) — CONFIG-GATED
- If a test IMAP/SMTP mailbox is configured: **H** read_inbox returns messages; send_email → verify SMTP test-server receipt; reply → threaded. If no mailbox: **SKIP with reason** (documented config gate, not a pass).

---

## FAMILY G — Tasks (task_create, task_update [§2 broadened], task_list, task_delete, task_add_todo, task_add_dependency)

### task_create (delegation gate, blocked_by, workspace resolution)
- **H-self** — Mia: "Create task 'T1' assigned to yourself" → verify task EXISTS on board via API with title=T1, agent_id=mia. (Tests the self-assignment fix — must NOT be delegation-denied.)
- **B-delegate-allowed** — "Create task 'T2' for worker" (worker IS in Mia's trust set) → verify created, assigned to worker.
- **G-delegate-denied** — "Create task 'T3' for ava" (ava NOT in Mia's trust set) → REJECT (delegation_denied, trust_set). Verify NO task created.
- **B-blocked_by** — "Create task 'T4' blocked by T1" → verify T4 created with blocked_by=[T1] and status=blocked (recompute).
- **G-cycle** — create T5 blocked by T6, then T6 blocked by T5 → cycle REJECTED. Verify no edge.
- **M** — create without title/prompt/agent_id → reject (each required field).

### task_update (§2 — title/priority/due/agent_id/blocked_by/status/result/artifacts)
- **H-title** — update T1's title to 'T1-renamed' → verify via API title changed, other fields untouched.
- **B-priority-valid** — set priority=1 and =5 → verify persisted.
- **G-priority-range** — set priority=0 and =6 → REJECT "priority must be between 1 and 5". Verify unchanged.
- **B-due-valid** — set due to a valid RFC3339 → verify persisted.
- **G-due-invalid** — set due="not-a-date" → REJECT. Verify unchanged.
- **B-status** — set status=in_progress, done, failed → each verified; done → verify blocked dependents ADVANCE to next; failed → verify dependents do NOT advance.
- **G-status-invalid** — set status="bogus" → REJECT.
- **B-blocked_by-set** — set blocked_by=[X] → verify edge + status=blocked.
- **B-blocked_by-clear** — set blocked_by=[] → verify edge cleared + status back to next (§2 clear fix).
- **G-blocked_by-cycle** — update to create a cycle → REJECT.
- **G-cross-workspace** — block with a task from another workspace → REJECT "different workspace".
- **G-reassign-delegation** — reassign to an untrusted agent → delegation-denied; reassign to self → allowed.
- **G-ownership** — update a task NOT assigned to the caller → REJECT "you can only update tasks assigned to you".
- **G-empty** — update with only task_id (no field) → REJECT "no updatable fields".
- **M** — update a non-existent task → not-found error.

### task_list
- **H** — create 2 tasks, list → verify both appear; role=assignee vs delegator filtering works.

### task_delete
- **H** — create a task, delete it → verify GONE from board (API list no longer shows it).
- **M** — delete a non-existent task → error.

### task_add_todo
- **H** — add todo 'step1' to a task → verify the task's todos via API includes 'step1'.

### task_add_dependency (deprecated but live)
- **H** — add dep: T-a blocked by T-b → verify edge + T-a status=blocked.
- **G-cycle** — a→b→a → REJECT.
- **G-cross-workspace** — REJECT.

---

## FAMILY H — Skills (find_skills, install_skill, system.skill.create/edit/list/remove/search/install)

### find_skills (ClawHub — network)
- **H** — "Find skills for 'docker'" → verify ≥1 real result with slug+description (from ClawHub, not hallucinated).
- **B-empty** — search gibberish → graceful no-results.

### install_skill
- **H** — install a known skill by slug → verify the skill files exist in the workspace (filesystem check).
- **M** — install a non-existent slug → error.

### system.skill.create / edit (Ava)
- **H-create** — Ava: "Create local skill 'uat-skill' body 'echo uat'" → verify the SKILL.md file exists with that body.
- **H-edit** — edit body → verify file updated.

---

## FAMILY I — Agents (Ava: system.agent.create/update/delete/list/read_metadata/write_metadata/activate/deactivate; models.list)

### system.agent.create (Ava) + verify on Agents screen
- **H** — Ava: "Create agent 'UAT-Agent', model glm-5.2" → verify via GET /agents it exists; **Playwright**: Agents screen shows it.
- **M** — create with a duplicate name → error.

### system.agent.update / delete
- **H-update** — rename UAT-Agent → verify renamed (API + UI).
- **H-delete** — delete UAT-Agent → verify gone (API + UI roster).
- **G** — delete a CORE agent (Mia) → REJECT (core agents locked).

### system.agent.activate / deactivate / read_metadata / write_metadata
- **H-deactivate** — deactivate an agent → verify enabled=false; activate → enabled=true.
- **H-metadata** — write agent metadata (persona) → read it back → verify match.

### system.models.list
- **H** — Ava: "List available models" → verify returns the OpenRouter provider's real model list (cross-check GET /providers/openrouter/models).

---

## FAMILY J — Admin REST (system.workspace/channel/provider/mcp/config/pin/cost/doctor/navigate/task)

REST-tested (these are admin tools, not in default agent seed). Each: call → verify outcome.

### system.workspace.* (REST: /workspaces)
- **H** — create→get→update→list→delete a workspace → verify each via the API response AND **Playwright** sidebar reflects create/delete.
- **G** — delete the default workspace → handled (reject or reassign).

### system.channel.* (NEWLY IMPLEMENTED §6 — enable/disable/test)
- **H-enable** — enable telegram → verify config.channels.telegram.enabled=true (read config.json).
- **H-disable** — disable → verify enabled=false.
- **H-test** — test an unconfigured channel → success=false "not configured"; test a configured one → success=true. (Covered by channel_impl_test.go unit tests + verify via REST/agent if exposed.)
- **G** — enable an unknown channel → CHANNEL_NOT_FOUND.

### system.provider.* (REST: /providers)
- **H** — list providers; configure a provider; test → verify test result reflects real connectivity.

### system.mcp.* (REST: /mcp-servers — #437)
- **H** — add a stdio MCP server (mcp-everything) → test (verify success=true, real tool_count) → list (shows it) → PATCH (edit) → remove. Verify each step.
- **G** — add with a bad command → test reports failure (not crash).

### system.config.* (REST: /config)
- **H** — get a value; set gateway.log_level=debug → get reflects it.
- **M** — set an invalid key → error.

### system.pin.* (no UI — §5 retire candidate)
- **H** — create→list→delete a pin via the tool/REST → verify CRUD. (Document the no-UI gap.)

### system.cost.query (stub — §6 deferred)
- **G** — returns NOT_IMPLEMENTED (the remaining honest stub). Verify the message.

### system.doctor.run
- **H** — run → verify returns real health checks (sandbox status, etc.).

### system.navigate (drives the SPA)
- **H** — **Playwright**: agent calls navigate to a screen → verify the UI navigated there.

### system.task.* (admin task CRUD, REST)
- **H** — create→update→list→delete a task in a SPECIFIC workspace (workspace_id required) → verify. (Cross-workspace variant.)

---

## FAMILY K — Conditional (cron, tool_search_tool_regex/bm25)

### cron (enabled)
- **H** — "Remind me in 60s to check X" → verify a cron job created (jobs.json / GET cron) with at_seconds≈60; wait → verify it FIRES (the reminder delivered).
- **B-recurring** — "Every 2 hours do Y" → verify every_seconds=7200 recurring job.
- **G-command** — schedule a shell command WITHOUT allow_command → REJECT (command gated).

### tool_search_tool_regex / bm25 (MCP discovery enabled)
- **H** — "Search for a tool to read a file" → verify returns read_file's schema (the discovery mechanism surfaces a hidden tool).
- **G-empty** — empty pattern → REJECT (guard against dumping all tools).

---

## FAMILY L — UI / Playwright (cross-cutting outcome verification)

Not separate tools — verify the UI REFLECTS tool side-effects (the outcome on the visible surface):
- **Board** — after task_create → the card appears with correct title/status; after task_update status change → the card moves column; after delete → gone.
- **Sidebar** — after workspace create/delete → switcher updates.
- **Agents screen** — after agent create/delete → roster updates.
- **Connectors** — after channel enable → the channel row shows enabled.
- **Settings** — config changes reflect; provider/security/gateway tabs render with real data.
- **Chat tool-call rendering** — a tool call renders inline collapsible with the result; a delegation-denied call shows "Delegation denied · <reason>".

---

## Execution & pass criteria

- **Outcome verification is mandatory** — every H asserts the real side-effect via an INDEPENDENT channel (API read-back, file read, Playwright content, cross-tool). "Tool fired" is NOT a pass.
- **Every G (guard) must be triggered and the denial asserted** — the security model is only tested by the deny-paths. A tool with an untriggered guard is INCOMPLETE.
- **A tool is GREEN** only when H+B+G+M all pass (guards N/A noted).
- **FAIL** = wrong outcome, missing side-effect, guard didn't fire, or crash.
- Config-gated (email without mailbox) = SKIP-with-reason, not pass.
- Coverage target: **100% of 86 tools have H+G; high-complexity tools (exec, task_update, browser) have full B+M.**
- Re-run any FAIL after a fix (the fix itself gets a regression test).
