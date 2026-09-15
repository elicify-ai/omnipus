# Health observation connection lifetime

Status: implemented; focused verification complete. Broader recovery integration
and release acceptance remain pending.

The recovery ownership contract requires a heartbeat and any sampled failure
to belong to a live authenticated encoder connection. Cancellation ends that
ownership immediately, before deferred socket unbinding. The tests retain real
CaptureSession binding, observation, and recovery code; only the relay/browser
transport is a fixture.

Cases derived from this contract: a current heartbeat is accepted; after original
connection cancellation, neither a bare heartbeat nor a structured sample may
change the stored observation or liveness timestamp. An already sampled failure
from that connection must not trigger recapture or publish recovery events.

Fault injection removed the heartbeat cancellation check, accepted bare
heartbeats despite cancellation, and removed the sampled-recovery cancellation
check. Remaining work includes comparing installed relay binding tokens and
received-packet progress, publishing complete frame health, and direct browser
recovery acceptance. These tests do not establish those properties.

GitNexus does not resolve the new RecordIngestHeartbeat and reportIngestLoss
helpers; graph impact is UNKNOWN. Manual scope covers the authenticated ingest
reader, sampled watchdog recovery, and relay loss callbacks. The callback path
without sampled evidence remains separate from heartbeat ownership.

## Evidence

The pre-fix run 6475 exited 1 in 1.529 seconds: both canceled heartbeat cases
changed liveness, the structured case replaced evidence, and the canceled
failure report requested one recapture and emitted lost/recovering events.

Driver 45843 exited 0. The focused health/loss/recovery selection passed in
1.873 seconds before mutation. All three mutations failed their intended
assertions. Restored tests passed in 1.479 seconds, followed by race detection
and shuffled execution in 3.957 seconds. The source was restored before both
final runs. These results cover the selected tests, not the full package suite.

A separate read-only review found no concrete defect in these guards or their
test oracles. It explicitly excluded the pending binding-aware received-packet
watchdog and does not replace the full release-diff review.

Staged GitNexus check 75656 exited 0 with LOW indexed impact (five files, one
nearby indexed callback, zero processes). Its attribution to onIngestLost does
not cover the unindexed helper bodies; manual inspection confirms the two
intended guard changes, the regression file, and the two evidence/ledger files.
