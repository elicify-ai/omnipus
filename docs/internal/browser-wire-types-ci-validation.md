# Browser internal-type lint correction

PR 685, run `34216723190`, source `b49194849`: wire-types lint reported eight
findings. The contracts job also reached this same lint failure after generation.
The local unchanged lint reproduced all eight findings with exit 1.

Direct declaration/caller inspection found internal state, not handwritten wire
payloads:

- `captureHealthTracker`: watchdog history and failure classification.
- `browserCommandQueue` / `browserCommand`: mutex/cancellation state and queued
  execution closures with admission timestamps.
- `browserOutboundFrame`: local envelope retaining encoded bytes, context and
  an admission callback; only its encoded data is written.
- `BrowserAnnotationFrame`: local crop geometry and media-stream object identity.
- `BrowserPresentedFrame`: local browser video-frame callback metadata.
- `BrowserCaptureIdentity` / `BrowserPeerIdentity`: camelCase peer state and
  callback identity. Actual offers already use a generated-contract `Pick` with
  explicit field mapping; answers use the generated contract type.

Only eight declaration comments across six source files changed. Each explains
its actual internal use with the guard's required descriptive justification.
No guard, schema, generated type, runtime expression or type shape changed.

Validation:

- `bash scripts/check-no-handwritten-wire-types.sh`: baseline exit 1/eight
  findings; corrected exit 0/zero findings.
- `bash scripts/check-no-handwritten-wire-types.sh --self-test`: exit 0,
  39 assertions passed, zero failed, retaining real violation detection.
- Manual diff and whitespace checks passed. No Go, runtime or typecheck run:
  this correction changes comments only. Remote CI still needs the integrated
  commit; neither original failed job is relabeled as passing.

Original CI logs:

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-review-evidence/ci-wire-types.log`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-review-evidence/ci-contracts.log`
