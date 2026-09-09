# UAT Plan — release/v0.1.1 defect-fix pass (ToolSearch policy seeding + 8 other UAT-found defects)

Date: 2026-09-01
Branch under test: `release/v0.1.1`
Commit under test: `f101a9b4` (or later, once any defect from this round lands)
Methodology template: `/Users/danielpiatkowski/Desktop/omnipus-uat-tester-prompt-rewritten.md` (the superseded original lacked the disposable-resource safety protocol — do not use it)

## Why this round exists

A live UAT pass found 9 real defects across Omnipus's tool catalog. All 9 were fixed and committed directly to `release/v0.1.1` (7 commits: `cf1299df`, `5aca35cc`, `95d3b818`, `ac6e83cd`, `c511dad6`, `6c5e7671`, `81f7ef26`), plus one CI-caught regression fix (`f101a9b4`) in the fixes themselves. CI (`go-build`, `go-vet`, `go test`, `go-race`, `e2e`, `contracts`, `spa`, `gofmt`) has been re-run and is green or in progress against the latest commit — **but CI only proves the code is internally consistent. It does not prove a real agent, running inside a real gateway, actually gets the fixed behavior at runtime.** The specific failure mode CI cannot catch: seeded default policy data (config.json / defaults.go / coreagent seeding) silently not reaching a live agent's resolved tool policy, which is exactly the shape of the original defect (an agent reporting it has no `ToolSearch`/`load_tool` despite the code believing it seeded one). This UAT round exists to close that gap.

## CRITICAL SAFETY RULES — read before running anything

