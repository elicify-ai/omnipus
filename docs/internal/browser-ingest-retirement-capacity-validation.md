# Installed ingest retirement capacity

The independent capture review found that successful ingest replacement launches
the old peer connection's native `Close` in a goroutine after installing its
replacement. The preparation worker then releases its capacity immediately.
Repeated successful replacements can therefore retain unbounded blocked cleanup
workers, even though failed private candidates already retain preparation slots.

The required behavior is a shared limit of four unfinished preparation or retired
cleanup operations. A successfully installed current connection consumes no slot
once its preparation finishes. A replacement answer must return without waiting
for its predecessor's cleanup. Saturation must preserve the installed connection;
completed cleanup must make capacity reusable.

The dedicated regression uses real Pion negotiation and the authenticated binding
route. An interceptor blocks native cleanup of installed connections. Four
successive replacements must answer while their predecessors remain blocked; the
next request must return the existing typed busy error without changing the
installed connection. Releasing the interceptor must permit a later replacement.
The fixture releases cleanup even when an assertion fails.

The authenticated preparation worker now publishes its buffered result before
running the retained old-connection cleanup synchronously. Its existing deferred
capacity release runs afterward. Failed private candidate cleanup remains inside
negotiation, before result publication. The legacy unbound entry point preserves
its asynchronous cleanup behavior; production authenticated ingestion uses the
bounded worker. No steady-state installed connection holds a preparation slot.

## Observed validation

- Baseline session `96775` exited 1: the fifth blocked retirement returned a
  successful 5,051-byte answer and displaced the current connection. Package
  duration was 4.922 seconds; test-owned cleanup drained before exit.
- Closeout driver `13322` exited 0. Initial focused green passed in 3.420 seconds.
- Moving retirement back into a goroutine reproduced both saturation failures.
- Delaying answer publication until after cleanup reproduced the explicit
  "replacement answer waited" assertion.
- Restored race/shuffle passed in 7.619 seconds: four top-level tests and three
  cancellation subtests, seven passing entries, zero skips, zero race warnings.
  The existing real-Pion teardown emits closed-stream diagnostic warnings; these
  are not race detector reports.

The exact restored selection covers installed-retirement saturation, failed
native-candidate saturation, canceled gathering (binding cancellation, offer
cancellation, replacement binding), and failed replacement preserving its prior
connection. No full package suite or CI was run for this correction.

Logs and the source-restoring fault driver are retained under
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-worktrees/startup/.startup-probe/ingest-retirement-*`.
No browser speed or installed-application behavior is established by this focused
resource test. Native cleanup is still not interruptible; its outstanding work is
bounded rather than forcibly terminated.
