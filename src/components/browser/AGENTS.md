# Browser (UI)

`BrowserLiveView.tsx` (video + input) and `BrowserLivePanel.tsx`. Reached via
`src/routes/_app/browser-live.tsx` and the chat browser handover.

## Design system

Read `.claude/skills/omnipus-design-system/SKILL.md` before adding or changing any
control, color, spacing, or type value here — it states the CI-enforced rules and cites
the script or test for each.

## WebRTC is the ONLY live-browser video path (ADR-061)

- The JPEG screencast fallback is deleted in full, including the SPA sink
  (`<img src="data:image/jpeg;base64,...">` and its frame state).
  `scripts/check-no-jpeg-screencast.sh` fails the build on that pattern in
  this folder — a merge from a pre-ADR-061 branch re-adds it as an ordinary,
  conflict-free addition; resolve by keeping the deletion.
- Why it must not come back: an `<img>` swapped ~30x/second is visually
  indistinguishable from video, so a WebRTC failure silently degraded to the
  slow path and looked normal — a fallback nobody can detect hides the real
  defect indefinitely.
- A WebRTC failure must be VISIBLE: a persistent error naming the real reason
  (`translateWebRTCFallbackReason` from `@/lib/browserWebRTC`, used in
  `BrowserLiveView.tsx` — it surfaces capability-gate reasons too) plus Retry.
  Never a blank panel, never a silent degrade.
- The ban is on the LIVE path only: JPEG in `browser_screenshot`, media
  resize, uploads, and the library preview is out of the guard's scope.

## Tests

CI group `components-misc` (pattern includes `src/components/browser/`).
Local: `npx vitest run src/components/browser/`. A test file matching no group
pattern runs in NO CI job while CI stays green;
`scripts/check-vitest-coverage.mjs` is the tripwire.
