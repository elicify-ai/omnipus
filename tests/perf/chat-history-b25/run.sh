#!/bin/bash
# One entry point; never starts Fly, installs dependencies or builds the gateway.
set -euo pipefail
HERE=$(python3 -I -c 'import pathlib,sys; print(pathlib.Path(sys.argv[1]).resolve().parent)' "$0")
if [ "${1:-}" = "--help" ]; then
  exec node "$HERE/evaluate.mjs" --help
fi
OUTPUT=""
PRIOR=""
for ARG in "$@"; do
  if [ "$PRIOR" = "--output" ]; then OUTPUT="$ARG"; fi
  case "$ARG" in --output=*) OUTPUT="${ARG#--output=}" ;; esac
  PRIOR="$ARG"
done
# Validate before creating a directory. No implicit output or founder-data path.
node --input-type=module -e 'const {safeOutput}=await import(process.argv[1]); safeOutput(process.argv[2]);' "$HERE/safety.mjs" "$OUTPUT"
mkdir -p "$OUTPUT"
chmod 700 "$OUTPUT"
if [ "$(uname -s)" = "Darwin" ]; then
  SLOTRUN=${OMNIPUS_SLOTRUN:-/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/slotrun.sh}
  exec bash "$SLOTRUN" squad-1266-b25 "$OUTPUT/slotrun.log" env OMNIPUS_B25_SLOTRUN=squad-1266-b25 node "$HERE/evaluate.mjs" "$@"
fi
# Reference host is already reserved by the dispatcher. There is intentionally
# no Fly client or runner change here; that authorization remains out of band.
exec node "$HERE/evaluate.mjs" "$@"
