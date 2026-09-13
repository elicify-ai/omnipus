# Dedicated input: early remote smoke

Run from the Mac against the verified Amsterdam experiment, after deployment.
This is a short correctness gate for both modes, not a latency acceptance or soak.
Use [input-connection.config.ts](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/tests/browser-acceptance/input-connection.config.ts); it intentionally has no global setup or server launch.

Required environment:

- `OMNIPUS_URL=https://uat-omnipus.fly.dev`
- `OMNIPUS_AUTH_FILE`: absolute authenticated Playwright state file.
- `BROWSER_INPUT_FIXTURE_CONFIG`: absolute JSON file with `url` pointing to the exact served [input-connection-fixture.html](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/tests/browser-acceptance/input-connection-fixture.html) (no query). The test verifies all served bytes before navigation. Reuse a live preview registration. Registrations are in memory and must be restored after gateway restart; this run used one authorized Browser UAT Test request limited to static `serve_web` registration.
- `BROWSER_INPUT_PROVENANCE`: absolute JSON file with verified `source` commit and `binarySHA256`. These labels are recorded, not independently attested by the test; the deploy operator must verify them first.
- `BROWSER_PROBE_OUTPUT_DIR`: absolute evidence directory.

Command from the repository: `npx playwright test --config=/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/tests/browser-acceptance/input-connection.config.ts`.
Use `--grep websocket` or `--grep dedicated` for separately scheduled comparisons. Both use Browser UAT Test, the same viewport and fixture. The test selects the mode before attachment and observes actual native WebSocket/data-channel sends; the selector alone is not route proof.

Assertions cover ten initial clicks; exact `Zażółć 世界` text decoded from video; 300 CSS pixels of native scrolling; complete drag; retained state and working clicks after tab return and resize; held-key release on input loss; Retry with the original media receiver in dedicated mode; and input after recovery. Baseline signaling reconnect may replace the receiver and explicitly rebinds the continuity reference. Audio coverage is a live track, not audible content.

The local [input-connection-fixture.spec.ts](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/tests/browser-acceptance/input-connection-fixture.spec.ts) checks the harness's real canvas-to-video decoder with native target events and negative text/click/scroll cases. Run it with [input-connection-fixture.config.ts](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/tests/browser-acceptance/input-connection-fixture.config.ts); the lib-store CI job executes the same four Playwright cases. Its local frame pump is not part of the served fixture. It does not prove the gateway, encoder, network or dedicated connection works. Remote smoke remains required.

Do not change the existing 100-click/200ms acceptance based on these ten-click timings. Smoke timings include runner and pixel polling overhead. Keep remote input/scroll comparison separate from website loading and avoid claims about physical display timing.

## Verified early-access checkpoint — 2026-09-10

Amsterdam source `f092b634d32b8acc5b9fba6b0cd18f011a4ebc3e`, verified binary SHA256 `0ccf3a4d99d8b8991c3b62b82bbd8f86b47111a9be3a696f64088cf74012d18b`:

- Dedicated: all 13 checkpoints passed, including input-only disconnection/retry with the original media receiver. Actual gesture sends: 45 reliable-channel, 1 hover-channel, zero WebSocket.
- Baseline: all 12 checkpoints passed, including signaling reconnection and input afterward. Actual gesture sends: 48 WebSocket.
- Both ended with 13 button clicks, 15 presses/releases, exact Unicode text, scroll position 300, one completed drag, no held input, no target errors, no page errors, and successful panel cleanup.

Initial attempts clicked before the existing current-page readiness gate cleared (initial navigation and baseline tab return). The final harness waits for that gate before each gesture stage; the exact event assertions remain unchanged. Dedicated passed before the final centralization of these waits; baseline passed with the final helper. This is a correctness checkpoint, not proof of faster interaction, audible audio, full CI, or sustained stability.

Retained evidence:

- Dedicated: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/remote-smoke-gate-f092b634d/input-connection-dedicated-f905a-rag-and-recovery-stay-exact/input-connection-evidence.json`
- Baseline: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/remote-baseline-final-f092b634d/input-connection-websocket-e1174-rag-and-recovery-stay-exact/input-connection-evidence.json`
