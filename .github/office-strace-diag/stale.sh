#!/usr/bin/env bash
# Stale-socket question: does LibreOffice remove its OSL_PIPE socket when a
# conversion is killed (SIGKILL / SIGTERM to its process group), and what does
# the next start on the same profile do with the leftover? Unsandboxed.
set -uo pipefail
logs="${1:?log dir}"; office="${OMNIPUS_TEST_OFFICE_ROOT:?}"; here="$(cd "$(dirname "$0")" && pwd)"
out="$logs/stale.txt"; : > "$out"
call="$(mktemp -d "$RUNNER_TEMP/stale-call.XXXXXX")"
mkdir -p "$call"/{in,out,home,tmp}
cp -a "$RUNNER_TEMP/initialized-profile" "$call/profile"   # past the first-start exit 81
python3 -I "$here/make_xlsx.py" "$call/in/document.xlsx"
run_env=(env -i PATH=/usr/bin:/bin LANG=en_US.UTF-8 HOME="$call/home" TMPDIR="$call/tmp")
args=(--headless --invisible --nologo --nodefault --norestore --nolockcheck "-env:UserInstallation=file://$call/profile" --convert-to pdf --outdir "$call/out" "$call/in/document.xlsx")
pipes() { find /tmp /var/tmp -maxdepth 1 -name 'OSL_PIPE*' 2>/dev/null | sort; }

kill_case() {
  local sig="$1" before new pid i
  rm -f "$call/out/document.pdf"
  before="$(pipes)"
  echo "== $sig case; pipes before: $(echo $before)" >> "$out"
  ( cd "$call" && exec setsid "${run_env[@]}" "$office/program/soffice.bin" "${args[@]}" > "$logs/stale-$sig.out" 2>&1 < /dev/null ) &
  pid=$!
  new=""
  for i in $(seq 1 3000); do
    new="$(comm -13 <(echo "$before") <(pipes))"
    [ -n "$new" ] && break
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.01
  done
  if [ -z "$new" ]; then
    echo "NOT MEASURED: no new socket appeared while the process ran (alive=$(kill -0 $pid 2>/dev/null && echo yes || echo no))" >> "$out"
    wait "$pid"; echo "exit=$?" >> "$out"; return
  fi
  echo "socket appeared: $new (after ~$((i*10)) ms); pdf yet: $( [ -s "$call/out/document.pdf" ] && echo yes || echo no )" >> "$out"
  ps -o pid,pgid,stat,cmd -g "$(ps -o pgid= -p "$pid" | tr -d ' ')" >> "$out" 2>&1
  kill "-$sig" -- "-$(ps -o pgid= -p "$pid" | tr -d ' ')" 2>>"$out"
  wait "$pid"; echo "exit after $sig: $?" >> "$out"
  sleep 1
  for p in $new; do
    if [ -e "$p" ]; then echo "LEFT BEHIND after $sig: $(stat -c '%F %a %U %n' "$p")" >> "$out"; else echo "REMOVED after $sig: $p" >> "$out"; fi
  done
  # The next normal start on the same profile, traced: what it does with a leftover.
  ( cd "$call" && "${run_env[@]}" strace -f -yy -tt -s 512 -e trace=%file,%network,%process \
      -o "$logs/stale-next-after-$sig.strace" "$office/program/soffice.bin" "${args[@]}" \
      > "$logs/stale-next-after-$sig.out" 2>&1 < /dev/null )
  echo "next start after $sig: exit=$? pdf=$( [ -s "$call/out/document.pdf" ] && echo yes || echo no )" >> "$out"
  for p in $new; do
    if [ -e "$p" ]; then echo "after next normal exit: STILL PRESENT $p" >> "$out"; else echo "after next normal exit: removed $p" >> "$out"; fi
  done
}
kill_case KILL
kill_case TERM
echo "pipes at end: $(pipes | tr '\n' ' ')" >> "$out"
cat "$out"
