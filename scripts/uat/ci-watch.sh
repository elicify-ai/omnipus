#!/bin/bash
# Launch (if the worker lock is free) and watch a full CI run on ci-omnipus-3.
# Usage: ci-watch.sh <short-sha> [--no-launch]
# Emits one line per gate result, per REAL FAILURE package, and per e2e shard verdict, as they
# appear, then a final summary with pass/fail counts, then exits.
# Idle detection uses the lock itself (flock -n) and a bracketed pgrep pattern: a plain
# `pgrep -f runci.sh` inside `sh -c '...'` matches its own command line and always says busy.
set -uo pipefail

SHA="${1:?usage: ci-watch.sh <short-sha> [--no-launch]}"
LAUNCH=1
[ "${2:-}" = "--no-launch" ] && LAUNCH=0
APP=ci-omnipus-3
LOG="/tmp/ci-wrap-$SHA.log"

# fly prints "Connecting to ..." on stderr (discarded here); never strip lines by position.
remote() { fly ssh console --app "$APP" -C "sh -c '$1'" 2>/dev/null | grep -v '^Connecting to '; }

if [ "$LAUNCH" = 1 ]; then
  state=$(remote "if flock -n /tmp/runci.lock true; then echo FREE; else echo HELD; fi" | tail -1)
  if [ "$state" != "FREE" ]; then
    echo "WORKER LOCK HELD, not launching $SHA (state=${state:-unreachable})"
    exit 2
  fi
  remote "nohup /cache/runci.sh $SHA > $LOG 2>&1 & echo launched" | tail -1 | sed "s/^/CI $SHA: /"
fi

seen=0
misses=0
while :; do
  out=$(remote "grep -aE \"[a-z][a-z0-9-]* -> exit [0-9]+|REAL FAILURE|GATE FAILURE|e2e shard [a-z0-9-]+: (PASS|FAIL)|=== RESULT\" $LOG 2>/dev/null | sed -e \"s/\x1b\[[0-9;]*m//g\"")
  if [ -z "$out" ]; then
    misses=$((misses + 1))
    if [ "$misses" -ge 30 ]; then
      echo "CI $SHA: WATCHER GAVE UP — no log lines after 30 polls (is the run alive? $LOG)"
      exit 3
    fi
    sleep 60
    continue
  fi
  misses=0
  total=$(printf '%s\n' "$out" | wc -l | tr -d ' ')
  if [ "$total" -gt "$seen" ]; then
    printf '%s\n' "$out" | tail -n +"$((seen + 1))" | sed "s/^/CI $SHA: /"
    seen=$total
  fi
  if printf '%s\n' "$out" | grep -q "=== RESULT"; then
    gates_fail=$(printf '%s\n' "$out" | grep -cE -- '-> exit [1-9]')
    gates_ok=$(printf '%s\n' "$out" | grep -cE -- '-> exit 0')
    sh_pass=$(printf '%s\n' "$out" | grep -cE 'e2e shard [a-z0-9-]+: PASS')
    sh_fail=$(printf '%s\n' "$out" | grep -cE 'e2e shard [a-z0-9-]+: FAIL')
    echo "CI $SHA: FINISHED — gates ok=$gates_ok failed=$gates_fail; e2e shards passed=$sh_pass failed=$sh_fail of $((sh_pass + sh_fail))"
    printf '%s\n' "$out" | grep -E -- '-> exit [1-9]|e2e shard [a-z0-9-]+: FAIL|REAL FAILURE' | sed "s/^/CI $SHA: RED: /"
    exit 0
  fi
  sleep 90
done
