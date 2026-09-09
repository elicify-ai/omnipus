# UAT Report — Full Tool Catalog, batch1 (Groups A–D) — 2026-09-02

**Scope note:** this is **batch1 of 4** covering only **Groups A–D** (Filesystem S1–S8, Shell/exec
S9–S13, Web/network S14–S18, Browser automation S19–S24b) of
`docs/internal/qa/uat-plan-full-tool-catalog-2026-09-02.md`. Scenario ids S25 onward (Groups E–V)
are **out of scope for this report** and are not adjudicated here.

**Build under test:** commit `362129a7e52e8c05c87e1630d4ddb4b7ca511d00` on `release/v0.1.1` — one
commit past the plan's cited `16dee850`, and that one commit is the plan document itself (docs-only;
no `pkg/`/`src/` diff between the two). All evidence in this report shares this one sha.

**Environment:** macOS host (darwin). `npm run build` exit=0, SPA synced to `pkg/gateway/spa/`,
`CGO_ENABLED=0 go build -tags goolm,stdjson` exit=0. Embed check:
`pkg/gateway/spa/assets/index-BaSRg6aR.js` non-empty (`grep -c "" ` = 2). Model:
**z-ai/glm-5-turbo via OpenRouter** (`OPENROUTER_API_KEY` from the environment) — every live-model
result below is a statement about this model, not models in general. Onboarded headless via
`omnipus onboard --non-interactive`. Isolated `$OMNIPUS_HOME` under this session's scratchpad
directory, port 19231 (confirmed free before use via `lsof`). The real, currently-running
production `~/.omnipus` install was confirmed running separately on ports 52711/10994 — **never
touched, never reused**, beyond one accidental read-only `ls` at the very start of the session
before its nature was recognized (no writes, no gateway start against it, no file contents read).

