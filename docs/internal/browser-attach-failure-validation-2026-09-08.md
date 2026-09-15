# Failed browser attachment delivery

A failed attach request must deliver its error while that request is current.
Replacing or clearing the attachment must retire the queued error. Failure must
also release waiting viewer offers without publishing a route or allowing a late
commit.

The handler now completes failed pending work while retaining its original
publication context. It sends the failure through the scoped outbound queue.
Replacement, detach, and connection cleanup cancel that context. Unexpected
abandonment still cancels pending work. Future contextual attach integration must
cancel its operation child separately on return; publication lifetime is not a
license to retain startup or browser work after failure.

## Evidence

- Before implementation, root handle `28280` exited 1: the real handler's invalid
  JSON and missing-field cases both left an old queued error deliverable after a
  replacement request. Package elapsed time: 3.630 seconds.
- Root handle `58488` exited 0 after implementation for
  `^(TestBrowserAttachFailure|TestPendingAttachment)`, 3.560 seconds.
- Root driver `88103` exited 0. All three deliberate faults were caught by
  behavioral assertions: removing original message scope, leaving a failed
  request pending, and omitting notification to waiting offers.
- Restored `^(TestBrowserAttachFailure|TestPendingAttachment|TestBrowserScoped)`
  passed with `-race -shuffle=on`: 32 tests/subtests, zero failures, zero skips,
  no race warnings, 6.237 seconds. Tests use the real handler and outbound
  admission checks; they do not establish full browser behavior.
- Independent bounded review found no additional defect in this delta. Existing
  successful attachment callbacks and other outbound producers still require
  their planned scope integration.

The earlier invalid-frame test intentionally changes its expected lifetime:
failure completes the request without canceling its undelivered result. It still
asserts no route and no late commit, and additionally verifies a waiting offer
returns promptly. New tests check replacement and clear retire the error.

Commands use `go test -tags goolm,stdjson -p 1 ./pkg/gateway`, `GOMAXPROCS=2`,
`CGO_ENABLED=0` normally and `CGO_ENABLED=1` for race checks. Verification is
serial. The retained driver and log are:

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-attach-failure-mutations.py`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-attach-failure-mutations.log`

GitNexus rated `handleAttach` LOW, with one direct caller and one gateway request
flow. New helpers and the renamed test are absent from the index; their scope
was checked directly against source rather than treated as a low-risk result.
