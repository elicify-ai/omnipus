---
name: fly-restart-wipes-volumeless-state
description: "A `fly machine restart` on a Fly app with no persistent volume wipes ALL $OMNIPUS_HOME runtime state, not just in-memory counters"
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 9a5cc9d5-94c8-4246-b11e-938e082e3387
  modified: 2026-07-31T10:20:09.289Z
---

`fly machine restart <id> --app <app>` on a Fly.io app deployed WITHOUT a `[[mounts]]`
persistent volume discards the entire `$OMNIPUS_HOME` directory (tasks, sessions,
workspaces, credentials.json, config.json — everything) and reboots into a fresh-install
state. This is NOT limited to redeploys; a plain machine restart on an already-running
machine has the same effect if the app has no volume. Confirmed directly on
`omnipus-uat-swimlane` (2026-07-31): restarted machine `7812791a464028` intending only to
clear a stuck in-memory concurrency counter; afterward every file under `$OMNIPUS_HOME`
was freshly timestamped at boot time, 343 task files and the entire UAT workspace
(with its configured agent roster/delegation edges) were gone, replaced by a brand-new
onboarding-state workspace ULID.

**Why:** I treated "restart the box" as a low-risk, previously-approved recovery action
(precedent: the user had authorized a restart earlier in the same session for a genuinely
resource-exhausted box) and assumed — without checking — that it would only reset
in-memory process state. It does not distinguish; the whole `$OMNIPUS_HOME` lives on the
container's writable layer, and whatever boot/entrypoint logic runs treats a restart the
same as a fresh container start when there's no volume backing that path. Related:
[[browser-provisioning-deferred]] and prior session notes already flagged
"no persistent volume — root cause of ephemeral-state pain" for this exact app, but that
was filed under redeploys losing test data, not restarts — the risk is broader than
originally scoped.

**How to apply:** Before running `fly machine restart` (or any restart/stop-start action)
on ANY Fly app as a "safe" recovery step, first check whether the app has a `[[mounts]]`
volume (`fly volumes list --app <app>` or check `fly.toml`). If it does not, treat a
restart as equivalent to a full data wipe / fresh install — same blast radius as a
redeploy — and get explicit confirmation before doing it if there's any live session data
worth preserving (test results, configured workspaces/teams, in-flight tasks). If a
stuck-resource symptom needs clearing on a volumeless app, prefer a narrower fix (e.g. a
targeted API call to stop/cancel the specific stuck record) over a blanket restart.
