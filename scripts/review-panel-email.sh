#!/usr/bin/env bash
# Local review instance for the founder's end-to-end review of the side panel
# + email feature (branch review/panel-email). Starts, from THIS checkout:
#   1. the fake mail server (scripts/review-fakemail.sh: loopback IMAP 1143,
#      SMTP 1025, control 1180; agent@test.local and alice@test.local, -deliver)
#   2. an Omnipus gateway on an ISOLATED data folder (never ~/.omnipus)
# Ctrl-C stops both.
#
# Usage: scripts/review-panel-email.sh [--no-build] [--fresh]
#   --no-build  reuse build/omnipus instead of running `make build`
#   --fresh     wipe the review data folder first (new onboarding)
# Env: REVIEW_HOME (default /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-review/home)
#      REVIEW_PORT (default 5199)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
REVIEW_HOME="${REVIEW_HOME:-/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-review/home}"
REVIEW_PORT="${REVIEW_PORT:-5199}"
BUILD=1
FRESH=0
for arg in "$@"; do
  case "$arg" in
    --no-build) BUILD=0 ;;
    --fresh) FRESH=1 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

# Never touch the live instance's data folder.
case "$(cd "$(dirname "$REVIEW_HOME")" 2>/dev/null && pwd)/$(basename "$REVIEW_HOME")" in
  "$HOME/.omnipus"|"$HOME/.omnipus/"*)
    echo "REFUSED: REVIEW_HOME points at the live data folder ($REVIEW_HOME)" >&2; exit 2 ;;
esac

for p in "$REVIEW_PORT" 1143 1025 1180; do
  if lsof -nP -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "REFUSED: port $p is already in use (another review instance or fakemail running?)" >&2; exit 2
  fi
done

cd "$ROOT"
if [ "$BUILD" = 1 ]; then
  echo "Building Omnipus from $(git rev-parse --abbrev-ref HEAD) @ $(git rev-parse --short HEAD) ..."
  make build
fi
BIN="$ROOT/build/omnipus"
[ -x "$BIN" ] || { echo "BLOCKED: $BIN not found; run without --no-build" >&2; exit 2; }

if [ "$FRESH" = 1 ]; then rm -rf "$REVIEW_HOME"; fi
mkdir -p "$REVIEW_HOME"
if [ ! -f "$REVIEW_HOME/config.json" ]; then
  umask 077
  printf '{\n  "version": 1,\n  "gateway": { "port": %s }\n}\n' "$REVIEW_PORT" > "$REVIEW_HOME/config.json"
fi

LOGDIR="$(dirname "$REVIEW_HOME")/logs"
mkdir -p "$LOGDIR"
"$ROOT/scripts/review-fakemail.sh" > "$LOGDIR/fakemail.log" 2>&1 &
FAKEMAIL_PID=$!
cleanup() {
  kill "$FAKEMAIL_PID" 2>/dev/null || true
  pkill -P "$FAKEMAIL_PID" 2>/dev/null || true
  [ -n "${GW_PID:-}" ] && kill "$GW_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

for _ in $(seq 120); do
  lsof -nP -iTCP:1143 -sTCP:LISTEN >/dev/null 2>&1 && break
  kill -0 "$FAKEMAIL_PID" 2>/dev/null || { echo "fakemail exited; see $LOGDIR/fakemail.log" >&2; exit 1; }
  sleep 1
done
lsof -nP -iTCP:1143 -sTCP:LISTEN >/dev/null 2>&1 || { echo "fakemail did not open port 1143; see $LOGDIR/fakemail.log" >&2; exit 1; }

OMNIPUS_HOME="$REVIEW_HOME" OMNIPUS_BEARER_TOKEN='' "$BIN" gateway --allow-empty > "$LOGDIR/gateway.log" 2>&1 &
GW_PID=$!
for _ in $(seq 60); do
  curl -fsS "http://localhost:$REVIEW_PORT/health" >/dev/null 2>&1 && break
  kill -0 "$GW_PID" 2>/dev/null || { echo "gateway exited; see $LOGDIR/gateway.log" >&2; exit 1; }
  sleep 1
done
curl -fsS "http://localhost:$REVIEW_PORT/health" >/dev/null 2>&1 || { echo "gateway not healthy; see $LOGDIR/gateway.log" >&2; exit 1; }

cat <<INFO
================================================================
 Review instance is running.
   Open:         http://localhost:$REVIEW_PORT
   Data folder:  $REVIEW_HOME   (isolated; not your live instance)
   Logs:         $LOGDIR/gateway.log, $LOGDIR/fakemail.log
 First start: complete onboarding with provider openrouter,
   model z-ai/glm-5.3-flash.
 Mailbox to add (Connectors -> Email):
   IMAP 127.0.0.1:1143  SMTP 127.0.0.1:1025  security: none (loopback)
   Username agent@test.local  Password s3cret
   Mail sent to alice@test.local arrives in alice's INBOX.
 Put a sample message in the agent's inbox:
   curl -s http://127.0.0.1:1180/inject -d '{"user":"agent@test.local","subject":"Welcome","text":"Hello agent."}'
 Ctrl-C stops the gateway and the fake mail server.
================================================================
INFO
wait "$GW_PID"