1. **Never touch `~/.omnipus`.** That is the founder's real, currently-running production install (`omnipus start`, real gateway process, real config, real credentials, real agents). Do not read, write, or start a gateway against it. Do not reuse its port.
2. **Use a fully isolated, throwaway `$OMNIPUS_HOME`** — e.g. `/tmp/omnipus-uat-<timestamp>` — created fresh for this run, on a **different port** than the real install (check `lsof -i` first; do not assume 5000 is free — it is not, macOS ControlCenter squats it).
3. **Before any destructive test** (delete, remove_skill, config overwrite, sandbox-boundary probing, circuit-breaker-triggering repeated failures): create a **disposable throwaway workspace and a disposable `Subagent`-type agent** inside the UAT `$OMNIPUS_HOME` first. Every destructive scenario in this plan targets that disposable agent/workspace, never the UAT-tester agent's own identity, never any core agent (Mia/Jim/Ava/Ray) except where a scenario specifically requires probing a *locked* core agent's delete-protection (read-only assertion, not an actual deletion attempt that could succeed).
4. **Test every filesystem/sandbox-relevant claim two ways where applicable**: via the UAT-tester's own tool call, AND via a raw shell command in the same sandboxed context, when both paths exist. A refusal is not a pass unless it reproduces on a second attempt.
5. **Evidence, not conclusions.** Every scenario result must paste the actual tool-call arguments and the actual tool result / error text — never a bare "worked" / "blocked correctly."
6. **End with cleanup confirmation**: every disposable resource created for this run is deleted, and the report says so explicitly with evidence (e.g. a follow-up list-agents/list-workspaces call showing it's gone).

## Environment setup (do first, record exact commands + output in the report)

```bash
# Isolated home, isolated port — DO NOT reuse the real install's port/home
export OMNIPUS_HOME=/tmp/omnipus-uat-$(date +%Y%m%d-%H%M%S)
mkdir -p "$OMNIPUS_HOME"
# Pick a free port (check lsof -i first); do not assume 5000
```

Build from the exact commit under test (SPA embed pipeline, per CLAUDE.md):
```bash
npm run build && rm -rf pkg/gateway/spa && cp -r dist/spa/* pkg/gateway/spa/
CGO_ENABLED=0 go build -tags goolm,stdjson -o /tmp/omnipus-uat-bin ./cmd/omnipus/
```

Start the gateway against the isolated home on the isolated port, with a real tool-capable model (CLAUDE.md names `z-ai/glm-5-turbo`, `google/gemini-2.5-flash`, or `anthropic/claude-3.5-haiku` as known-good; `OPENROUTER_API_KEY` is available in this shell environment). Confirm which credential path is actually usable before proceeding — if none work, stop and report the blocker rather than fabricating a scripted-provider substitute silently.

## Creating the UAT-tester agent

Create a **new custom agent** (not one of the 4 locked core agents) via the REST API, named e.g. `uat-tester`, with an **explicit, wildcard-free tool policy** granting `allow` on the full static builtin catalog (per CLAUDE.md hard constraint #6 — no wildcards; enumerate every tool explicitly, mirroring the seeded default policy set already used for e.g. Jim). Confirm explicitly that `ToolSearch` resolves to `allow` for this agent with **no manual per-agent override beyond what a fresh custom agent gets from the seeded defaults** — that is the actual regression surface: does a newly created agent get `ToolSearch: allow` for free from the install-time seed, or does it silently fail closed the way the original bug did?

## Scenarios

Each scenario is driven as a real chat turn to the live `uat-tester` agent (not a Go unit test, not a mocked provider) — the point is to observe genuine LLM tool-selection behavior against the real, running tool surface.

### S1 — ToolSearch is actually reachable (the core regression this round exists to catch)
Ask the agent something that requires a tool it wasn't given full upfront schema for, and confirm it discovers and loads it via `ToolSearch` rather than reporting "I don't have that tool" (the exact original failure mode). Paste the actual tool_call/tool_result pair.

### S2 — retired `load_tool` gives actionable guidance
Ask the agent to use `load_tool` directly. Expect the new actionable retired-tool error naming `ToolSearch` as the successor (commit `95d3b818`), not a bare "unknown tool" error.

### S3 — browser tool round-trip, SEC-25 wrapper unwrapped correctly
Drive `browser_open_tab` → `browser_switch_tab` → `browser_close_tab` in sequence (real Chrome via chromedp — confirm Chrome is available first). Confirm the tool_result JSON parses cleanly with no leaked SEC-25 untrusted-content wrapper markers (commit `cf1299df`). Test twice if the first attempt is ambiguous.

### S4 — `set_config` string-encoded bool coercion
Ask the agent to set a boolean config field using the string `"true"` (not a JSON bool). Confirm it's stored as a real bool, not a truthy string, by reading it back (commit `5aca35cc`).

### S5 — agent-delete on a locked core agent
Ask the agent to attempt deleting a **locked core agent** (Mia). This must be **read-only in effect** — expect an `AGENT_LOCKED` error, and confirm via a follow-up list-agents call that the entity still exists (commit `ac6e83cd`). Do not attempt this against any disposable/custom agent as a substitute — the locked-agent path is specifically what changed.

### S6 — Google Chat `chat_id` double-prefix
Code-path-only unless a real Google Chat credential is available in this environment. If not independently live-testable, say so explicitly in the report rather than skipping silently, and note it's covered by the existing unit test only (commit `95d3b818`).

### S7 — sandboxed shell actually usable (symlinked-home kernel-deny fix, proxy check)
Have the agent run a simple `bash` command that reads/writes inside its own workspace (e.g. `pwd`, write+read a scratch file). This machine's `/tmp` is a symlink (macOS firmlink), the exact condition commit `c511dad6` fixes — confirm the sandbox does **not** cascade-deny system paths (no `EPERM` on ordinary commands like `ls`, `pwd`). Test twice via both the agent's own shell tool call and, if feasible, an equivalent raw invocation.

### S8 — tool-failure circuit breaker
Have the agent attempt a tool call with deliberately malformed/failing arguments **6+ times in a row on the disposable agent** (never on the UAT-tester's own identity, to avoid tripping its own usable tool surface for the rest of the run — use the disposable agent from the safety-setup step for this one). Confirm a circuit-breaker denial kicks in after the threshold rather than looping indefinitely (commit `6c5e7671`). Paste the exact point at which the denial starts.

### S9 — skill install/remove correctness + ownerHandle
Install a real skill, confirm it's listed, then remove it via `remove_skill` and confirm it is actually gone from the **global** skills directory (not just deregistered) — this was the exact defect in commit `81f7ef26` (wrong root directory). Also exercise `install_skill` with an `ownerHandle` argument if a scoped registry is configured; if none is configured, say so and mark not independently testable.

### S10 — general tool-surface sweep
Have the agent attempt at least 15–20 read-only/no-side-effect invocations spanning file, browser, config, skill, agent-management, and memory tool categories. Confirm no crashes, no unexpected denials, no schema-validation failures. List exactly which tools were exercised.

### S11 — negative control: explicit deny still works
Temporarily set an explicit `deny` on one otherwise-allowed tool for the disposable agent, confirm it is refused cleanly (not silently allowed, not a crash) — this proves the policy engine's deny path wasn't broken by the seeding fix while making the allow path work.

## Report deliverable

Write `docs/internal/qa/uat-report-release-v0.1.1-toolsearch-defects-2026-09-01.md` following the rewritten template's fixed structure:
1. Verdict (2–3 sentences: ship or not, and the one blocking reason if not).
2. Anything that got through / regressed (should-be-fixed-but-isn't) — first, unsoftened.
3. Anything that should work and doesn't (usability regressions).
4. Two-layer comparison table where applicable (tool-path vs shell-path).
5. What couldn't be tested and why (e.g. S6 if no Google Chat credential).
6. Cleanup confirmation — every disposable resource deleted, with evidence.

## Acceptance

Every scenario is a clear PASS with pasted evidence, or every FAIL has a genuine root-cause investigation (per standing project instruction: never dismiss as pre-existing/flaky), a committed fix on `release/v0.1.1` with correct human authorship (no agent co-author trailer — see CLAUDE.md "Git commit authorship"), and independent re-verification of that specific fix.
