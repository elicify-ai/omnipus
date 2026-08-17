#!/usr/bin/env bash
# race-packages.sh
#
# Single source of truth for the `-race` package list. Prints one `./pkg/...`
# / `./tests/...` package-path token per line on stdout.
#
# Why this file exists (architect ruling, #615/#617/#618 hardening review,
# F5): the list used to be duplicated by hand in two places —
#   .github/workflows/pr.yml       ("Run go test -race" step)
#   deploy/ci-worker/runci.sh      (run_gorace)
# — with a separate checker script (scripts/check-race-package-lockstep.sh,
# now deleted) whose job was to catch the two copies drifting apart. That
# checker was itself unreliable: it captured tokens from a byte range that
# could include comment text (a comment quoting the full package list would
# satisfy it even if the REAL invocation two lines later had been narrowed to
# almost nothing), it only ever looked at the first matching invocation per
# file (a second `go test -race` line was invisible to it), and it compared
# packages only — never flags, timeouts, or `-run`/`-p` divergence, despite a
# header comment claiming a broader "byte-for-byte identical" invariant. A
# regex-based comparator can always be fooled by a shape its author didn't
# anticipate; the fix is to remove the second copy, not to keep hardening the
# comparator. `scripts/e2e-shards.sh` + `tests/e2e/shards.json` already prove
# the pattern for this project's OTHER cross-surface list (Playwright shards):
# one plan, both CI surfaces consume it, so there is nothing left to drift.
#
# Consumers (both MUST call this script and word-split its output into the
# `go test -race` argument list — never hardcode the package list again):
#   .github/workflows/pr.yml  — the "Run go test -race" step
#   deploy/ci-worker/runci.sh — run_gorace
#
# Because this file lives under scripts/, the ci-omnipus worker gets it from
# its own checkout on every run — unlike deploy/ci-worker/runci.sh, which
# requires a separate manual redeploy step (see deploy/ci-worker/CLAUDE.md's
# "Redeploying runci.sh") whenever it changes. That closes the exact skew
# class that has already bitten this project once (a deployed runci.sh 80
# lines stale with no flock mutex): the -race package list can no longer go
# stale on the worker independently of the repo.
#
# Usage:
#   scripts/race-packages.sh                 # one package path per line
#   go test -race -tags goolm,stdjson -count=1 -timeout 900s $(scripts/race-packages.sh) ./tests/...
#
# When adding or removing a package from the -race surface: edit ONLY the
# list below. Both pr.yml and runci.sh pick it up automatically — no other
# file needs to change, and there is no longer a separate script to keep in
# lockstep.

set -euo pipefail

cat <<'PACKAGES'
./pkg/sysagent/...
./pkg/config/...
./pkg/credentials/...
./pkg/channels/...
./pkg/entity/...
./pkg/agentstore/...
./pkg/agent/...
./pkg/gateway/...
./pkg/logger/...
./pkg/task/...
./pkg/plan/...
./pkg/tools/...
./pkg/providers/...
./tests/integration/...
PACKAGES
