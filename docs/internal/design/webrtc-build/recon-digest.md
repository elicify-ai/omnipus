# WebRTC build — recon digest (2026-07-18, 4-agent recon of feature/browser-video-2 + archive)

Condensed working reference for the ADR amendment + implementation waves. File:line cites
verified by recon agents on this branch unless marked FETCH_HEAD (= archive
feature/live-browser-video-streaming @ 2e2701b1, fetched).

## Backend (current JPEG stack)

- ONE shared Chrome via `BrowserCoordinator` (`pkg/tools/browser/coordinator.go:73`); launch
  `launchChrome()` :597 → `chromedp.NewExecAllocator` :626-630. Flags in
  `exec_resolver.go:31` `managedExecAllocatorOpts` — **fixed debug port 9223**
  (`exec_resolver.go:46`, `manager.go:40,44`); managers dial `ws://127.0.0.1:9223`
  (`coordinator.go:54`), port preflight :757, pid marker :834. Per-agent browser context
  `WithNewBrowserContext` :207, re-adopt `manager.go:944`.
- Tabs = CDP targets in one window (`manager.go:1003` createTab, no own-window). Agent tab =
  `DefaultSessionID="default"` (`tools.go:48`); WS handlers ignore client session id and always
  target it (`browser_ws.go:544,704,784,954`); SPA picks the AGENT (`browser_attach.agent_id`,
  manager via `agentLoop.BrowserManagerForAgent`, `browser_ws.go:530`).
- Screencast: `live.go:558`/`:821` StartScreencast JPEG Q60 1280×720 every-frame; coalescing
  `runAckWorker` `live.go:998` (ackCh depth-1, `queueAck` :976); frames → `deliver` :1016 →
  `LiveFrame` :44 → per-viewer `FrameSink` (`browser_ws.go:544` marshals
  `BrowserScreencastFrame`, lossy `sendFrameGen` :70, cap 64 :43). `lastFrame` replay :529/:1040.
  Tab switch → `onTabsChanged` :604 → `rebindScreencast` :725/:755; death watch :900.
- Input: `browser_ws.go:654` handleInput → `LiveInput` (`live.go:59`) →
  `Live().Input(DefaultSessionID,…)` → `dispatchInput` :1167 / `buildInputAction` :1368
  (DispatchMouseEvent/KeyEvent/InsertText/Navigate; SSRF gate `ValidateURL` :1211; rate limit
  50/s :30/:1231). **Controller lock in backend**: `takeControl` :1252 first-come,
  `releaseControl` :1271, reject non-controller :1169; agent tools defer while human drives
  (`tools.go:798` → `IsControlled` :372). Gates: `tools.browser.live_view_enabled`
  (`browser_ws.go:246`), `take_control_enabled` :778 (audited).
- WS endpoint `/api/v1/browser/ws` (`gateway.go:2091`): first-frame `{type:auth,token}`
  (`browser_ws.go:283`), origin `wsCheckOrigin` :189, inbound schema validation :450-466.
  Frame catalog: in `browser_attach|input|control|tab_action|detach`; out
  `browser_screencast|status|tabs` (+auth/error).
- **No pion/WebRTC anywhere in the main module** (spikes are separate modules).

## SPA (current)

