# Live-Browser Video Streaming — Implementation Plan (wave fan-out)

Source of truth: `live-browser-video-streaming-spec.md` (R7) + `ADR-044` (§6.0/§6.0.1/§6.0.2/§6.0.3 authoritative). Branch: `feature/live-browser-video-streaming`.

This plan decomposes the epic into **waves of parallel, file-scoped agents** (no two agents in a wave share a file). Each component codes to the **interfaces** below so parallel work integrates. Dev tags: `goolm,stdjson`. CI is the test authority (never run the full Go suite in the pod).

## Component interfaces (agreed contracts between parallel units)

- **CDP-over-pipe transport** (`pkg/tools/browser/cdppipe`): exposes a chromedp-compatible allocator
  `NewPipeAllocator(parent context.Context, execPath string, opts PipeOptions) (context.Context, context.CancelFunc, error)` that launches Chrome with `--remote-debugging-pipe`, wires fd 3 (write)/fd 4 (read), speaks CDP as NUL-delimited JSON over the pipe (reuse `github.com/chromedp/cdproto`), and drives chromedp's higher layers with **no TCP port**. Also `PipeConn` implementing the minimal read/write CDP message loop. Coordinator consumes this instead of `chromedp.NewExecAllocator`.
- **Stream relay + GOP cache** (`pkg/tools/browser/live.go`): `type StreamRelay` with `Ingest(streamID, chunk EncodedChunk)`, `Attach(streamID, viewer) (replayGOP []EncodedChunk)`, `Detach`, `Evict()` (idle-first, never a viewed/attaching stream). `EncodedChunk{Seq uint32; TS uint64; Key bool; Codec string; Kind video|audio; Payload []byte}`.
- **Ingest endpoint** (`pkg/gateway/browser_ingest.go`): `/api/v1/browser/capture-ingest` loopback-only WS. Mints per-stream token (`MintIngestToken(streamID) string`), single-connection lifecycle, `browser_ingest_init` + binary `browser_ingest_chunk` upstream; downstream `browser_frame_feed` (JPEG) to the encoder page. Calls `StreamRelay.Ingest`. Audit on start/stop + every reject.
- **Encoder page** (`pkg/tools/browser/encoderpage`, `go:embed`): static HTML/JS. Receives `browser_frame_feed` (JPEG) → `createImageBitmap`→`VideoFrame`→`VideoEncoder` (h264-main/vp8) → `browser_ingest_chunk`. Audio: `getUserMedia` on the sink monitor → `AudioEncoder` (Opus). Token injected via CDP `addScriptToEvaluateOnNewDocument`.
- **WS write pump** (`pkg/gateway/browser_ws.go`): `sendCh` becomes opcode-tagged (`type wsSendItem{ Binary bool; Data []byte }`), preserving the nil-ping sentinel; Binary→WS binary, Text→WS text.
- **SPA decode** (`src/lib/browserLiveWs.ts`, `BrowserLiveView.tsx`): `binaryType='arraybuffer'`; branch chunk vs JSON; `VideoDecoder`→`<canvas>`, `AudioDecoder`→audio; advertise `video_caps`/`audio_caps` in `browser_attach`; unavailable state.

## Waves

### W0 — Contracts (1 agent; BLOCKS all typed code)
`contracts/asyncapi.yaml` + `contracts/components/schemas/`: ADD `browser_stream_init`, `browser_video_chunk` (binary, `{seq:u32, ts:u64, key:u8, len:u32, payload}`), `browser_audio_chunk`, `browser_ingest_init`, binary `browser_ingest_chunk`, `browser_frame_feed`; amend `browser_attach` with `video_caps`/`audio_caps`. **KEEP `browser_screencast`** (removed in W3 after cutover — FR-010/F-02 sequencing). Regen via `scripts/gen-contracts.sh`; commit generated `pkg/api/generated/` + `src/lib/api/generated/`; `make verify-contracts` clean. FR-002/005/010/012/013/017.