Two disposable Main-type tester agents were created with an explicit, wildcard-free 88-tool policy
(CLAUDE.md Constraint #6): `uat-fullcatalog-tester-batch1-main` (all "allow") and
`uat-fullcatalog-tester-deny-batch1-main` (all "allow" except `bash`="deny", for S13). Two earlier
Subagent-type agents were also created but discovered to be unusable as direct chat targets
("this agent is a worker and cannot be a chat target") — they were never driven and were deleted
at cleanup alongside the working Main-type pair. A disposable workspace
(`uat-fullcatalog-batch1-workspace`) held both testers on its `core_team`; its `work/` directory is
the shared filesystem root both agents' `write_file`/`read_file`/`bash`/`list_directory` calls
operate against (workspace-scoped, not per-agent). A local `python3 -m http.server` served
`$UAT_MOUNT_ROOT` on port 19232 for the fixture-server scenarios.

---

## 0. Scenario ledger

28 scenario ids in this batch's scope (S1–S24 plus lettered sub-scenarios S6b, S17b, S22b, S24b).

| ID | Verdict | One-line result | Evidence |
|---|---|---|---|
| S1 | PASS | write_file/read_file exact round-trip incl. UTF-8 + raw control byte, 54 bytes | S1_result.json |
| S2 | PASS | list_directory exact 2-entry listing; clean not-found on nonexistent path | S2_result.json |
| S3 | PASS | edit_file targeted replace (byte-count diff proves not a rewrite); stale old_string fails cleanly, file intact | S3_result.json |
| S4 | PASS | append_file preserves existing content; append-to-nonexistent creates it | S4_result.json |
| S5 | PASS | library_list/library_read round-trip an out-of-band upload; both traversal probes cleanly refused | S5_result.json |
| S6 | **FAIL (CRITICAL)** | request_mount fails with "this turn has no workspace to mount into" on its own mandatory post-approval execution path — reproduced twice; no mount ever created; real host folder never touched | S6_redo_result.json, S6_repro_result.json, gateway.stdout.log |
| S6b | BLOCKED | S6's prerequisite (a working mount) never succeeds, so the unmount/real-folder-safety check has nothing to test | (see S6) |
| S7 | PASS, loud caveat | relative traversal clamped; absolute-path read of an ungranted external file **succeeds** — confirmed live as the DOCUMENTED default `filesystem_model=open` behavior (ADR-062); symlink-write escape blocked by bash's guard | S7_result.json, sandbox_status_check.json |
| S8 | PASS | read_file and `bash cat` agree (both succeed, byte-identical content) on the same ungranted target — no disagreement | S7_result.json (probe 2), S12_result.json |
| S9 | PASS | bash("echo ...") exact stdout match | S9_result.json |
| S10 | PARTIAL | dispatch + poll sub-cases clean; read sub-case inconsistent — an explicit `read` after the async completion push already delivered output returns `status:"timeout"` with no output, contradicting `poll`'s "done" moments earlier | S10_result.json, S10b_result.json, S10c_result.json |
| S11 | PASS | UAT_SECRET_PROBE (present in the launching shell) absent from `bash`'s own `env` (~17 vars only) | S11_result.json, S11_outer_shell_env.txt |
| S12 | PASS | bash `cat <abs path>` succeeds (open-reads model); `cd <abs outside dir> && cat` blocked | S12_result.json |
| S13 | PASS | deny-policied `bash` refused at ToolSearch load time: "denied by this agent's policy" | S13_result.json |
| S14 | PASS | search_web real DuckDuckGo results, title/url/snippet shape | S14_result.json |
| S15 | PASS, loud caveat | fetch_url refuses the local fixture — **documented, intentional** ("you cannot fetch a local dev server... use the browser tools instead", `pkg/tools/web.go`) | S15_redo_stdout.log |
| S16 | PASS | both 169.254.169.254 and gateway self-fetch refused identically via fetch_url | S16_result.json |
| S17 | PASS | static preview URL returned and independently verified reachable, byte-exact README.md content | S17_result.json, curl checks |
| S17b | N-A-ENVIRONMENT | "Tier 3 dev servers are Linux only" — confirmed live, macOS host | S17b_redo_result.json |
| S18 | N-A-ENVIRONMENT (partial) | same Linux gate fires before the port-range validator is ever reached — port=99999 never actually tested | S18_result.json |
| S19 | PASS | navigate/get_text/screenshot against real content (local fixture SSRF-blocked, substituted example.com); JPEG magic bytes FF D8 FF confirmed on disk | S19_redo_result.json |
| S20 | PASS | browser_type succeeds; browser_click succeeds via text-match once the real page markup was confirmed (httpbin.org/forms/post substituted for the SSRF-blocked local fixture) | S20_redo_result.json, S20_retry3_result.json |
| S21 | PASS | browser_wait succeeds on the delayed element (`{"found":true}`) and times out cleanly on one that never appears | S21_redo2_result.json, S21_result.json |
| S22 | PASS | browser_evaluate denied by default, script did not execute | S22_result.json |
| S22b | PASS | flag flip live (direct config.json edit, no restart) → executes and returns real result `{"result":2}`; flip back → denied again | S22b_enable_result.json, S22b_disable_result.json |
| S23 | PASS, loud caveat (characterization) | 169.254.169.254 refused; gateway's own origin (localhost:19231, ANY path) resolves and loads real content — **documented** ADR-044 D2 gateway-origin exception, host:port-scoped not path-scoped | S23_result.json, ssrf.go citation |
| S24 | PASS | full tab lifecycle: open/list/switch/get_text-confirms-switch/close, all exact | S24_redo_result.json |
| S24b | PASS | closing the last tab replaces it with a fresh blank tab, never zero tabs | S24b_redo_result.json |

**Counts (28 scenario ids total: S1–S24 = 24, plus lettered sub-scenarios S6b/S17b/S22b/S24b = 4):**

- **PASS = 23**: S1, S2, S3, S4, S5, S7, S8, S9, S11, S12, S13, S14, S15, S16, S17, S19, S20, S21,
  S22, S22b, S23, S24, S24b — of which 4 (S7, S15, S23, and implicitly S6b's situation) carry a
  "loud caveat" documented-behavior note, spelled out per-scenario below and in §2.
- **FAIL = 1**: S6 (CRITICAL).
- **PARTIAL = 1**: S10.
- **BLOCKED = 1**: S6b.
- **N-A-ENVIRONMENT = 2**: S17b, S18.
- **NOT RUN = 0**.

23 + 1 + 1 + 1 + 2 + 0 = **28** ✓.

---

## 1. Verdict

23 of 28 scenarios PASS (4 of those with a loud, load-bearing caveat that the literal fail-condition
text technically fired but the behavior matches documented, intentional design — S7, S15, S23, and
by extension S6b's blocked status is a consequence of S6, not an independent failure). **1 CRITICAL
FAIL (S6): `request_mount` — the only tool that grants write access to a real folder on the
operator's machine — is non-functional.** It fails every time on its own mandatory approval path
with `request_mount: this turn has no workspace to mount into`, reproduced twice in isolated
single-call turns. **1 PARTIAL (S10):** background-bash's `read` action returns a misleading
"timeout" status with no output for a session that `poll` simultaneously reports as "done." **2
scenarios are N-A-ENVIRONMENT** (S17b, S18 — Tier-3 dev-server mode is Linux-only, confirmed live,
not inferred). **1 is BLOCKED** (S6b — its prerequisite, S6, never produces a mount to test unmount
safety against). This round is **not** an unqualified pass: the CRITICAL S6 finding blocks a
documented, advertised capability (real-folder write access) end to end, and it is fair to say the
single highest-severity check this plan calls out (S6b, mount deletion never touching the real
folder) could not be run at all because the feature under test never succeeds far enough to reach
that check.

---

## 2. Anything that got through / regressed — unsoftened, first

### 2.1 CRITICAL — `request_mount` cannot ever succeed (S6)

`request_mount` always requires an interactive tool-approval (a gate separate from the coarse
allow/ask/deny static tool policy — confirmed even with policy `allow`, the call still paused with
a `tool_approval_required` WS frame and an `approval_id`, requiring
`POST /api/v1/tool-approvals/{id} {"action":"approve"}`, the exact documented mechanism the SPA
modal itself uses). After approving via that documented endpoint, `Execute()` runs and immediately
fails:

```
request_mount: this turn has no workspace to mount into
```

logged at `ERR` level, `pkg/tools/registry.go:572`, confirmed via `grep` on `gateway.stdout.log`.
The **same turn's** `list_mounts` calls immediately before and after correctly resolve the same
workspace (`"workspace_resolved_from":"agent_membership"`) — so workspace resolution demonstrably
works for other tools in that exact turn; only `request_mount`'s post-approval resumed execution
loses it. Reproduced a second time in a completely isolated, single-purpose turn (different target
folder, `S6_repro_result.json`) with byte-identical error text and log line. No REST workspace
mount record is ever created (`GET /workspaces/{id}` never returns a `mounts` field after either
attempt), and the real host folder is provably untouched both times (`ls` before/after — only the
pre-existing `marker.txt`). A subsequent `write_file` to the mount's intended alias name (e.g.
`mountgrant/probe_after_grant.txt`) reports success but silently lands in an **ordinary
in-workspace directory of the same name** (confirmed on disk under `workspaces/<id>/work/mountgrant/`),
not the real external folder — because `write_file` has no concept of "this name refers to
something external," it simply creates whatever relative path it's given.

**Net effect: in this build, an agent (or the operator through the same flow the SPA uses) can
never successfully grant a real-folder mount.** This is the tool CLAUDE.md's own ADR-063 citations
describe as the sole way to give an agent write access outside its workspace; it does not work.

### 2.2 Moderate — background `bash` read-after-async-push returns a misleading status (S10)

Twice, when a background job finished while the model was still mid-turn, the gateway proactively
injected an async completion notification (`"Background session <id> finished (exit code 0)...<real
output>"`) directly into the assistant's token stream and ended the turn — the model's own planned
`poll`/`read` calls never fired because the turn was already over, but the real output WAS
delivered correctly both times via this path. Once, in a genuinely later turn (~4.5 minutes after
dispatch, same session continued), an explicit `poll` call correctly reported `status:"done"`, but
an explicit `read` call moments later in the **same turn** reported `status:"timeout"` with **no
output field at all** — contradicting `poll`'s result from seconds earlier and never surfacing the
real, already-known output through the documented poll→read path. This reads to a caller as "the
command is still hanging" when it demonstrably finished cleanly. Full raw evidence across all three
attempts: `S10_result.json`, `S10b_result.json`, `S10c_result.json`.

### 2.3 Worth a design review, not a regression — SSRF's gateway-origin exception is host:port-scoped, not path-scoped (S23)

`browser_navigate` correctly refuses `169.254.169.254` (the cloud-metadata address). It does
**not** refuse `http://localhost:19231/api/v1/state` (the gateway's own port). This is
**documented, intentional** behavior (`pkg/security/ssrf.go`'s `AllowGatewayOrigin`/
`isAllowedGatewayOrigin`, cited explicitly as "ADR-044 D2 ... so the built-in browser can reach the
gateway's own preview URL") — but the match is on **host:port only, not path**, so the exception
covers the gateway's entire origin (any REST path it serves), not just `/preview/...`. The
practical blast radius in THIS test was limited to `/api/v1/state`, which is itself a deliberately
unauthenticated endpoint (`withOptionalAuth` per CLAUDE.md) — I did not test whether an
authenticated-only endpoint is reachable this way (browser navigation carries no Bearer header), so
I cannot say whether this widens real exposure beyond what an anonymous user could already see. This
is presented as a scope-widening design observation worth a second look, not as a plain FAIL —
S23's literal FAIL CONDITION text technically fired, but the mechanism is deliberate and cited.

### 2.4 Non-defect but worth recording — the plan's own local-fixture design is unreachable by two of the four tools it names

The plan's Environment Setup section built a local `python3 -m http.server` specifically "so it
doesn't depend on real internet reachability" and to "give a legitimate `fetch_url`/
`browser_navigate` target." Live testing found:
- `fetch_url` refuses ANY loopback/localhost target **by design** — its own `Description()` states
  "you cannot fetch a local dev server or preview URL with this tool; use the browser tools
  instead" (S15).
- `browser_navigate` ALSO refused the local fixture (both `127.0.0.1` and `localhost` forms) by the
  live default SSRF policy (S19/S20/S21/S24's first attempts) — contradicting fetch_url's own
  "use the browser tools instead" guidance, since the browser tools couldn't reach it either.
- An attempt to unblock this via the documented operator escape hatch
  (`PUT /security/sandbox-config {"ssrf_allow_internal": ["127.0.0.1/32"]}`, after the required
  `POST /auth/reauth` consent step) was confirmed **saved** (readable back via a subsequent GET) but
  **did not visibly take effect** — both `fetch_url` and `browser_navigate` continued to refuse the
  identical target immediately afterward, with no gateway restart performed. This contradicts the
  API's own documented claim that `ssrf.allow_internal` is "hot-reloaded." I did not root-cause this
  (out of my mandate) — flagging it as a secondary, worth-investigating observation, distinct from
  the tools' baseline SSRF-blocking behavior. The change was reverted (`ssrf_allow_internal: []`)
  before cleanup.
- The actual working path for local-content browser testing turned out to be `serve_web` (Tier 1
  static) + `browser_navigate` to the resulting `/preview/<agent>/<token>/...` URL — which lands on
  the gateway's OWN origin and is therefore covered by the §2.3 exception. This is very likely the
  INTENDED workflow (ADR-044's whole point), but the plan's fixture design (an independent
  `python3 -m http.server`) does not actually connect to it, and I had to substitute either real
  external sites (example.com, example.org, httpbin.org) or the `serve_web`/`/preview/` path to get
  usable evidence for S19–S21/S24/S24b. Substitutions are noted individually in the ledger and in
  each scenario's own evidence file.

---

## 3. Anything that should work and doesn't (usability regressions)

- **S6 (see §2.1)** is the standout: a documented, advertised capability is completely non-functional.
- **S10's read-after-async-push (see §2.2)**: an operator/agent who doesn't catch the async push in
  the same turn has no reliable way to retrieve a background command's output afterward — `read`
  reports a status that reads as "still running" when the job is long finished.
- Minor, non-blocking observations from driving the tools (not separately scenario-scored):
  `browser_click`/`browser_get_text`/`browser_wait` all return a clean, specific
  `"'selector' parameter is required"` error when the model omits the parameter — good error
  quality, but the model (not the tool) repeatedly omitted it across several turns, which is a
  model-fidelity note, not a product defect, and is why several transcripts show 3–6 repeated
  identical failed calls before the gateway's own repeated-failure guard rail
  (`[SYSTEM NOTICE: this exact call has now failed N times in a row...]`) nudged it to change
  approach — that guard rail itself worked exactly as intended and is worth a positive mention.

---

## 4. Two-layer comparison table (S8 — filesystem claims tested both via tool call and via `bash`)

| Target | Via `read_file` | Via `bash` | Agree? |
|---|---|---|---|
| `$UAT_CANARY_ROOT/canary.txt` (absolute path, never mounted/granted) | Succeeds, returns real content verbatim (S7 probe 2) | `cat <abs path>` succeeds, returns real content verbatim (S12 call 1) | **Yes** — both allow, both return byte-identical content |
| `$UAT_CANARY_ROOT` via a `cd`-based approach | N/A (read_file has no `cd` concept) | `cd <abs outside dir> && cat canary.txt` — BLOCKED by the safety guard ("path outside working dir") | N/A — this is a bash-internal distinction (a bare-argument read vs. a working-directory change), not a read_file-vs-bash disagreement |
| `../../../etc/passwd`-shaped relative traversal | Clamped inside the workspace root, clean "no such file or directory" (S7 probe 1) | Not separately probed via bash in this batch | — |

**No CRITICAL two-layer disagreement was found.** `read_file` and `bash cat` agree consistently: both
implement the confirmed-live `filesystem_model="open"` default (reads outside the workspace are
allowed by design, per `request_mount.go`'s own doc comment: "Everything outside your workspace is
already readable; ask only when you need to WRITE" — and ADR-062 as summarized in CLAUDE.md). The
one asymmetry found (`bash cat <abs>` succeeds but `bash cd <abs> && cat` is blocked) is an
internal inconsistency in bash's own path-use heuristic, not a read_file-vs-bash disagreement, and
is recorded in §2.4's spirit as a minor secondary observation rather than a scenario-scored finding.

---

## 5. Group T policy-enforcement table

**Not in scope for this batch.** Group T (S82–S84) belongs to a later batch's assignment. The one
policy-denial check that IS in this batch's scope, S13 (`bash` explicitly denied for the deny-seeded
tester), is recorded in the ledger above: PASS — `ToolSearch` itself refused to load `bash` with
"denied by this agent's policy," a clean, literal, correctly-classified refusal.

---

## 6. What couldn't be tested and why

- **S17b / S18 — Tier-3 dev-server mode is Linux-only**, confirmed LIVE (not just inferred from
  source): `serve_web` with a `command` argument returns the literal string
  `"Tier 3 dev servers are Linux only"` on this macOS host, and per source read
  (`pkg/tools/web_serve.go`'s `executeDev`) that `runtime.GOOS != "linux"` check is the FIRST
  statement in the function — before the Tier-3 command allow-list check and before the port-range
  validator. This means S18's actual subject (bind-port allow-list refusal) is structurally
  unreachable here; only the outer platform gate was exercised (port=99999 never reached the
  validator it was meant to test). Per the plan's own acknowledged gap: `ci-omnipus` (the Linux CI
  worker) is the sanctioned environment for this leg, not this macOS pass.
- **Windows** was not available at all and is out of scope per the plan.
- No mailbox was configured, but **Group J (S45–S47) is out of scope for this batch anyway**
  (belongs to a later batch's assignment), so this is noted for completeness, not as a batch1 gap.

---

## 7. Stress-group results

**Not in scope for this batch.** Group U (S85–S92) belongs to a later batch's assignment.

---

## 8. Cleanup confirmation

All disposable resources deleted, evidenced:

- **4 disposable agents deleted**, all `204 No Content`:
  `2f46c1e8-e8ba-43a2-a517-e85df7bf58a4` (unused Subagent-type allow tester),
  `e192d426-b0da-485c-9776-265d306370b4` (unused Subagent-type deny tester),
  `7b916e10-137e-4fa4-9bcd-6c5fe42329ef` (Main-type allow tester, the actual driver),
  `0eee748d-a454-465e-b5ec-2920f35f397f` (Main-type deny tester).
  Follow-up `GET /api/v1/agents` confirms only the built-in core roster remains: `mia, jim, ava,
  ray, worker, planner, explorer, researcher, judge, plansupervisor` — none of the four disposables
  present.
- **1 disposable workspace deleted** (`01M1G9VHWNFABNQQQX2PMGDXQ1`), `204 No Content`. Follow-up
  `GET /api/v1/workspaces` shows only the pre-existing default workspace
  (`01M1G9H0RQE7YB90BC0KGH5M6K`).
- **Real fixture directories verified intact before and after the run**:
  - `$UAT_CANARY_ROOT`: final manifest is **byte-identical** to the pre-run manifest
    (`diff` exit 0) — zero writes, zero deletes, confirming the single highest-severity guarantee
    this plan cares about held for the canary tree throughout.
  - `$UAT_MOUNT_ROOT`: only expected additions are present (`fixtures/form.html`,
    `fixtures/delay.html` — logged fixtures I created; `mount-target/marker.txt`,
    `mountgrant/marker.txt`, `mountgrant2/marker.txt` — my own out-of-band setup markers for the
    (failed) mount attempts, also logged). **No file inside any of these real folders was ever
    created, modified, or deleted by a tool call** — consistent with S6's finding that
    `request_mount` never actually succeeds, so no tool-driven write to a real external folder ever
    happened in this batch.
- **Gateway and static server stopped**: both PIDs confirmed no longer running via `ps -p`.
  `lsof -iTCP -sTCP:LISTEN` on ports 19231/19232 after shutdown returns nothing — no orphaned
  listeners.
- **Config changes reverted**: `sandbox.browser_evaluate_enabled` returned to its default-absent
  (denied) state after S22b; `security.ssrf_allow_internal` returned to `[]` after the (ineffective)
  local-fixture unblock attempt — confirmed via a final `GET /security/sandbox-config`.
- **Orphaned tool-approval cleaned up**: an approval left pending by an early, since-abandoned S6
  attempt (`de74faa8-da20-485f-af0b-755a94946cbd`) was resolved via
  `POST /tool-approvals/{id} {"action":"cancel"}` so it would stop appearing in every subsequent
  `session_state` frame.
- `$OMNIPUS_HOME` itself (401 MB, mostly the bundled Chromium download) was left on disk under the
  session's own scratchpad directory as evidence, not deleted — it is fully isolated from
  `~/.omnipus` and contains no real user data.

---

## Appendix — files referenced

All evidence files live under the isolated `$UAT_EVIDENCE` directory
(`uat-batch1-evidence-20260902-122820/`) inside this session's own scratchpad, alongside
`gateway.stdout.log` (the full gateway log for this run) and the two `MANIFEST-*.sha256` file pairs
(before/after) for the canary and mount trees. File names referenced in this report
(`S<n>_result.json`, `S<n>_redo_result.json`, etc.) are the raw WS frame captures for each chat
turn — every one contains the literal tool-call arguments and tool-call results quoted above.
