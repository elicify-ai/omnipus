#!/usr/bin/env bash
# check-no-reasoning-channel-selfcheck.sh
#
# Proves that scripts/check-no-reasoning-channel.sh CAN fail — a guard that
# has never been seen to go red is not a guard (this repo's
# docs/internal/false-green-patterns.md records a guard test that passed
# 673/673 with the feature it guarded deleted). Also proves the guard's one
# narrow, documented exclusion (the Go sweep test file) actually passes
# clean, so it can't silently widen into "nothing is ever checked", and that
# markdown headings are NOT dropped as comments (a heading like
# "# Reasoning channel" must fire the guard, not green it).
#
# Builds a throw-away synthetic tree (pkg/cmd/src/contracts/docs/config, the
# roots the real script requires to exist) and drives the real script against
# it with REPO_ROOT — never touches the real scripts/ or pkg/ directories.
#
# Exit: 0 all cases behave, 1 a case misbehaves, 2 harness could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/check-no-reasoning-channel.sh"
[ -f "$CHECK" ] || { echo "selfcheck: missing $CHECK" >&2; exit 2; }

TMP="$(mktemp -d "${TMPDIR:-/tmp}/no-reasoning-channel.XXXXXX")" || exit 2
trap 'rm -rf "$TMP"' EXIT

fresh_tree() {
  rm -rf "$TMP/tree"
  mkdir -p "$TMP/tree/pkg" "$TMP/tree/cmd" "$TMP/tree/src" "$TMP/tree/contracts" \
           "$TMP/tree/docs/connectors" "$TMP/tree/docs/internal/architecture" "$TMP/tree/config" \
           "$TMP/tree/pkg/channels" "$TMP/tree/pkg/gateway" "$TMP/tree/pkg/config"
}

FAIL=0
expect() { # expect <label> <want-exit>
  local label="$1" want="$2" got
  REPO_ROOT="$TMP/tree" bash "$CHECK" >"$TMP/out" 2>&1
  got=$?
  if [ "$got" -ne "$want" ]; then
    echo "selfcheck FAIL: $label — wanted exit $want, got $got" >&2
    sed 's/^/    | /' "$TMP/out" >&2
    FAIL=1
  else
    echo "selfcheck ok:   $label (exit $got)"
  fi
}

# --- 1. clean synthetic tree -------------------------------------------------
fresh_tree
expect "clean synthetic tree" 0

# --- 2. doc surfaces -----------------------------------------------------------
fresh_tree
printf 'Example config:\n\n```json\n{\n  "reasoning_channel_id": ""\n}\n```\n' > "$TMP/tree/docs/connectors/telegram.md"
expect "connector doc carrying the reasoning_channel_id key" 1

fresh_tree
printf 'Set the reasoning channel to route extended traces.\n' > "$TMP/tree/docs/connectors/slack.md"
expect "connector doc prose 'reasoning channel'" 1

fresh_tree
printf '## Reasoning channel\n\nRemoved in v0.1.1.\n' > "$TMP/tree/docs/connectors/qq.md"
expect "connector doc markdown heading '## Reasoning channel' (headings are NOT comments)" 1

fresh_tree
printf 'Hyphenated prose: the reasoning-channel feature was removed.\n' > "$TMP/tree/docs/connectors/line.md"
expect "connector doc hyphenated prose 'reasoning-channel'" 1

fresh_tree
printf 'Export OMNIPUS_CHANNELS_TELEGRAM_REASONING_CHANNEL_ID to override.\n' > "$TMP/tree/pkg/channels/README.md"
expect "channels README carrying the env var spelling" 1

fresh_tree
printf 'The channel config rows cite `ReasoningChannelID()`.\n' > "$TMP/tree/docs/internal/architecture/AS-IS-architecture.md"
expect "AS-IS doc quoting the Go identifier" 1

fresh_tree
printf 'Ordinary connector setup: set the token, enable the channel, allow your user id.\n' > "$TMP/tree/docs/connectors/wecom.md"
expect "negative control: ordinary connector doc content" 0

# --- 3. shipped config files ----------------------------------------------------
fresh_tree
printf '{\n  "channels": { "telegram": { "reasoning_channel_id": "" } }\n}\n' > "$TMP/tree/config/config.example.json"
expect "config/config.example.json carrying the key" 1

fresh_tree
printf '{\n  "channels": { "telegram": { "reasoning_channel_id": "" } }\n}\n' > "$TMP/tree/pkg/gateway/config.json"
expect "pkg/gateway/config.json carrying the key" 1

# --- 4. code symbols -------------------------------------------------------------
fresh_tree
printf 'package channels\n\nfunc (c *BaseChannel) ReasoningChannelID() string { return "" }\n' > "$TMP/tree/pkg/fixture.go"
expect "ReasoningChannelID reintroduced in pkg/" 1

fresh_tree
printf 'package agent\n\nfunc (al *agentLoop) handleReasoning(ctx context.Context) {}\n' > "$TMP/tree/pkg/fixture.go"
expect "handleReasoning reintroduced in pkg/" 1

fresh_tree
printf 'package agent\n\nfunc spawnReasoningPublish() {}\n' > "$TMP/tree/pkg/fixture.go"
expect "spawnReasoningPublish reintroduced in pkg/" 1

fresh_tree
printf 'package config\n\nReasoningChannelID string `json:"reasoning_channel_id" env:"OMNIPUS_CHANNELS_X_REASONING_CHANNEL_ID"`\n' > "$TMP/tree/pkg/fixture.go"
expect "env var binding OMNIPUS_CHANNELS_X_REASONING_CHANNEL_ID reintroduced" 1

# --- 5. the one narrow exclusion: the enforcement surfaces ------------------------
fresh_tree
printf 'package config\n\nvar reasoningChannelBannedTokens = []string{"ReasoningChannelID"}\n' > "$TMP/tree/pkg/config/reasoning_channel_zero_trace_test.go"
expect "the Go sweep test file itself (excluded, exact path)" 0

# ...and nothing beyond that exact file is excluded.
fresh_tree
printf 'package config\n\nvar x = "ReasoningChannelID"\n' > "$TMP/tree/pkg/config/some_other_file.go"
expect "any OTHER file in the same directory still fails" 1

# --- 6. missing required directory -> cannot run ----------------------------------
fresh_tree
rm -rf "$TMP/tree/docs"
expect "missing required directory (docs/) — refusal, not a green" 2

if [ "$FAIL" -ne 0 ]; then
  echo "selfcheck: check-no-reasoning-channel.sh does not behave — see above" >&2
  exit 1
fi
echo "selfcheck: OK (check-no-reasoning-channel.sh provably fails on offenders and provably passes its documented exclusion)"
exit 0
