# Browser attachment cancellation and publication

## Scope

Branch `browser-improvements-startup-attach` starts at integrated commit
`61f11867c`. It changes only registry attachment, the gateway attach handler,
and its callback/initial-availability delivery. The installed browser instance
on port 10994 is untouched.

`AttachContext` acquires the tab-command gate with both the original caller's
cancellation and the existing PageTimeout admission cap. It then releases the
admission timer and uses the caller for the shared `SessionContext` startup body
(`sessionUnderGate`). One gate remains held through target selection, viewer
registration, and the initial tabs snapshot. There is no new overall attachment
timeout. Accepted viewers and tabs retain their own lifetimes.

Failed registration decrements the manager's count before releasing its gate,
then removes the viewer and cleans up input/control outside that gate. Initial
and later tab snapshots use the bounded latest-state slot. Status/control,
attach-ok, and initial WebRTC availability retain the original request's
publication context. The handler's temporary work child is canceled on return;
the original publication context remains owned by attachment replacement/clear.

The supported gateway path has one serial attachment worker per connection and
cleans up before that worker accepts the next attachment. No extra same-viewer
token or registration map was introduced.

## Reproduced failures

- Driver 63219 exited 1. Four registry failures and the accepted-lifetime control
  ran in 3.243 seconds. Pre-canceled/admission-canceled requests created viewers;
  cancellation inside initial tabs publication retained the new viewer; cold
  launch ignored cancellation. The accepted viewer survived caller cancellation.
- The same driver's gateway stage exited 1 in 9.698 seconds: status/control and
  initial availability remained deliverable after replacement, the tabs callback
  waited for a full critical queue, and the real attach handler remained blocked
  on a controlled remote CDP command after its attachment was replaced.
- Gate regression 46676 exited 1 in 1.661 seconds: a target switch completed
  during initial attachment publication. The existing PageTimeout admission
  control passed. The earlier attempt 21594 was a test compilation error and is
  excluded from behavioral evidence.

The gateway cancellation fixture uses a real WebSocket/CDP boundary and the real
handler/manager. It starts no Chrome process. All controlled workers and sockets
are drained before test completion.

## Impact and remaining verification

GitNexus reported LOW risk for the existing edited symbols. `handleAttach` had
one direct caller and three affected symbols including ServeHTTP flows;
`Attach` and the availability helper had no indexed direct callers. Manual
tracing confirmed the gateway's serial attachment worker. Newly introduced
context/callback helpers are outside the older index.

Closeout driver 94193 exited 0. Initial focused green passed eight browser
leaves in 1.745 seconds and nine gateway leaves in 3.579 seconds. Three deliberate
faults were caught: releasing admission before initial publication, removing the
existing admission cap, and discarding the actual handler request lifetime.
Restored race/shuffle verification passed the same eight browser leaves in
2.755 seconds and nine gateway leaves in 8.967 seconds, with no skips or race
warnings. The selection includes legacy tab rebinding, failed-attachment error
scope, and availability controls; no broader startup or relay suites were rerun.

The staged GitNexus attempt 83499 exited 1 because this isolated worktree is
unregistered and repository selection is ambiguous. It was not redirected to
the root checkout. Manual staged scope is limited to the nine assigned source,
test, and evidence files; whitespace checks passed. Integrated graph inspection
and full CI remain with the parent.

The actual-handler regression covers cancellation during pending CDP work;
successful video attachment and real-browser latency remain integrated runtime
checks. No startup or video speed improvement is claimed from these fixtures.
