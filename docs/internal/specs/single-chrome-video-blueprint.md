# Single full-Chrome, encoder-as-tab — implementation blueprint

**Status:** Ready for dev wave. **Grounds:** ADR-044 Amendment (2026-07-17). **Scope:** collapse the current two-Chrome "Option A" into ONE full-Chrome-headless process that hosts both agent tabs and the WebCodecs encoder tab. **No wire-contract change, no frontend change, no CGo, single binary.**

Every claim below is cited to the code as it stands on `bugfixes2`. `[FACT-n]` refers to the operator probes recorded in the ADR amendment.

---

## 0. The one design decision, stated concretely

Today the encoder page is launched into a **separate, orchestrator-owned Chrome process** (`encoderBrowser`, `pkg/gateway/browser_stream.go:378-656`) resolved from the full-Chrome build (`EnsureChromiumFullBuild`), while agent tabs run in the coordinator's `chrome-headless-shell` (`pkg/tools/browser/coordinator.go:675-786`, binary from `selectDownloadBuild()` → `headlessShellBuild()`, `installer.go:260-262`).

**Change:** the agent Chrome becomes **full Chrome** (one binary), and the encoder page is launched as a **tab in that same coordinator Chrome's default browser context** — reusing the coordinator's existing shared root context (`BrowserCoordinator.RootContext()`, `coordinator.go:495-502`).

The relay, GOP cache, ingest endpoint + token, `Page.startScreencast` capture, codec negotiation, liveness/step-down/kill-switch, and every WS frame **stay exactly as they are**. The ONLY structural change is the *source of the encoder tab's root context*: from `o.encoder.ensureRoot(ctx)` (a dedicated process) to `coordinator.RootContext()` (the shared process). `LaunchEncoderPage` itself is unchanged — it already creates the encoder target with a raw `target.CreateTarget("about:blank").Do(cdp.WithExecutor(rootCtx, c.Browser))` in the default context and adopts it via `chromedp.NewContext(rootCtx, chromedp.WithTargetID(tid))` (`encoder_launch.go:216-238`). That is precisely the mechanic we want; we simply feed it the coordinator's `rootCtx`.

### Why `RootContext()` (a `context.Context`) is sufficient

`LaunchEncoderPage(rootCtx, cfg)` extracts the shared browser from the context itself: `c := chromedp.FromContext(rootCtx); … c.Browser` (`encoder_launch.go:216-220`). So the orchestrator needs only the coordinator's `rootCtx` — not a separate `*chromedp.Browser` handle. `RootContext()` already returns `(context.Context, bool)`; the `bool` is the liveness signal we map to "fail the stream closed" when Chrome is mid-relaunch/down.

### Isolation is preserved (ADR-043)

Agent tabs keep running in **per-agent browser contexts** created by `coordinator.Register` via `chromedp.NewContext(rootCtx, chromedp.WithNewBrowserContext())` (`coordinator.go:241-257`). The encoder tab runs in the **default** context (the window Chrome opens at launch), which no agent uses. `[FACT-1]` confirms both `createBrowserContext{…}+createTarget{browserContextId}` (agent contexts) AND default-context `createTarget` (encoder) succeed together on plain `--headless` full Chrome. The FR-016 "agent navigates to the encoder origin" threat is re-secured by the unguessable loopback origin + secret path (unchanged) and, phase-1, by there being no audio grant at all (`HasAudio` always false).

---

## 1. Exact file/function changes

### 1.1 `pkg/tools/browser/installer.go` — flip the agent's default build (the load-bearing one-liner)

