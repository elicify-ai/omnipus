#!/usr/bin/env bash
# DIAGNOSTIC ONLY. The product's LibreOffice start (same executable, flags,
# environment, call-folder layout, exit-81 restart) under a re-created
# converter Landlock ruleset, plus the named extra /tmp rights, traced.
# usage: emulated.sh <log dir> <none|sock|sock+remove>
set -uo pipefail
logs="${1:?log dir}"; tmp="${2:?tmp rights}"; office="${OMNIPUS_TEST_OFFICE_ROOT:?}"; here="$(cd "$(dirname "$0")" && pwd)"
tag="emulated-${tmp/+/-}"
call="$(mktemp -d "$RUNNER_TEMP/$tag-call.XXXXXX")"
mkdir -p "$call"/{in,out,home,tmp,profile/user}
python3 -I "$here/make_xlsx.py" "$call/in/document.xlsx"
rules=(--rx "$office")
for d in /lib /lib64 /usr/lib /usr/lib64 /etc/ld.so.cache; do [ -e "$d" ] && rules+=(--rx "$d"); done
for d in /usr/share/fonts /usr/local/share/fonts /etc/fonts; do [ -e "$d" ] && rules+=(--ro "$d"); done
rules+=(--rw "$call" --tmp "$tmp")
out="$logs/$tag.txt"
{ echo "call=$call"; echo "rules: ${rules[*]}"; echo "pipes before: $(find /tmp /var/tmp -maxdepth 1 -name 'OSL_PIPE*' | tr '\n' ' ')"; } > "$out"
for attempt in 1 2; do
  ( cd "$call" && env -i PATH=/usr/bin:/bin LANG=en_US.UTF-8 HOME="$call/home" TMPDIR="$call/tmp" \
    strace -f -yy -tt -s 512 -e trace=%file,%network,%process -o "$logs/$tag-start$attempt.strace" \
    python3 -I "$here/landlock_run.py" "${rules[@]}" -- \
    "$office/program/soffice.bin" --headless --invisible --nologo --nodefault --norestore --nolockcheck \
    "-env:UserInstallation=file://$call/profile" --convert-to pdf --outdir "$call/out" "$call/in/document.xlsx" \
    > "$logs/$tag-start$attempt.out" 2>&1 < /dev/null )
  code=$?
  echo "start $attempt exit=$code pdf=$( [ -s "$call/out/document.pdf" ] && echo yes || echo no )" >> "$out"
  [ -s "$call/out/document.pdf" ] && break
  [ "$code" = 81 ] || break
done
echo "pipes after: $(find /tmp /var/tmp -maxdepth 1 -name 'OSL_PIPE*' | tr '\n' ' ')" >> "$out"
cat "$out"
for f in "$logs/$tag"-start*.strace; do echo "-- denied in $f:"; grep -E '= -1 E(ACCES|PERM|XDEV|ROFS)' "$f" | cut -c1-300 | head -60; done
# leave /tmp clean for the next variant, but record what we removed
for p in $(find /tmp -maxdepth 1 -name 'OSL_PIPE*'); do echo "cleanup: removing leftover $p" >> "$out"; rm -f "$p"; done
