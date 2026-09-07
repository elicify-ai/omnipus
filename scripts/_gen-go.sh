#!/usr/bin/env bash
# _gen-go.sh — Regenerate Go types from contracts/
#
# This script is consumed by scripts/gen-contracts.sh to regenerate
# the two Go generated files under pkg/api/generated/.
#
# Usage (from repo root):
#   bash scripts/_gen-go.sh
#
# Prerequisites:
#   - oapi-codegen v2 installed:
#       go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
#   - Go 1.22+ in PATH (needed by oapi-codegen for formatting)
#   - gopkg.in/yaml.v3 available in go.mod (already present)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# Resolve oapi-codegen: honour explicit OAPI_CODEGEN env, then GOBIN, then PATH.
if [ -n "${OAPI_CODEGEN:-}" ] && [ -x "$OAPI_CODEGEN" ]; then
  : # already set and executable — use it
elif [ -n "${GOBIN:-}" ] && [ -x "${GOBIN}/oapi-codegen" ]; then
  OAPI_CODEGEN="${GOBIN}/oapi-codegen"
elif command -v oapi-codegen >/dev/null 2>&1; then
  OAPI_CODEGEN="$(command -v oapi-codegen)"
else
  echo "oapi-codegen not found. Install with:" >&2
  echo "  go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest" >&2
  exit 1
fi

# Resolve go binary: honour explicit GO env, then PATH.
if [ -z "${GO:-}" ]; then
  if command -v go >/dev/null 2>&1; then
    GO="$(command -v go)"
  else
    echo "go not found in PATH" >&2
    exit 1
  fi
fi

echo "==> Generating openapi_types.gen.go via oapi-codegen..."
"$OAPI_CODEGEN" \
  -config pkg/api/generated/oapi-codegen-config.yaml \
  contracts/openapi.yaml

echo "==> Generating asyncapi_types.gen.go + llm_error_messages.gen.go via custom converter..."
# Third argument emits the LLMError user-facing copy catalogue from the
# x-user-messages block on components.schemas.LLMError. Codegen ABORTS if any
# code in the enum lacks a message or an attribution — that hard failure is what
# keeps pkg/agent/translate_error.go's catalogue exhaustive without a hand-written map.
CGO_ENABLED=0 "$GO" run ./scripts/gen-asyncapi-go/ \
  contracts/asyncapi.yaml \
  pkg/api/generated/asyncapi_types.gen.go \
  pkg/api/generated/llm_error_messages.gen.go

# Wire-format fixup: replace inline anonymous struct copies with named type
# references so Constraint #8 is honored at the codegen level. See
# scripts/gen-go-fixup.go for the rewrite rules.
echo "==> Applying wire-format fixup to openapi_types.gen.go..."
CGO_ENABLED=0 "$GO" run ./scripts/gen-go-fixup.go \
  -strict \
  -file pkg/api/generated/openapi_types.gen.go

echo "==> Go type generation complete."
echo "    pkg/api/generated/openapi_types.gen.go"
echo "    pkg/api/generated/asyncapi_types.gen.go"
echo "    pkg/api/generated/llm_error_messages.gen.go"