- Docked panel `BrowserLivePanel.tsx:27-83` (mounted `AppShell.tsx:260`); pop-out route
  `routes/_app/browser-live.tsx`. Render = base64 JPEG `<img>` `BrowserLiveView.tsx:1824-1830`;
  frame state = local useState :331/:675 (no store). **No <video>/<audio>/WebAudio/RTC anywhere
  in src/** — playback surface is new code. Panel opens on explicit click (user gesture for
  audio autoplay): `ChatControls.tsx:69/77`, `BrowserTool.tsx:134-140`.
- Dedicated WS client `browserLiveWs.ts` (auth handshake :126-142; zod parse :63-91; reconnect
  5× backoff :52-54; re-attach on every reconnect :126-142). Input send :206-235
  (`sendInput/sendControl/sendTabAction`); RAF-coalesced mouse_move `flushPendingMove`
  (`BrowserLiveView.tsx:852`), native non-passive wheel :795-827.
- Coords: `browserLiveCoords.ts` `mapClientToDevice` :56-76 (client→device px → ÷page_scale);
  for video: read `video.videoWidth/Height`, keep math.
- Drive UX: `computeDriveMode` :131-144; implicit take `takeWheelIfNeeded` :885-951 (pauses
  agent via `cancelStream`); Esc release :1398-1436/:1386-96 (WCAG 2.1.2); other-driving chip
  :1484; auto-release on agent turn :623-634.
- **Annotate crop reads `<img>` naturalWidth/Height** (`cropFrameToFile` :998-1041) — video path
  needs canvas.drawImage(video) equivalent or keep img alongside.
- Wire payloads for input/control/tab-action can stay IDENTICAL over a data channel — only the
  carrier changes (generated types `asyncapi-types.ts:383-450`).

## Contracts + sandbox + provisioning

- asyncapi 3.0 `browser` channel :131-162, ops :498-560. Per-frame schema files with
  discriminator `const` + `additionalProperties:false` (e.g. `BrowserAttachFrame.yaml:17-19`);
  browser frames NOT in `WsFrameType.yaml`. New frames: schema file + message stub + channel
  wiring + ops → `make gen-contracts` (also syncs `pkg/gateway/inboundschemas/`).
  **ADR-034**: never model as oneOf union with external file refs.
- Sandbox: Landlock+seccomp applied to the GATEWAY ITSELF at boot (`sandbox_apply.go:242,440,451`);
  children inherit unchanged (`hardened_exec_linux.go:4-5`). **Landlock net = TCP-only**
  (`sandbox_linux.go:69-70`) → Pion UDP binds + outbound STUN are unfiltered today (fragile
  invariant — document). TURN-over-TCP on non-{53,80,443} would be blocked by connect allow-list
  (`sandbox.go:171`). Enforcement gated on ABI v4 / kernel 6.7+ (`sandbox.go:44-45`).
  Bind allow-list: DevServerPortRange 18000-18999 + 9223 (`sandbox_apply.go:374-388`; 9223
  entries removed once pipe lands).
- Extension shipping precedent: `pkg/skills/embed.go` `//go:embed all:embedded` + `SeedDefaults`
  :61 (staged tmpdir + atomic rename, idempotent) into `OmnipusHomeDir()` (`pkg/config/home.go:34`).
- Config flag pattern: `BrowserToolConfig` (`config.go:2984`), defaults `defaults.go:437-448`,
  post-auth gate precedent `browser_ws.go:246`. Settings UI: `*Section.tsx` + Switch + mutation.
- Lite-build pattern: real+stub file pair w/ build tags + blank-import init registration
  (whatsapp_native, `gateway.go:55`).

## Archive (FETCH_HEAD) portable assets

1. **`pkg/tools/browser/cdppipe/`** — pure-Go CDP-over-pipe chromedp transport (allocator.go 424,
   pipeconn.go 127, dialer.go 92, frame.go 58 + 625 lines tests). Zero omnipus-internal imports
   (chromedp+cdproto+gobwas/ws). **CLEAN CHERRY-PICK.** Integration seams on FETCH_HEAD:
   `launchManagedPipe` (exec_resolver.go L187), coordinator `pipeLauncher` seam.
2. **Coordinator rework** — `coordinator_lock_unix.go` (51, flock) + `coordinator_lock_other.go`
   (40) clean; `coordinator.go` (1060) replaces port/preflight/RemoteAllocator with
   lockfile + `takeLaunchLock` + child contexts of the pipe rootCtx. PORT-WITH-EDITS.
3. **Installer dual-download + capability** — `installer.go` (604: `fullChromeBuild()`,
   `EnsureChromiumBuild`, `selectDownloadBuild`, per-build subdirs, GoogHash verify) +
   `capability.go` (~135: `ClassifyVideoCapability`) + tests (591+). NEAR CHERRY-PICK; reword
   A2-framed semantics.
4. Do NOT port: stream_relay.go/GOP, encoder_launch.go, encoderpage/, gateway
   browser_ingest/browser_ws_video/browser_stream/metrics, BrowserChunkEnvelope-family contracts,
   SPA VideoDecoder path. Xvfb/PulseAudio already removed on archive itself (`effb14d3`).
5. No extension-loading code exists on archive; stealth (deHeadlessUA/webdriver override) already
   on HEAD (`manager.go:1743-1797`).
6. FETCH_HEAD flag builder: `chromeHardeningBaseFlags()` + new-signature
   `managedExecAllocatorOpts` (no debug port; adds --disable-gpu --enable-unsafe-swiftshader
   --mute-audio etc.). Note for WebRTC: re-evaluate `--mute-audio` (spike Q2 proved capture works
   WITH it, T1) and GPU flags.

## Spike-proven recipes (wv1-spike-results.md has full detail)

- Capture: tabCapture MV3 ext, `Extensions.loadUnpacked` over PIPE only (`--load-extension` dead
  since Chrome 137), `--allowlisted-extension-id` (no gesture), self-consume in extension page,
  survives navigation, ~30fps + real audio, no sidecars/flags. Ext ID is path-deterministic —
  pin via manifest `key` for a stable ID instead of the two-phase learn dance.
- Pion: RegisterDefaultCodecs+Interceptors for media; shared TrackLocalStaticRTP per kind;
  PLI → ingest PC w/ remote MediaSSRC on viewer join; drain RTP/RTCP on receiver+sender;
  dc text framing: browser `send(string)` ⇒ reply `SendText`.
- Input over DC: p50 10.9ms p95 21ms under 60mv/s stress, media unaffected; CDP keyDown needs
  `text`/`unmodifiedText` for printables (NOTE: current `buildInputAction` live.go:1368 already
  handles text via InsertText — reuse as-is).
- Q1 traversal: pod outbound UDP+STUN works (srflx v4+v6); EXTERNAL hole-punch still pending
  operator test; fallback posture = JPEG screencast remains (this branch's live path).