- **`selectDownloadBuild()` (`installer.go:260-262`)** currently `return headlessShellBuild()`. **Change to `return fullChromeBuild()`.** This is what makes the agent's `EnsureChromium` (`installer.go:228-230` → `EnsureChromiumBuild(ctx, installRoot, selectDownloadBuild())`) resolve/download the full `chrome` build. Update the doc comment (it currently states "the agent's own browser is always chrome-headless-shell").
- **Keep** the whole dual-build machinery: `EnsureChromiumBuild` (`:114`), `fullChromeBuild()`/`headlessShellBuild()` (`:70-78`), `findInstalledBinary` detect-either (`:271-276`), and especially the **F-08 fallback** (`:153-182`) that drops to `headlessShellBuild()` when the full build is missing from the manifest or has no build for the platform. That fallback is now the graceful-degradation path (#4): a host that can only get headless-shell still browses; `ClassifyVideoCapability` will report not-capable and the panel shows the unavailable state.
- **Keep `EnsureChromiumFullBuild` (`:238-240`)** — still the explicit "encoder needs full Chrome" entry point used by the classifier's semantics and the boot prefetch (§1.6). It now resolves the same binary the agent uses, which is correct.
- **DoD:** fresh install downloads full `chrome` as the agent binary; headless-shell fallback intact; `installer_test.go` updated for the new `selectDownloadBuild()` return (it asserts the returned build).

### 1.2 `pkg/tools/browser/exec_resolver.go` — one flag-set, delete the encoder cmdline

- **`managedExecAllocatorOpts` (`:128-145`)** already appends `--headless` (plain, old mode — correct per `[FACT-1]`), `--hide-scrollbars`, `--mute-audio`, and inherits `chromeHardeningBaseFlags()`. `chromeHardeningBaseFlags` already includes `--disable-gpu --enable-unsafe-swiftshader` (`:97-98`) — required for the WebCodecs software encode path per `[FACT-2]`. **So the agent launch flags need NO functional change** — only doc updates (the comment says "unconditionally chrome-headless-shell now… no video-capable branch"; the binary is now full Chrome, the flags are unchanged, and this process now *also hosts the encoder tab*).
- **DELETE `EncoderChromeCmdline` (`:147-185`)** — there is no separate encoder process anymore. Its flags were `chromeHardeningBaseFlags() + --headless`, i.e. the agent's flags minus `--hide-scrollbars/--mute-audio`; the encoder tab now simply runs inside the agent process.
- **`chromeHardeningBaseFlags` doc (`:50-66`)** references "the dedicated encoder browser (`EncoderChromeCmdline`)" — update to note the base is shared by the single agent+encoder Chrome.
- **`managedLaunchParams` (`:35`)** stays an empty struct (signature stability for `coordinator.go`/`manager.go` callers).
- **DoD:** package compiles with no `EncoderChromeCmdline` symbol; `security_launch_test.go` (asserts hardening flags on the launch cmdline) updated to assert against `managedExecAllocatorOpts` only.

### 1.3 `pkg/gateway/browser_stream.go` — collapse `encoderBrowser` into a coordinator-root seam (the big one)

**Delete** the entire "dedicated encoder browser" section and its plumbing:
- `encoderBrowser` type + all methods: `newEncoderBrowser` (`:421`), `encoderProfileDir` (`:439`), `encoderPipeLaunch` (`:449`), `defaultEncoderExecPath` (`:459`), `defaultEncoderPipeLaunch` (`:472`), `ensureRoot` (`:517`), `watchForCrash` (`:574`), `clearLocked` (`:588`), `tabOpened` (`:603`), `tabClosed` (`:619`), `close` (`:648`), and the `defaultEncoderMaxRelaunches`/`defaultEncoderIdleTeardown` consts (`:352-355`).
- The `encoder *encoderBrowser` field on `BrowserVideoOrchestrator` (`:675`) and its construction in `newOrchestrator` (`:796`).
- The **background full-Chrome prefetch goroutine + `encoderPrefetchTimeout`** in `RegisterBrowserVideo` (`:762-781`) — relocated/repurposed per §1.6.

**Add** a lazy root-context seam (the coordinator is created lazily and may be nil at boot — `AgentLoop.BrowserCoordinator()` doc, `loop.go:3623-3639`):

```go
// coordinatorRoot returns the shared coordinator Chrome's live root context, or
// ok=false when no Chrome is currently live (never launched, mid-relaunch, or
// shut down). Resolved fresh on every encoder-tab launch — never cached — so a
// coordinator crash/relaunch is observed correctly. *browser.BrowserCoordinator
// satisfies the RootContext() shape; a nil source (hermetic tests) yields ok=false.
type coordinatorRoot func() (context.Context, bool)
```

- Add `Coordinator coordinatorRoot` to `BrowserVideoDeps` (`:259-274`); default it in `newOrchestrator` to a `func() (context.Context, bool){ return nil, false }` when nil, exactly as the other seams default to real impls. Store it as `o.rootSource`.
- **Rewrite `launchEncoderTab` (`:1116-1130`)**: replace `root, _, err := o.encoder.ensureRoot(ctx)` with `root, ok := o.rootSource()`; if `!ok`, return an error (`"coordinator chrome unavailable"`) — the caller already maps a launch error to the FR-018 unavailable state (`startStreamLocked:1021-1026`, `handleEncoderDrop:1263-1268`). Then `o.launchEncoder(root, cfg)` **unchanged**. Drop `o.encoder.tabOpened()`. The `ctx` parameter becomes unused → drop it (both call sites pass `context.Background()`: `startStreamLocked:1018`, `handleEncoderDrop:1253`).
- **Simplify `closeEncoderTab` (`:1138-1144`)**: drop `o.encoder.tabClosed()`; just `tab.Close()`.
- **`Shutdown` (`:1429-1445`)**: drop `o.encoder.close()` — the orchestrator no longer owns a process. The coordinator's own `Shutdown` (`coordinator.go:526`, called from `loop.go:2923-2925` / `shutdown.go`) kills the single Chrome. Per-stream teardown loop stays.
- **Doc updates (no logic):** `videoStream.agentCtx` comment (`:287-293`) and `AttachParams` "PINNED CONTRACT (i)" comment (`:829-850`) both explain the two-Chrome rationale — rewrite to "the encoder tab is a default-context tab of the coordinator's shared Chrome; its root comes from `o.rootSource()`."
- **Lifetime note (call out in the PR):** the encoder tab is now a child of the coordinator's `rootCtx`. When the coordinator Chrome crashes (`coordinator.go:802-864` `watchForCrash` nils `rootCtx`, clears agent contexts, relaunches), the encoder tab's `Done()` fires → existing CRIT-002 recovery (`handleEncoderDrop`) runs → `launchEncoderTab` re-reads `o.rootSource()`; if the coordinator is mid-relaunch it returns `ok=false` → stream fails closed to unavailable (correct; the agent capture tab died in the same crash anyway). No new lifecycle code needed — the seam's `ok=false` path IS the recovery contract.
- **DoD:** no `encoderBrowser`/prefetch symbols remain; hermetic orchestrator tests still pass with a nil `Coordinator` seam (they never bring up a real encoder); a new unit test wires a fake `coordinatorRoot` returning a live-ish ctx and asserts `launchEncoderTab` calls `o.launchEncoder` with it.

### 1.4 `pkg/gateway/gateway.go` — wire the coordinator seam (depends on §1.3's new field)

- At the `RegisterBrowserVideo` call (`gateway.go:2167-2174`), add to `BrowserVideoDeps`:
  ```go
  Coordinator: func() (context.Context, bool) {
      c := agentLoop.BrowserCoordinator() // may be nil pre-first-browse
      if c == nil { return nil, false }
      return c.RootContext()
  },
  ```
  `agentLoop` is already in scope here (`:2168` uses `agentLoop.AuditLogger()`). The closure re-resolves the (lazily-constructed) coordinator on every launch — never captures a nil.
- **DoD:** gateway boots; first agent browse launches full Chrome via the coordinator; first live-view attach creates the encoder tab in that same Chrome. **Ordering: land §1.3 before §1.4** (the `Coordinator` field must exist). §1.3 is independently landable because the seam defaults to `ok=false`.

### 1.5 `pkg/tools/browser/coordinator.go` + `capability.go` — doc truth-up, logic mostly unchanged

- **`coordinator.go` `launchChrome` (`:706-711`)**: the comment "the agent's shared Chrome is unconditionally headless-shell now… video capture lives on a separate encoder browser" is now false. Correct it to "full Chrome; hosts the WebCodecs encoder tab in its default context (ADR-044 amendment)." **No code change** — `managedExecAllocatorOpts(c.cfg, …)` + `execPath.resolve` now yield full Chrome + plain `--headless` automatically.
- **`coordinator.go` `RootContext` doc (`:478-494`)**: currently "the dedicated encoder browser is NOT a sibling context of this rootCtx — it is a wholly separate Chrome process." Rewrite: "the encoder tab IS a default-context tab of this rootCtx; this method is the single source of the encoder's root (consumed by the video orchestrator's `coordinatorRoot` seam)." **No code change** — the method already returns `(ctx, ok)` with the exact liveness semantics the seam needs.
- **`capability.go` `ClassifyVideoCapability` (`:102-117`)**: logic **unchanged** — it already gates video-capable on `linux + findInstalledBuild(installRoot, platform, fullChromeBuild()) != ""` (`:113`). Since the agent binary is now the full build, "capable" naturally becomes true once the agent binary is present. Only the doc comments (`:60-101`) that say "the agent's own browser is always chrome-headless-shell; only the live-view video path (a SEPARATE, dedicated encoder browser process) is gated on this" need truth-up to "the single agent+encoder Chrome is full Chrome; this gates whether that build is installed."
- **DoD:** docs match reality; `coordinator_test.go`/`capability`-facing tests that reference `EncoderChromeCmdline` or assume headless-shell-as-agent updated.

### 1.6 Installer prefetch & the ~120 MB first-boot window

- Under two-Chrome the orchestrator ran a background prefetch of the **encoder** binary (`browser_stream.go:762-773`). Under single-Chrome the coordinator downloads full Chrome **lazily on first browser-tool use** (`coordinator.launchChrome` → `execPath.resolve` → `EnsureChromium` → full build). **Net download is LESS than Option A** (Option A fetched BOTH headless-shell for the agent AND full Chrome for the encoder; single-Chrome fetches ONLY full Chrome).
- **HTTP/WS is already non-blocking at boot:** the gateway's listeners come up immediately; the ~120 MB full-Chrome download blocks only the FIRST browser tool call, never gateway boot and never a WS attach. Clients connecting during first-browse provisioning get the normal "no stream yet / unavailable" states, not a hang. **Recommendation: KEEP a boot-time background prefetch**, but point it at `browser.EnsureChromiumFullBuild(ctx, installRoot)` (now the agent binary) so first-browse latency is hidden. Simplest placement: a small `go func(){ _ = browser.EnsureChromiumFullBuild(ctx, installRoot) }()` guarded by `runtime.GOOS == "linux"` at gateway boot (or retain the orchestrator's prefetch goroutine but call `EnsureChromiumFullBuild` directly instead of via the deleted `encoder.resolveExecPath` seam). This is a judgment call flagged for the operator; it is an optimization, not correctness.
- **DoD:** no functional dependency on the prefetch (lazy download still works if it's skipped); if kept, it warms the same binary the coordinator launches.

### 1.7 `pkg/tools/browser/manager.go` — stealth doc truth-up (agent is now full Chrome)

- `applyStealth` (`:1795`) is already invoked on the agent tab-bind path (`manager.go:1023`), applying `deHeadlessUA` + `stealthInitScript` via `emulation.SetUserAgentOverride` (unchanged, correct).
- The **"Effectiveness caveat"** on `stealthInitScript` (`:1763-1770`) says the `navigator.webdriver` override "lands on full-Chrome `--headless=new`, but NOT on the bundled chrome-headless-shell." With the agent now on full Chrome, the override **does** land (`[FACT-3]`) — update the caveat to reflect the strictly-better stealth posture on both the UA and `webdriver` axes. **No code change** — this is a comment correction only.
- **DoD:** comment accurate; no behavioral change.

### 1.8 `encoder_launch.go` — security-doc re-scope, phase-1 code unchanged

- **`LaunchEncoderPage` (`:197-299`) code is UNCHANGED.** The default-context `target.CreateTarget` + `NewContext(WithTargetID)` mechanic (`:220-224`) is exactly what we want against the coordinator root.
- **SECURITY POSTURE doc (`:17-50`)**: the "structurally impossible — rootCtx is a browser process dedicated solely to encoder pages, no agent tab exists" argument is now false. Rewrite to the ADR amendment's FR-016 re-scoping: unguessable origin + secret path is the primary defense; phase-1 is video-only so no grant exists to inherit; the encoder tab shares the process with per-agent contexts but lands in the default context.
- **Phase-2 `HasAudio` note (`:255-275`)**: currently instructs a *browser-level* origin grant WITHOUT `WithBrowserContextID` ("because agent-inheritance is structurally impossible"). **Invert it:** phase-2 audio MUST scope the grant to the encoder tab's **default** browser context via `WithBrowserContextID`, because an agent tab (in a different context) CAN now navigate to the origin if it ever learns it. Phase-1 `HasAudio` is always false, so this is a design-note change, not phase-1 code.
- **DoD:** docs match single-Chrome reality; phase-1 launch behavior identical; `encoder_launch` tests unaffected (they exercise the unchanged mechanic).

### 1.9 What does NOT change (guard against scope creep)

- **No `contracts/*.yaml` change, no `pkg/api/generated` / `src/lib/api/generated` regen** — every wire frame (`browser_stream_init`, `browser_video_chunk`, ingest) is byte-identical. Constraint #8 untouched.
- **No frontend change** — the SPA `VideoDecoder` path (`src/lib/browserLiveWs.ts`) still receives `avc1.4D4028` H.264 (`[FACT-2]` confirms full Chrome encodes it). Verify-only.
- **No policy/sandbox change** — no new tool, no tool-policy entry (Constraint #6 untouched). The single Chrome is still launched with `--no-sandbox` under the gateway's outer Landlock/seccomp (`exec_resolver.go:106-113`, unchanged).
- Relay, GOP cache, ingest token, `Page.startScreencast` capture (component L), codec negotiation, liveness/step-down/kill-switch — all unchanged.

---

## 2. Constraint & ADR compliance checklist (verify before the `→ main` PR)

| Guard | How this blueprint honors it |
|---|---|
| #1 Single Go binary, SPA `go:embed` | One fewer Chrome process; no new binary; encoder page still `encoderpage.Handler()` embedded. |
| #2 Pure Go, no CGo, no shelling for media | Encoding stays in-browser (WebCodecs); no ffmpeg/x264; deleted a process, added none. |
| #3 Footprint < 10 MB overhead | Net **less** RAM/disk than Option A (one full Chrome vs headless-shell + a second full Chrome). |
| #4 Graceful degradation | Non-linux / no-full-build → `ClassifyVideoCapability` not-capable → unavailable state; installer headless-shell fallback retained. |
| #6 No default tool-policy fallback | Untouched — no tool added, no policy branch. |
| #8 Contract-first wire formats | No wire change at all; no codegen. |
| ADR-043 per-agent isolation | Preserved — per-agent browser contexts on plain `--headless` full Chrome (`[FACT-1]`). |
| ADR-044 §6.0–6.4 relay decision | Preserved verbatim; only encoder process topology changes. |

---

## 3. Parallelized wave task breakdown

Eight disjoint dev units (each owns non-overlapping files), then review → fix → UAT. **Hard ordering: only C-before-D** (D consumes the `BrowserVideoDeps.Coordinator` field C defines; C is independently landable because the seam defaults to `ok=false`). Everything else is fully parallel — no two units share a file.

| Unit | Agent | Files (exclusive) | Work | Definition of done |
|---|---|---|---|---|
| **A** | backend-lead | `pkg/tools/browser/installer.go` (+ `installer_test.go`) | `selectDownloadBuild()` → `fullChromeBuild()`; doc truth-up; **keep** F-08 fallback + `EnsureChromiumFullBuild`. | Agent default download = full `chrome`; fallback intact; test asserts new build; `go build -tags goolm,stdjson ./pkg/tools/browser/` green. |
| **B** | backend-lead | `pkg/tools/browser/exec_resolver.go` (+ `security_launch_test.go`) | Delete `EncoderChromeCmdline`; doc-fix `managedExecAllocatorOpts` + `chromeHardeningBaseFlags`; confirm plain `--headless` + swiftshader retained. | No `EncoderChromeCmdline` symbol; agent cmdline renders full-Chrome plain `--headless`; hardening test green. |
| **C** | backend-lead | `pkg/gateway/browser_stream.go` | Remove `encoderBrowser` + all methods + prefetch; add `coordinatorRoot` seam + `BrowserVideoDeps.Coordinator`; rewrite `launchEncoderTab`/`closeEncoderTab`/`Shutdown`; doc truth-up. | No `encoderBrowser`/`encoderPrefetchTimeout` symbols; hermetic tests pass with nil seam; new unit test drives a fake `coordinatorRoot`. **Lands before D.** |
| **D** | backend-lead | `pkg/gateway/gateway.go` (+ boot prefetch per §1.6) | Wire `Coordinator:` closure over `agentLoop.BrowserCoordinator().RootContext()`; optional boot prefetch of `EnsureChromiumFullBuild`. | Gateway boots; encoder tab created in coordinator Chrome on first attach. **After C.** |
| **E** | backend-lead | `pkg/tools/browser/coordinator.go` + `capability.go` (+ `coordinator_test.go`) | Doc truth-up in `launchChrome`/`RootContext`; `ClassifyVideoCapability` doc-fix (logic unchanged). | Docs accurate; capability logic unchanged; tests referencing removed symbols updated. |
| **F** | security-lead | `pkg/tools/browser/encoder_launch.go` | Rewrite SECURITY POSTURE doc for single-Chrome FR-016; **invert** the phase-2 audio-grant note to default-context `WithBrowserContextID`. Phase-1 code unchanged. | Docs match single-Chrome threat model; phase-1 launch byte-identical; security_launch/encoder tests green. |
| **G** | backend-lead | `pkg/tools/browser/manager.go` | Truth-up the `stealthInitScript` "Effectiveness caveat" (webdriver override now lands on the full-Chrome agent). Comment-only. | Comment accurate; zero behavioral change; package compiles. |
| **H** | qa-lead | `pkg/gateway/live_video_pipeline_e2e_test.go` + any residual `*_test.go` referencing removed symbols | Update tests that assert on `encoderBrowser`/`EncoderChromeCmdline`/two-Chrome; add the integration assertion that ONE coordinator Chrome hosts N per-agent contexts + a default-context encoder tab. | Full `pkg/gateway` + `pkg/tools/browser` test packages compile; the single-Chrome integration test is the wave's behavioral gate. **After A–G.** |

**Shared-type note:** the only cross-unit type is `BrowserVideoDeps.Coordinator` (owned by C). No other unit touches a file another owns. Units A, B, E, F, G are fully independent and can run in one fan-out with C; D and H gate on their predecessors.

### Review wave (7-reviewer quality gate, MANDATORY per CLAUDE.md)
Run the pr-review-toolkit set (code-reviewer, silent-failure-hunter, type-design-analyzer, comment-analyzer, pr-test-analyzer, code-simplifier) + `architect` on the epic diff **before** the `→ main` PR. Focus items: (a) the `coordinatorRoot` `ok=false` path genuinely fails streams closed (no nil-ctx deref); (b) no lingering reference to a "second Chrome"; (c) FR-016 doc/phase-2 note correctness (security-lead sign-off); (d) no accidental `contracts/*` or generated drift (`make verify-contracts`).

### Fix wave
Fan out parallel fix agents against reviewer findings, disjoint by file (same ownership map).

### UAT / build gates (CI is authority — do NOT run the full Go suite locally; OOM risk)
Push and run on `ci-omnipus`: `go-build`, `go-vet`, `go-test`, `contracts`, `spa`, `gofmt`, then `e2e`. Manual UAT on the pod (`$DEVPOD_PREVIEW_URL`): fresh `OMNIPUS_HOME`, trigger an agent browse (confirms full-Chrome download + per-agent context), open the live-view panel (confirms encoder tab in the same Chrome, H.264 decode in the SPA), open a SECOND agent's browse concurrently (confirms per-agent isolation coexists with the default-context encoder tab — the `[FACT-1]` combination on the coordinator's real launch), then flip the FR-020 kill-switch (confirms teardown). Zero console errors (WS reconnect warnings OK).

---

## 4. Risk register (carry into the PR body)

- **R1 — coordinator launch flags vs WebCodecs:** `[FACT-2]` used a bespoke Chrome; the coordinator adds more hardening flags (`chromeHardeningBaseFlags`). Mitigation: the swiftshader/GPU flags the encoder needs are already in the base set (`exec_resolver.go:97-98`); UAT H is the confirmation on the real coordinator launch.
- **R2 — encoder tab in default context while agents use named contexts:** validated structurally by `[FACT-1]`; UAT H exercises N-contexts + encoder concurrently.
- **R3 — full-Chrome-as-agent footprint (#3):** measured for the encoder under Option A, not for the agent role. Non-blocking (net is still less than two binaries); one RSS/disk read on the pod closes it.
- **R4 — phase-2 audio FR-016:** design-note only today; must be honored (default-context-scoped grant) when audio ships — tracked in Unit F's doc change.
