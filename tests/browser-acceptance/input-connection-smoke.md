# Dedicated input: early remote smoke

Run from the Mac against the verified Amsterdam experiment, after deployment.
This is a short correctness gate for both modes, not a latency acceptance or soak.
Use [input-connection.config.ts](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/tests/browser-acceptance/input-connection.config.ts); it intentionally has no global setup or server launch.

Required environment:

- `OMNIPUS_URL=https://uat-omnipus.fly.dev`
- `OMNIPUS_AUTH_FILE`: absolute authenticated Playwright state file.
- `BROWSER_INPUT_FIXTURE_CONFIG`: absolute JSON file with `url` pointing to the exact served [input-connection-fixture.html](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/tests/browser-acceptance/input-connection-fixture.html) (no query). The test verifies all served bytes before navigation. Reuse the existing preview registration; no model call is needed.
- `BROWSER_INPUT_PROVENANCE`: absolute JSON file with verified `source` commit and `binarySHA256`. These labels are recorded, not independently attested by the test; the deploy operator must verify them first.
- `BROWSER_PROBE_OUTPUT_DIR`: absolute evidence directory.

Command from the repository: `npx playwright test --config=/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/tests/browser-acceptance/input-connection.config.ts`.
Use `--grep websocket` or `--grep dedicated` for separately scheduled comparisons. Both use Browser UAT Test, the same viewport and fixture. The test selects the mode before attachment and observes actual native WebSocket/data-channel sends; the selector alone is not route proof.

Assertions cover ten initial clicks; exact `Zażółć 世界` text decoded from video; 300 CSS pixels of native scrolling; complete drag; retained state and working clicks after tab return and resize; held-key release on input loss; Retry with the original media receiver in dedicated mode; and input after recovery. Baseline signaling reconnect may replace the receiver and explicitly rebinds the continuity reference. Audio coverage is a live track, not audible content.

The local [input-connection-fixture.test.ts](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/tests/browser-acceptance/input-connection-fixture.test.ts) checks the harness's real canvas-to-video decoder with native target events and negative text/click/scroll cases. Its local frame pump is not part of the served fixture. It does not prove the gateway, encoder, network or dedicated connection works. Remote smoke remains required.

Do not change the existing 100-click/200ms acceptance based on these ten-click timings. Smoke timings include runner and pixel polling overhead. Keep remote input/scroll comparison separate from website loading and avoid claims about physical display timing.
