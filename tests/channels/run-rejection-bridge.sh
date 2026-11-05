#!/usr/bin/env bash
# T-30c / S-90. Serial bridge; invoke with an absolute evidence-parent directory.
# The Go producer MUST pass and export actual response bytes before Node starts.
# Never run this script inside heavy: each of its two steps owns a separate slot.
set -u
set -o pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
parent=${1:?pass an absolute evidence-parent directory}
if [[ "$parent" != /* ]]; then
  printf 'Evidence directory must be absolute.\n' >&2
  exit 2
fi
mkdir -p "$parent"
receipt=$(mktemp -d "$parent/rejection-bridge.XXXXXXXX")
cd "$root" || exit 2
printf 'source=%s\nreceipt=%s\n' "$(git rev-parse HEAD)" "$receipt" > "$receipt/version.txt"
sha256sum pkg/gateway/channel_outbound_rejection_producer_test.go src/lib/api-error.ts tests/channels/rejection-decoder.probe.mjs >> "$receipt/version.txt"

producer=(/usr/local/bin/heavy --sync "$receipt" --output "$receipt" timeout --kill-after=30s 8m env CGO_ENABLED=0 "CHANNELS_REJECTION_EXPORT_PATH=$receipt/short-envelope.json" go test -tags=goolm,stdjson -p 1 -count=1 -timeout=6m -v -run '^TestT30c_ProductionRejection_SanitizesBoundsAndRequestPositions$/short-envelope$' ./pkg/gateway/)
printf 'command=' > "$receipt/producer.log"
printf '%q ' "${producer[@]}" >> "$receipt/producer.log"
printf '\n' >> "$receipt/producer.log"
"${producer[@]}" >> "$receipt/producer.log" 2>&1
rc=$?
printf '\nexit=%s\n' "$rc" >> "$receipt/producer.log"
printf '%s\n' "$rc" > "$receipt/producer.exit"
if (( rc != 0 )); then
  printf 'Producer did not pass (exit %s); decoder NOT RUN. Receipt: %s\n' "$rc" "$receipt"
  exit "$rc"
fi
if [[ ! -s "$receipt/short-envelope.json" ]]; then
  printf 'BLOCKED: producer passed but no fresh response artifact arrived. Receipt: %s\n' "$receipt" >&2
  exit 1
fi

# --local is deliberate: node_modules is a shared, untracked symlink in this
# authoring environment. The producer process is already joined and exited.
decoder=(/usr/local/bin/heavy --local timeout --kill-after=30s 3m node tests/channels/rejection-decoder.probe.mjs "$receipt/short-envelope.json")
printf 'command=' > "$receipt/decoder.log"
printf '%q ' "${decoder[@]}" >> "$receipt/decoder.log"
printf '\n' >> "$receipt/decoder.log"
"${decoder[@]}" >> "$receipt/decoder.log" 2>&1
rc=$?
printf '\nexit=%s\n' "$rc" >> "$receipt/decoder.log"
printf '%s\n' "$rc" > "$receipt/decoder.exit"
printf 'Decoder exit %s. Receipt: %s\n' "$rc" "$receipt"
exit "$rc"
