# Implementation plan — early user test first

Status (2026-09-13): the parallel implementation and early handoff are delivered; source e15b9caef is deployed and verified in Amsterdam. The independent review's startup/control race is corrected and both deliberate startup interleavings passed live. Expanded live testing found two remaining recovery gaps: silent local media closure, and delayed held-key release after input loss. Frontend corrections and focused checks are complete; the unambiguous server failure acknowledgment and updated deployment verification are in progress. Five of six alternating interaction runs passed; the sixth caught the held-key failure, so the comparison is not accepted. Separate scrolling on a real documentation page passed in both modes. Speed benefit and long-duration validation remain unproven. One combined design review only; later checks are bounded implementation-finding verification.

All project paths below are relative to the explicit root `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo` for internal ownership only. Implementers must present absolute paths to the user. Branch browser-improvements; main and installed Mac10994 remain untouched. Preserve existing local edits. Agents share the repository and must not revert one another's work.

## Dependency order and parallel ownership

1. **Backend worker with lead coordination — contract freeze (first, small serial task).** Own `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/contracts/asyncapi.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/contracts/components/schemas/`, and every generated artifact. Define input offer/answer/state, peer epoch, control epoch/acknowledgment, reliable-boundary and hover-sequence fields; regenerate once with `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/scripts/gen-contracts.sh`. Confirm common frame limits and backend/frontend agreement before fanout. Review seam includes navigation/tab/viewport messages crossing the separate input route.
2. **Worker A — backend, parallel after contract freeze.** Own new dedicated-peer implementation in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/pkg/tools/browser/webrtc/`, gateway input signaling/attachment lifecycle in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/pkg/gateway/`, and related Go tests. Reuse current input gates, cancellation and borrowed muxes. Implement merged bounded scheduler, validation and held-release cleanup. Do not modify contracts or frontend.
3. **Worker B — frontend, parallel with A.** Own `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/src/lib/browserInputWebRTC.ts`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/src/lib/browserLiveWs.ts`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/src/components/browser/BrowserLiveView.tsx` and related tests. Implement two channels, fresh-hover barriers, reliable dragging/scrolling, explicit A/B selection, independent input error/Retry and existing frame guards. Do not change media encoding or generated files.
4. **Worker C — validation preparation, parallel with A/B.** Own a new remote comparison fixture/test under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/tests/browser-acceptance/` and evidence scripts under `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/`. Use a Mac-hosted viewer against Amsterdam, verify actual mode, exact input and scroll distance. Prepare short smoke, baseline capture and rollback commands. Do not touch product source or deploy partially integrated work.
5. **Lead — integration and EARLY HANDOFF.** Review combined ownership/ordering seams while workers complete focused checks. Run one short real-browser correctness smoke, build SPA plus candidate once, deploy to Amsterdam with configuration preserved, verify actual binary/image, run the short Mac-to-Amsterdam smoke against that deployed image in both modes, then send exact baseline/new-mode links and selected agent to user immediately. Tag as experimental; original image remains rollback. Do not wait for 20-minute soaks, unrelated CI fixes or final performance tuning.
6. **After user access.** Collect user feedback and paired remote measurements, fix only demonstrated issues, run relevant checks. One consolidated CI when this experiment is settled. Keep remaining candidate CI issues and Mac memory refusal separate; do not call the experiment release-ready.

## Minimal test gates

- Before behavior implementation: focused red tests for delayed hover across a reliable boundary, stale peer/attachment rejection and source-owned key release.
- Before early handoff: relevant Go race suite (one at a time), frontend transport/routing tests and typecheck, generated schema checks, ten exact clicks, Unicode text, scroll distance, drag, tab/resize, input disconnect/retry and unchanged media track identity. All must pass; missing test coverage is recorded rather than claimed.
- Performance evidence: short alternating baseline/new runs from Mac to Amsterdam, unchanged fixture/viewport/media settings, per-mode actual route proof. Record p95 and correctness without moving the 200 ms controlled target. Website navigation/load is measured separately.

## Scope and rollback

No automatic WS fallback, unreliable drag/scroll, three-connection media split, new codec, tool-system modification or memory-threshold relaxation. Keep default WS for the experiment until results justify changing it. An experiment selector is temporary, per-viewer and fixed before attach; changing mode requires a fresh owner with old input canceled first.

Fallback image: `registry.fly.io/uat-omnipus:browser-improvements-32de1e610-stability@sha256:b5aaaa0a383e8cda1bc8c240212ec6c02b9070b3d427078098aaae660dc0339a`. Preserve running-machine configuration and persistent data. Mac testing uses isolated11094 only when memory headroom permits; Linux early access must not wait for that unrelated host issue.

## Review budget

One combined ADR/spec grill round. Lead incorporates concrete findings once; no repeated grill loop. Implementation still gets focused correctness review and relevant tests, without new formal grill rounds or full CI per fix.

## Integration corrections

Focused implementation review found and corrected silent attachment-timeout failures, stale control epochs on asynchronous peer-state messages, incomplete joining of input dispatch during peer replacement, and reliable events overtaken by control messages. All three input counters now restart at each control epoch on both sides; late events from the previous epoch are ignored. Regression checks cover exact Unicode delivery through real WebRTC channels, held-source retirement, control acknowledgments, peer-scoped state delivery, and the overtaken-event case.

Early handoff completed: dedicated input passed 13 live checkpoints and WebSocket passed 12, including exact gestures, tab/resize actions and recovery. Evidence and remaining limits are recorded in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/internal/browser-input-candidate-2026-09-10.md`.
