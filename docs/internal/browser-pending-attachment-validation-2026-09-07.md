# Pending browser attachment lifetime

Status: focused regression, mutation, and race verification passed; production
viewer negotiation and callback publication integration remain pending.

The connection identity contract requires one lifetime from receipt of an attach
request through its committed browser attachment. An early video offer must
wait for that exact request, not look up whichever attachment becomes current
later. A replacement, detach, failed attach, or caller timeout must end the wait
without borrowing the replacement's route. Committing the matching attachment
must preserve its original lifetime so initial callbacks and later input share
one identity.

Boundary: real connection-state methods and contexts, with inert manager pointers
as route identities. There is no browser or network transport in these state
tests. Cases cover live pending identity, identity preserved on commit, no old
command route while replacement is pending, wait through startup, caller timeout,
replacement cancellation, failed request cleanup, and stale failure cleanup that
must leave a newer request alive. Wait tests use a context-Done observation
barrier, not a sleep, to prove the waiter reached its cancellation boundary.

Planned mutations remove request-lifetime preservation, allow old request waits
to use a replacement, omit cancellation on failed attach, and let pending
commands inherit the prior committed route. Production viewer-offer and initial
state-publication wiring follow the state-machine fix; these focused state tests
will not by themselves establish that end-to-end behavior.

## Observed evidence

The initial focused run (process 32554, terminal exit 1) reproduced all eight
state-level cases. After preserving the pending request context through commit,
the expanded run (97021, terminal exit 1) passed those original cases and failed
three additional production-handoff regressions: taking the prior route canceled
the new request, stale work took a replacement route, and malformed attachment
input left the pending request alive.

The handoff now checks the request epoch under the attachment mutex and clears
only the prior route fields. The handler abandons an uncommitted request on
return; cleanup from an older request cannot cancel a newer one. These changes
passed the expanded focused gateway selection (18731, terminal exit 0,
4.582 seconds), including attachment, queued command, connection state, panel
resolution, detach, and attach/read-loop responsiveness cases. This result does not
verify viewer negotiation or initial callback publication, which still require
production integration.

## Review follow-up

Bounded independent review identified a missing assertion: malformed replacement
input must also relinquish the previous route, since receipt has already ended
its lifetime. A second regression requires malformed work from an older request
to leave the newer route and its outbound status queue untouched. These checks
were run red before moving the epoch-checked detach handoff before validation.

Both review regressions failed behaviorally in run 59306 (terminal exit 1,
6.879 seconds): the old route remained installed, and the stale request queued
an error frame. The handler now performs its epoch-checked detach handoff before
parsing. Bounded independent re-review confirmed this ordering resolves both
reported cases; asynchronous callback publication remains the separate scope below.

Final driver 67020 completed with exit 0. The expanded initial selection passed
in 6.345 seconds. Six deliberately injected faults each failed the intended
behavioral assertion: replaced commit lifetime, leaked prior route, omitted
failure cleanup, replacement borrowed by an old waiter, request canceled by
handoff, and validation bypassing handoff. Restored regression tests passed in
6.186 seconds; restored shuffled race tests passed in 9.237 seconds with no race
warning. No test assertions were weakened to obtain these results.

## Production integration still required

The dispatch path must capture an immutable attachment request before launching
the video-offer goroutine. The handler must await that request after basic
validation and capability checks, then reject a manager or chat-session mismatch.
It must pass the original attachment lifetime into the context-aware viewer
adapter; a temporary negotiation timeout must not become the established data
channel's lifetime. Answers and health notifications need the original offer and
capture identities, with canceled work suppressed before queueing and writing.

Initial tab callbacks run before attachment commit, so they must use the pending
request's lifetime and the existing latest-state queue. Commit must preserve that
lifetime. Tests must exercise a video offer received before attachment finishes,
a replacement while it waits, detach during negotiation, and an initial callback
queued before commit. A successful current request must still answer and permit
input after temporary negotiation cleanup. State-helper tests alone cannot close
this integration requirement.
