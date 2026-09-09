# Browser-streaming spikes — evidence package

Supports ADR-071. Two spikes, both run 2026-08-30 on an Intel i7-1068NG7 Mac.

## One external dependency, not vendored

`pages/source-dash.html` loads `dash.all.min.js` (801 KB, third party). Fetch it
before running the DASH arm:

    curl -sLo pages/dash.all.min.js https://cdn.dashjs.org/latest/dash.all.min.js

The YouTube arm and the Safari arm need no external files.

## Not fully automated

`run-all.sh` drives the Chrome-side capture and the Chrome/Firefox replay.
**The real-Safari arm (§3.2 of the ADR) is not automated**: `safaridriver`
requires "Allow Remote Automation" and AppleScript injection requires "Allow
JavaScript from Apple Events", neither of which the spike enables. That arm
works by opening `pages/safari-viewer.html` in Safari by hand; the page POSTs
its own measurements to the local report server, and those payloads are in
`evidence/safari-reports/`.

`crossengine.js` covers Playwright's WebKit build, which is NOT the same as
real Safari — the ADR distinguishes them deliberately.

## What is here

- `shim.js` — scope-agnostic MSE interceptor (window and worker)
- `record.js` — CDP recorder, including the worker-injection sequence
- `codecgate.js` — AV1 denial: patches `isTypeSupported`, `canPlayType`,
  `mediaCapabilities.decodingInfo`
- `replay.js`, `serve.js` — replay driver and local server
- `analyse.js` — container-box classification, the INIT/BOUNDARY/CONT split
  behind the "96% mid-segment" figure, and bandwidth arithmetic
- `portability.js`, `crossengine.js` — cross-engine codec probes
- `pages/` — source, viewer and Safari harness pages
- `evidence/` — manifests, viewer stats, Safari report payloads, run logs

## What is NOT here

The captured media chunks (~170 MB across runs) are excluded deliberately.
The manifests in `evidence/` record per-append offsets, sizes, codecs, container
classification and load average, which is what the ADR's numbers are computed
from. Re-running regenerates the chunks.
