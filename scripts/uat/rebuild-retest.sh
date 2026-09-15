#!/bin/bash
# UAT re-test rebuild window (plan: evidence/2026-09-13/retest/RETEST-PLAN-2026-09-14.md).
# Usage: rebuild-retest.sh <short-sha>
#   Builds SPA + binary from wt-integrate at <short-sha>, stops the UAT gateway by
#   exact pid, adds the tester accounts, restarts on 127.0.0.1:5177, proves every
#   account can log in. Never prints passwords. Never signals a process group.
set -euo pipefail

SHA="${1:?usage: rebuild-retest.sh <short-sha>}"
# Build from a dedicated worktree by default: wt-integrate is shared with agents that
# compile pkg/gateway, and step 2 briefly deletes pkg/gateway/spa.
WT="${WT:-/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-uat-build}"
U=/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat
HOMEDIR="$U/home"
BASE=http://127.0.0.1:5177
ACCOUNTS="$U/tester-accounts.txt"
PATCH="$U/retest-users-patch.json"
export PATH=/usr/local/go/bin:$HOME/go/bin:$PATH

step() { printf '\n== %s\n' "$*"; }
die() { printf 'ABORT: %s\n' "$*" >&2; exit 1; }

step "1. Check the source tree"
head_sha=$(git -C "$WT" rev-parse --short HEAD)
[ "$head_sha" = "$SHA" ] || die "$WT HEAD is $head_sha, expected $SHA"
if git -C "$WT" status --porcelain --untracked-files=no | grep -q .; then
  die "$WT has uncommitted changes"
fi
[ -f "$ACCOUNTS" ] || die "missing $ACCOUNTS"
[ -f "$PATCH" ] || die "missing $PATCH"

step "2. Build the SPA and sync it into the Go embed folder"
( cd "$WT" && npm run build ) > "$U/build-spa.log" 2>&1 || die "SPA build failed, see $U/build-spa.log"
rm -rf "$WT/pkg/gateway/spa"
mkdir -p "$WT/pkg/gateway/spa"
cp -R "$WT"/dist/spa/* "$WT/pkg/gateway/spa/"
for marker in library-add-mount-dialog-refused library-add-mount-dialog-broad; do
  if ! grep -q "$marker" "$WT"/pkg/gateway/spa/assets/*.js; then
    die "embedded SPA is stale: marker '$marker' not found"
  fi
done
echo "SPA synced; newest markers present"

step "3. Build the binary"
( cd "$WT" && CGO_ENABLED=0 go build -tags goolm,stdjson -o "$U/omnipus.$SHA" ./cmd/omnipus/ ) \
  > "$U/build-go.log" 2>&1 || die "Go build failed, see $U/build-go.log"
ls -la "$U/omnipus.$SHA"
printf 'SPA=0\nSYNC=0\nGO=0\nSHA=%s\n' "$SHA" > "$U/build-status.txt"

step "4. Back up the config"
if [ -f "$HOMEDIR/config.json.pre-retest" ]; then
  cp "$HOMEDIR/config.json" "$HOMEDIR/config.json.pre-retest.$(date +%Y%m%d%H%M%S)"
else
  cp "$HOMEDIR/config.json" "$HOMEDIR/config.json.pre-retest"
fi

step "5. Stop the running gateway by exact pid"
if [ -f "$HOMEDIR/gateway.pid" ]; then
  pid=$(cat "$HOMEDIR/gateway.pid")
  if ps -o comm= -p "$pid" 2>/dev/null | grep -q omnipus; then
    kill -TERM "$pid"
    for _ in $(seq 1 60); do
      ps -p "$pid" > /dev/null 2>&1 || break
      sleep 0.5
    done
    ps -p "$pid" > /dev/null 2>&1 && die "gateway pid $pid did not exit within 30 s"
    echo "stopped pid $pid"
  else
    echo "pid file names $pid, which is not an omnipus process; nothing to stop"
  fi
fi
if lsof -nP -iTCP:5177 -sTCP:LISTEN > /dev/null 2>&1; then
  die "port 5177 is still in use"
fi

step "6. Install the binary and merge the tester accounts"
[ -f "$U/omnipus" ] && cp "$U/omnipus" "$U/omnipus.before-retest"
cp "$U/omnipus.$SHA" "$U/omnipus"
python3 - "$HOMEDIR/config.json" "$PATCH" <<'PY'
import json, os, sys, tempfile
cfg_path, patch_path = sys.argv[1], sys.argv[2]
cfg = json.load(open(cfg_path))
patch = json.load(open(patch_path))
users = cfg.setdefault("gateway", {}).setdefault("users", [])
have = {u.get("username") for u in users}
added = []
for u in patch["gateway"]["users"]:
    if u["username"] not in have:
        users.append(u)
        added.append(u["username"])
fd, tmp = tempfile.mkstemp(dir=os.path.dirname(cfg_path))
with os.fdopen(fd, "w") as f:
    json.dump(cfg, f, indent=2)
os.chmod(tmp, os.stat(cfg_path).st_mode & 0o777)
os.replace(tmp, cfg_path)
print("accounts added:", added or "none (already present)")
print("accounts now:", [u.get("username") for u in users])
PY

step "7. Start the gateway"
(
  cd "$U"
  OMNIPUS_HOME="$HOMEDIR" nohup ./omnipus gateway --allow-empty >> "$U/gateway.log" 2>&1 &
)
ok=""
for _ in $(seq 1 120); do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/v1/state" || true)
  if [ "$code" = "200" ]; then ok=1; break; fi
  sleep 1
done
[ -n "$ok" ] || die "gateway did not answer /api/v1/state within 120 s; see $U/gateway.log and $HOMEDIR/logs/gateway_panic.log"
echo "gateway up, pid $(cat "$HOMEDIR/gateway.pid" 2>/dev/null || echo '?')"

step "8. Prove every account can log in"
jar=$(mktemp)
login_check() {
  local user="$1" pass="$2" code
  : > "$jar"
  code=$(curl -s -c "$jar" -b "$jar" -o /dev/null -w '%{http_code}' \
    -H 'Content-Type: application/json' -X POST "$BASE/api/v1/auth/login" \
    --data "$(python3 -c 'import json,sys; print(json.dumps({"username":sys.argv[1],"password":sys.argv[2]}))' "$user" "$pass")")
  local csrf
  csrf=$(awk '$6 ~ /csrf/ {v=$7} END {print v}' "$jar")
  curl -s -b "$jar" -o /dev/null -H "X-Csrf-Token: $csrf" -X POST "$BASE/api/v1/auth/logout" || true
  printf '%-10s login=%s\n' "$user" "$code"
  [ "$code" = "200" ]
}
for required in founder admin uat-op2 uat-x1 uat-x2 uat-t2 uat-t3 uat-t4 uat-t5; do
  grep -q "^$required " "$ACCOUNTS" || die "account $required missing from $ACCOUNTS"
done
fail=0
first=1
while read -r user pass; do
  [ -n "$user" ] || continue
  [ "$first" = 1 ] || sleep 7   # stay under the per-IP login limiter
  first=0
  login_check "$user" "$pass" || fail=1
done < "$ACCOUNTS"
rm -f "$jar"
[ "$fail" = 0 ] || die "at least one account could not log in"

step "DONE: UAT instance rebuilt at $SHA with all tester accounts"
