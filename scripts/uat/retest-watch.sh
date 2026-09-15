#!/bin/bash
# Watch the UAT re-test: one line per finished lane (LANE DONE), plus UAT gateway
# outages and panics. Exits when all 8 lanes are done.
# Runs under bash with nullglob: under zsh an unmatched glob (no reports yet) aborts.
set -u
shopt -s nullglob

R=/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/evidence/2026-09-13/retest
G=/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/gateway.log
P=/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/home/logs/gateway_panic.log
TOTAL=8

size() { if [ -f "$1" ]; then wc -c < "$1" | tr -d ' '; else echo 0; fi; }

seen=" "
done_count=0
down=0
gstart=$(size "$G")
pstart=$(size "$P")
echo "UAT re-test watcher started: $TOTAL lanes (op op2 x07 t1 t2 t3 t4 t5)"

while :; do
  for f in "$R"/report-retest-*.md; do
    lane=$(basename "$f" .md)
    lane=${lane#report-retest-}
    case "$seen" in *" $lane "*) continue ;; esac
    if tail -n 3 "$f" | grep -qx "LANE DONE"; then
      seen="$seen$lane "
      done_count=$((done_count + 1))
      echo "LANE DONE: $lane ($done_count of $TOTAL)"
    fi
  done

  code=$(curl -s -o /dev/null -m 10 -w '%{http_code}' http://127.0.0.1:5177/api/v1/state || true)
  if [ "$code" != "200" ]; then
    down=$((down + 1))
    [ "$down" -eq 2 ] && echo "UAT GATEWAY NOT ANSWERING (http=$code, twice in a row)"
  else
    down=0
  fi

  gnow=$(size "$G")
  if [ "$gnow" -gt "$gstart" ]; then
    tail -c +"$((gstart + 1))" "$G" | grep -aE "panic:|fatal error:|FTL" | head -3 | sed 's/^/UAT GATEWAY: /'
    gstart=$gnow
  fi
  pnow=$(size "$P")
  if [ "$pnow" -gt "$pstart" ]; then
    echo "UAT GATEWAY PANIC LOG GREW: see $P"
    pstart=$pnow
  fi

  if [ "$done_count" -ge "$TOTAL" ]; then
    echo "ALL $TOTAL LANES DONE"
    exit 0
  fi
  sleep 60
done
