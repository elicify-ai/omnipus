---
name: devpod-gh-token-resource
description: git push fails 401 — re-source /etc/profile.d/10-devpod-env.sh; the Bash shell snapshot carries a stale GH_TOKEN
metadata: 
  node_type: memory
  type: feedback
  originSessionId: b7a78e24-b362-40f9-bb84-d430cd72665b
  modified: 2026-07-20T12:27:47.104Z
---

In this elicify-devpod, the Bash tool's shell is initialized from a **snapshot** that can carry a **stale `GH_TOKEN`**, so `git push` / `gh` intermittently fail with HTTP 401 "Invalid username or token" even though a valid token exists in the environment. The credential helper is `!/usr/bin/gh auth git-credential`, so gh's (stale) token drives pushes.

**Fix — re-source the live devpod env before any push/gh op:**
```bash
set -a; source /etc/profile.d/10-devpod-env.sh 2>/dev/null; set +a
git push origin <branch>
```
The fresh `GH_TOKEN` (len 40, user `daniel-piatkowski-ai`) then returns HTTP 200.

**Why:** The operator pushed back — "why are you losing the credentials all the time … it should be in the environment variable you might need to reload the shell." Don't conclude "no valid credential exists" from a 401; re-source first. `GOPAT` is empty here — `GH_TOKEN` is the one that matters.

**How to apply:** Prepend the `source` line to any Bash call that runs `git push` or authenticated `gh` in this pod. Relates to [[planning-goals-epic]] branch work.
