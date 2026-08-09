---
name: ci-worker-deployed-script-drift
description: "The ci-omnipus worker's /cache/runci.sh silently drifts from the repo copy — check md5 before trusting any verdict"
metadata: 
  node_type: memory
  type: project
  originSessionId: 90a5dcd1-4156-4d4a-9727-bab20b4a9cf8
  modified: 2026-07-28T08:00:14.234Z
---

**The deployed CI script is not the repo script, and the gap can be huge.**
`deploy/ci-worker/CLAUDE.md` warns that editing the repo file doesn't update
`/cache/runci.sh` — but understates it. Measured 2026-07-28:

| | lines | md5 | `flock` |
|---|---|---|---|
| repo `deploy/ci-worker/runci.sh` | 524 | `685b22ec…` | yes (`:35-43`) |
| deployed `/cache/runci.sh` | 444 | `517eb74c…` | **zero** |

~80 lines of drift. The most damaging omission: the `/tmp/runci.lock` mutex
(`flock -n 9`, then `flock -w 5400 9`) that CLAUDE.md documents as if deployed
**was not there at all**. Consequence: two sessions sharing the worker do NOT
queue — each run `git reset --hard`s `/cache/omnipus` to its own SHA underneath
the other, so both get garbage verdicts and neither is told. This repo has two
checkouts driven by separate agent sessions, so the collision is routine, not
theoretical.

**Before trusting any worker verdict:**
```bash
fly ssh console --app ci-omnipus -C "sh -c 'wc -l /cache/runci.sh; md5sum /cache/runci.sh'"
md5sum deploy/ci-worker/runci.sh          # compare
fly ssh console --app ci-omnipus -C "sh -c 'ps -ef | grep runci.sh | grep -v grep'"
```
A `runci.sh <sha> <gate>` process whose SHA isn't yours means someone else's run
is live — wait, don't launch.

**Never redeploy `/cache/runci.sh` while a run is active.** bash reads scripts
incrementally; overwriting one mid-execution makes the running shell execute
garbage from a shifted byte offset.

Note `ps aux` is unavailable on the worker (busybox-ish `ps`) — use
`sh -c 'ps -ef | …'`, and `fly ssh console -C` needs the `sh -c` wrapper for
anything with pipes.

Related: [[mechanism-not-property-defect-class]] — same family, a control that
looks deployed and isn't.
