# pkg/tools/browser — live browser

## Running tests here

1,000+ tests in this package (1,300+ with subpackages), and many are gated
on a real Chrome (`skipIfNoBrowser`) — they skip locally, which is expected,
not a failure. Scope to one symbol (`CGO_ENABLED=0 go test -tags goolm,stdjson
-run '^TestCoordinator_OwnershipMarker_RoundTrip$' -p 1
./pkg/tools/browser/`) — the coordinator's ownership-marker round-trip (pid
+ identity), which runs without Chrome. CI is the authority for full-suite
results.

## WebRTC is the ONLY live-video path (ADR-061)

The JPEG screencast fallback is deleted in full: the CDP
`Page.startScreencast` / `ScreencastFrameAck` drivers, the frame-quality
consts, `LiveView`'s frame delivery and pause coordination, the
`BrowserScreencastFrame` wire schema with its generated Go/TS types, and the
SPA `<img>` base64 sink. This reverses ADR-047 D3, which kept both paths for
instant fallback — read ADR-061 before touching this area.

Why it must not come back: an `<img>` whose `src` swaps ~30x/second is
visually indistinguishable from video, so on WebRTC failure the panel silently
degraded to the slow path and looked completely normal — a fallback nobody can
detect hides the real defect indefinitely. It was also the expensive path:
base64 inside a JSON WS frame, ~80 KB/frame at q60 1280x720, decoded and
re-rendered on the browser's main thread, no inter-frame compression.

A WebRTC failure must be VISIBLE: a persistent error with the real reason and
a Retry — never a blank panel, never a silent degrade. The SPA surfaces every
reason, including the capability-gate ones that used to stay silent, via
`src/lib/browserWebRTC.ts::translateWebRTCFallbackReason`.

Guard: `scripts/check-no-jpeg-screencast.sh` fails the build if any of the
deleted symbols returns. Branches cut before the removal still contain the
whole pipeline, so a merge re-adds it as an ordinary conflict-free addition —
resolve by keeping the deletion. Legitimate JPEG uses (`browser_screenshot`,
media resize, uploads, the library preview) are deliberately out of the
guard's scope.
