# pkg/config — the operator's config.json world

Load/save/defaults/validation for config.json, the two-layer tool-policy
ceiling, env keys, container detection. Config NEVER holds secrets — only
`*Ref` names resolved by pkg/credentials (ADR-004).

## Running tests here

`CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run
'^TestReconcileToolPolicyCeiling_PreservesOperatorSetValue$'
./pkg/config/`

## Tool policy — two layers, and the code is in validate.go

Layer 1: the global ceiling `sandbox.go::OmnipusSandboxConfig.ToolPolicies`
(`map[string]string`). Layer 2: sparse per-agent overrides
`config.go::AgentConfig.Tools` → `.Builtin.Policies`
(`map[string]ToolPolicy`). The type asymmetry is deliberate — the legacy
migration is generic over `~string` because of it; do not "fix" it.

`validate.go::ReconcileToolPolicyCeiling` — NOT a tool_policy*.go file,
the only tool_policy* files are tests — runs at gateway boot and every
reload (`gateway_sandbox.go::repairAndValidateToolPolicyCoverage`),
keeping the ceiling COMPLETE for the static catalog: it ADDS the shipped
default for any catalog tool with no entry (self-healing an old install
forward), never overwrites an existing entry, and never re-adds a retired
key (retired names are absent from knownTools by definition).
There is no legacy tool-policy key remapping (session-core DEL-21): a retired
saved key is inert, never rewritten, merged or converted.
`validate.go::ValidateToolPolicyCoverage` is a never-firing correctness
tripwire after Reconcile (OR-based: a global ceiling entry covers every
agent). It cannot 400 a sparse per-agent map — UAT 2026-09-02, a PUT
omitting `stop_plan` returned 200. The live REST 400 is
`ValidateSubmittedToolPolicyMap` (complete literal keys, no
static-catalog wildcards). Do not delete that check because Coverage
"already gates writes".

## The removed fail-closed backfill stays removed

`RepairIncompleteToolPolicyCoverage` and
`ValidateAgentOwnToolPolicyCoverage` were deleted by operator decision
(ADR-077) — the reconciled ceiling IS the default; an agent riding the
ceiling is the normal state. Guard:
`scripts/check-no-fail-closed-backfill.sh`. A merge from a pre-removal
branch resurrects them as an ordinary addition — resolve by keeping the
deletion. No `DefaultPolicy`/`GlobalDefaultPolicy` field exists anywhere
in pkg/config.

## defaults.go — a ceiling, not a grant

`defaults.go::defaultToolPolicyCeiling` assembles eight per-family maps
(including `"bash": "allow"` in defaultToolPoliciesGeneral). The values
resolve strictest-wins at runtime (see pkg/tools' compositor), so a
global "allow" can never loosen an agent's own policy. The map mirrors
`pkg/coreagent/seed.go`'s `allStaticToolNames` literal-for-literal — a
second hardcoded literal that config cannot import (cycle); drift is
caught loudly at boot by the coverage validator.

## Load is lenient; strictness is opt-in per site

The config-load path has no global DisallowUnknownFields — other unknown
keys keep their existing lenient handling (pinned by
TestLoadConfig_LegacySandboxProfileFields_Ignored). Strictness is local:
`pdf_limits.go` rejects unknown PDF-limit fields; a mixed legacy/nested
mailbox entry is also a hard error. `config_unmarshal.go` otherwise handles
flexible TYPE coercion (FlexibleStringSlice accepts string/number/array,
splitting on ASCII and full-width `，` commas).
`cli_token_migration.go` still runs on every load (one-shot,
raw-JSON-map patch — not SaveConfig, which drops explicit zero values
under omitempty).

## Env, container detection, and the scrubber

`envkeys.go` declares only five runtime knobs (HOME, CONFIG,
BUILTIN_SKILLS, BINARY, GATEWAY_HOST); most env binding is via caarlos0/env
tags, and `env_prefix_guard_test.go` guards the double-prefix bug that
once shipped ~20 dead env vars. `container_detect.go::RunningInContainer`
deliberately never consults memory limits, treats bare `0::/` as NOT
containerized, and honours the `OMNIPUS_CONTAINERIZED` override.

`security.go::RegisterSensitiveValues` is replace-not-append: every call
must pass the complete current set, or rotated secrets stop being
scrubbed. It lives on `config.Config`, not in pkg/credentials.

## PDF limits — engine configuration, not catalogue data (#1968)

`config.go::Config.PDFLimits` is a sparse optional overlay. Nil uses the one
embedded `data/pdf_limits.yaml` table, including its researched override rows
and `[ASSUMED]` comments; never duplicate these quantities in Go defaults.
`pdf_limits.go::Config.ResolvePDFLimits` is the only resolver. Per field it
uses exact model > matching prefixes (longest first) > provider > default;
operator fields beat shipped fields at the same key. Explicit zero is
unlimited, false selects text, absence inherits. Returned `PDFLimits` is an
immutable value; resolution does not modify the config or shipped data.

`PDFLimitsConfig.UnmarshalJSON` strictly decodes only this section and calls
`Validate`: unknown fields, negatives, empty providers, duplicate keys and
bad model patterns name their row/field in a hard error. Other sections
remain lenient. Pointers retain zero/false/absence through SaveConfig and
Clone. Do not materialize shipped rows into the operator overlay on load.

No tombstone: neutralize a shipped row at the same key with explicit desired
values (0 for unlimited, true for inline), or delete the operator row to
restore shipped behavior. A provider override never replaces a more-specific
model row. User reference: [PDF limits](../../docs/operations/configuration.md#pdf-delivery-limits-pdf_limits).