### W1 — Core building blocks (8 parallel, file-disjoint; after W0)
| # | Agent | Files (own) | FRs | Lead |
|---|-------|-------------|-----|------|
| A | CDP-over-pipe transport | `pkg/tools/browser/cdppipe/*` (new) | FR-013, EC-3, CRIT-001 | security |
| B | Xvfb sidecar | `pkg/tools/browser/display_linux.go`, `display_other.go` (new) | FR-021 | backend |
| C | PulseAudio sidecar + PA native client | `pkg/tools/browser/audiosink_{linux,other}.go`, `pkg/audio/pulse/*` (new) | FR-022, FR-023 | backend |
| D | Stream relay + GOP cache | `pkg/tools/browser/live.go` (extend) | FR-003, FR-004, MAJ-005 | backend |
| E | Ingest endpoint + token lifecycle + audit | `pkg/gateway/browser_ingest.go` (new) | FR-012/013/014/024, CRIT-002 | security |
| F | WS binary framing | `pkg/gateway/browser_ws.go` (write pump only) | FR-017 | backend |
| G | Encoder page | `pkg/tools/browser/encoderpage/*` + embed (new) | FR-001/011/016/023 | frontend |
| H | SPA decode + unavailable state | `src/lib/browserLiveWs.ts`, `BrowserLiveView.tsx` | FR-005/007/017 | frontend |

### W2 — Coordinator/launch/installer (5 parallel, after W1; consumes A/B/C)
| # | Agent | Files | FRs |
|---|-------|-------|-----|
| I | ADR-043 coordinator rework (lockfile single-launch, in-process child contexts, remove port) + sandbox 9223 removal | `coordinator.go`, `sandbox_apply.go`, `manager.go` | CRIT-001, FR-013 |
| J | `managedExecAllocatorOpts` headful + dbus + DISPLAY + pipe | `exec_resolver.go` | FR-001, MAJ-001 |
| K | Installer dual-download + platform classification | `installer.go` + capability classifier | FR-009, F-08 |
| L | Screencast capture driver (drive `Page.startScreencast` → feed encoder page) | new `pkg/tools/browser/capture.go` | FR-001, FR-016 |
| M | Stream orchestration (mint token, launch encoder page, re-mint+relaunch on drop, kill-switch, timeouts, observability, config) | new `pkg/tools/browser/stream.go` + config | FR-018/019/020/024 |

### W3 — Cutover + degradation (2 parallel, after W2 integration green on video-capable path)
| # | Agent | Files | FRs |
|---|-------|-------|-----|
| N | Remove `browser_screencast` (contract + Go emit + SPA) once video path reachable | `asyncapi.yaml`, `browser_ws.go`, `browserLiveWs.ts` | FR-010, M-10 |
| O | Degradation wiring + video-capable classification end-to-end + observability finalize | classifier, `BrowserLiveView.tsx`, metrics | FR-007/018/019 |

### W-tests — comprehensive coverage (parallel, after impl compiles)
- Go units (Tests 1–19, 27–32): 3–4 agents by area (ingest/relay/security; sidecars/launch/installer; contract/framing; observability/audit).
- SPA units (Tests 20–22): 1 agent.
- **E2E (Tests 23–26) incl. LLM-driven** (Playwright MCP + an LLM-agent driver hitting the running binary): 1–2 agents.

## Review + UAT (per operator goal)
1. After integration: **8 reviewers** (7 pr-review-toolkit + grill-code) over the **entire diff**; fix **ALL** findings (not only critical).
2. **8 reviewers again** over the original complete diff; fix all findings again.
3. Everything green (gofmt, golangci-lint, go test tags, vitest, tsc, verify-contracts, govulncheck) via CI.
4. Build a **UAT test matrix**; execute with subagents impersonating human UAT testers; fix all issues.

## Sequencing gates (carried, do not lose)
- Pre-build: EC-3 (build+prove pipe transport, Test 30) + ADR-043 coordinator rework (W1-A, W2-I).
- Pre-`/taskify`-equivalent measurement: min-spec EC-1 re-run, SC-001a/SC-002 (measured on CI during build).
- Before CDP-transport swap ships: MAJ-001 browsing-equivalence (Test 16).
- Ship gate: EC-4 iPad H.264-main decode (operator device — deferred).
