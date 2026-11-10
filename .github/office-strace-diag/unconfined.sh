#!/usr/bin/env bash
# The product's Linux LibreOffice start (pkg/preview/programs.go::officeConversion.run:
# same executable, flags, closed environment and call-folder layout), traced,
# WITHOUT any sandbox. Includes the exit-81 restart. Comparison only.
set -uo pipefail
logs="${1:?log dir}"; office="${OMNIPUS_TEST_OFFICE_ROOT:?}"; here="$(cd "$(dirname "$0")" && pwd)"
call="$(mktemp -d "$RUNNER_TEMP/unconfined-call.XXXXXX")"
mkdir -p "$call"/{in,out,home,tmp,profile/user}
python3 -I "$here/make_xlsx.py" "$call/in/document.xlsx"
echo "call=$call" > "$logs/unconfined.txt"
echo "before: $(find /tmp /var/tmp -maxdepth 1 -name 'OSL_PIPE*' 2>/dev/null | tr '\n' ' ')" >> "$logs/unconfined.txt"
touch "$logs/.marker-unconfined"; sleep 1
for attempt in 1 2; do
  ( cd "$call" && env -i PATH=/usr/bin:/bin LANG=en_US.UTF-8 HOME="$call/home" TMPDIR="$call/tmp" \
    strace -f -yy -tt -s 512 -e trace=%file,%network,%process -o "$logs/unconfined-start$attempt.strace" \
    "$office/program/soffice.bin" --headless --invisible --nologo --nodefault --norestore --nolockcheck \
    "-env:UserInstallation=file://$call/profile" --convert-to pdf --outdir "$call/out" "$call/in/document.xlsx" \
    > "$logs/unconfined-start$attempt.out" 2>&1 < /dev/null )
  code=$?
  echo "start $attempt exit=$code pdf=$( [ -s "$call/out/document.pdf" ] && echo yes || echo no )" >> "$logs/unconfined.txt"
  [ -s "$call/out/document.pdf" ] && break
  [ "$code" = 81 ] || break
done
echo "after: $(find /tmp /var/tmp -maxdepth 1 -name 'OSL_PIPE*' 2>/dev/null | tr '\n' ' ')" >> "$logs/unconfined.txt"
echo "--- files newer than the start, outside the call folder (tmp, var/tmp, dev/shm, runner home):" >> "$logs/unconfined.txt"
find /tmp /var/tmp /dev/shm "$HOME" -xdev -newer "$logs/.marker-unconfined" 2>/dev/null | grep -v "^$call" | head -200 >> "$logs/unconfined.txt"
cp -a "$call/profile" "$RUNNER_TEMP/initialized-profile"
cat "$logs/unconfined.txt"
