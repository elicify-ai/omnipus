// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---- system.config.get ----

type ConfigGetTool struct{ deps *Deps }

func NewConfigGetTool(d *Deps) *ConfigGetTool   { return &ConfigGetTool{deps: d} }
func (t *ConfigGetTool) Name() string           { return "get_config" }
func (t *ConfigGetTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *ConfigGetTool) Description() string {
	return "Read a setting (port, timeout, host, feature flag, or similar) from config.json by dot-notation " +
		"key, e.g. 'gateway.port', 'sandbox.mode'. Pair with set_config to change a value. Credential-shaped " +
		"fields (API keys, secrets, tokens, passwords) and a handful of security-critical keys (gateway " +
		"account/token data, the whole sandbox namespace's writable form, etc.) are refused or returned " +
		"redacted — use list_providers to check configured providers without exposing keys." +
		"\nParameters: key (required, e.g. 'gateway.port')."
}

func (t *ConfigGetTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"key": map[string]any{"type": "string"}},
		"required":   []string{"key"},
	}
}

func (t *ConfigGetTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	key, _ := args["key"].(string)
	if key == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "key is required", ""))
	}
	// Block reading sensitive keys by name.
	if isSensitiveConfigName(key) {
		return tools.ErrorResult(errorJSON("FORBIDDEN",
			"Credentials cannot be read via config.get — they are write-only",
			"Use list_providers to see configured providers (without keys)",
		))
	}
	// Fetched once and reused for both the read-policy check and the read
	// itself, so a namespaced channel instance key ("channels.telegram.one.…")
	// resolves to the SAME instance for both — see configKeySegments' comment.
	cfg := t.deps.GetCfg()
	// Block reading the keys the policy table marks undisclosable. get_config
	// used to have no deny list at all: every key set_config refuses was
	// readable, so the tool handed out the account hashes (gateway.users), the
	// auth-bypass flag and the whole enforcement configuration — the map an
	// attacker wants BEFORE using any of the write-side findings.
	if err := validateConfigReadKey(cfg, key); err != nil {
		return tools.ErrorResult(errorJSON("FORBIDDEN", err.Error(),
			"Operators inspect these in Settings → Security or config.json"))
	}
	value, err := dotGet(cfg, key)
	if err != nil {
		return tools.ErrorResult(errorJSON("KEY_NOT_FOUND",
			fmt.Sprintf("Config key %q not found: %v", key, err),
			"Check the key name and try again",
		))
	}
	// A section read must not return what the direct read of its children
	// refuses; blocked and credential-bearing descendants come back redacted.
	return tools.NewToolResult(successJSON(map[string]any{
		"key":    key,
		"value":  redactConfigValue(key, value),
		"source": "config",
	}))
}

// ---- system.config.set ----

type ConfigSetTool struct{ deps *Deps }

func NewConfigSetTool(d *Deps) *ConfigSetTool   { return &ConfigSetTool{deps: d} }
func (t *ConfigSetTool) Name() string           { return "set_config" }
func (t *ConfigSetTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *ConfigSetTool) Description() string {
	return "Update a setting (port, timeout, host, feature flag, or similar) in config.json by dot-notation key, e.g. 'gateway.port', 'sandbox.tool_policies'. Only updates settings that already exist — it never creates a new one, and a key naming nothing that exists is refused rather than silently dropped. Writable sections are limited to: gateway., agents., sandbox., channels., tools., devices., providers, workspace_path — everything else is refused. Within those, several security-critical subtrees are refused too, even though their prefix is writable: the whole sandbox.* namespace, gateway.dev_mode_bypass/users/token/cli_token/public_url/trust_xff, tools.exec/mcp/browser/allow_read_paths/allow_write_paths, and the channel ownership fields channels.*.identity/workspace_id — each refusal names where the setting actually belongs. Credential/API-key-shaped values (api_key, secret, token, password in the name) are always refused here — use configure_provider or configure_channel instead, which route secrets into the encrypted credential store. The result includes a requires_restart field: when true, the change was saved but has no effect until the operator restarts the gateway — check it and relay that to the user rather than assuming the change is already live.\nParameters: key (required), value (required)."
}

func (t *ConfigSetTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key":   map[string]any{"type": "string"},
			"value": map[string]any{"description": "New config value (any JSON-compatible type)"},
		},
		"required": []string{"key", "value"},
	}
}

func (t *ConfigSetTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	key, _ := args["key"].(string)
	if key == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "key is required", ""))
	}
	value, hasValue := args["value"]
	if !hasValue {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "value is required", ""))
	}
	if isSensitiveConfigName(key) {
		return tools.ErrorResult(errorJSON("FORBIDDEN",
			"Use configure_provider to set API keys",
			"Credentials are stored encrypted in credentials.json, not config.json",
		))
	}
	requiresRestart := isRestartRequired(key)

	// Capture prevValue before mutation (under the same lock as the mutation),
	// and observe what the config actually holds afterwards. Everything happens
	// inside the one WithConfig closure so the "did it land" answer is read from
	// the same config the write mutated, under the same lock — and so a failed
	// check rides WithConfig's own rollback (deps.go: fn error restores the
	// pre-mutation snapshot and never calls SaveConfigLocked).
	//
	// validateConfigKey (the deny/allow-list gate) runs INSIDE this closure now,
	// not before it. It used to be a lock-free pre-check, which was fine while
	// it was purely static — but it now resolves a namespaced channel instance
	// key against the live cfg (configKeySegments), exactly like
	// validateConfigKeyLands does. Checking the deny list against one snapshot
	// and then writing against a later, different one would reopen a TOCTOU
	// window the lock exists to close: an instance created between the two
	// reads could flip the deny-list verdict. Both checks now read the SAME
	// locked cfg.
	var denyErr, schemaErr, setErr, landErr error
	var prevValue, storedValue any
	var normalised []string
	err := t.deps.WithConfig(func(cfg *config.Config) error {
		// Validate that the key refers to a known, non-security-critical
		// config path to prevent arbitrary injection and privilege escalation.
		if err := validateConfigKey(cfg, key); err != nil {
			denyErr = err
			return fmt.Errorf("INVALID_KEY: %w", err)
		}
		// Truth gate, before any mutation: does this key name a place a write
		// can actually land? See validateConfigKeyLands — without it, a key that
		// json.Unmarshal silently drops was reported as a successful write.
		if err := validateConfigKeyLands(cfg, key); err != nil {
			schemaErr = err
			return fmt.Errorf("INVALID_KEY: %w", err)
		}
		prevValue, _ = dotGet(cfg, key)
		if err := dotSet(cfg, key, value); err != nil {
			setErr = err
			return fmt.Errorf("SET_FAILED: %w", err)
		}
		// Truth gate, after the mutation. The schema walk above is a static
		// check against the config TYPE; this one reads the live config back and
		// is what catches a drop no type walk can predict — a custom
		// UnmarshalJSON that ignores a field, for instance.
		storedValue, normalised, landErr = observeConfigWrite(cfg, key, value)
		if landErr != nil {
			return fmt.Errorf("WRITE_NOT_APPLIED: %w", landErr)
		}
		return nil
	})
	if denyErr != nil {
		return tools.ErrorResult(errorJSON("INVALID_KEY", denyErr.Error(),
			"Use get_config to inspect available config keys"))
	}
	if schemaErr != nil {
		return tools.ErrorResult(errorJSON("INVALID_KEY", schemaErr.Error(),
			"Use get_config to inspect the settings that actually exist"))
	}
	if setErr != nil {
		return tools.ErrorResult(errorJSON("SET_FAILED", setErr.Error(),
			"Check that the key is a valid config path"))
	}
	if landErr != nil {
		return tools.ErrorResult(errorJSON("WRITE_NOT_APPLIED", landErr.Error(),
			"Nothing was changed. Re-read the key with get_config before acting on it"))
	}
	if err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	payload := map[string]any{
		"key":              key,
		"value":            value,
		"previous_value":   prevValue,
		"requires_restart": requiresRestart,
	}
	// The write landed, but not byte-for-byte: the config layer normalised it
	// (a bare string stored as a one-element list, an enum lower-cased, …).
	// Report the stored form rather than letting "value" stand as the whole
	// truth — same principle as the checks above, one step milder, because
	// normalisation is legitimate behaviour and must not fail the call.
	if len(normalised) > 0 {
		payload["stored_value"] = storedValue
		payload["note"] = fmt.Sprintf(
			"stored, but normalised by the config layer at %s — trust stored_value, not value",
			strings.Join(normalised, ", "))
	}
	return tools.NewToolResult(successJSON(payload))
}

// knownConfigPrefixes are the top-level config sections that can be set at runtime.
// Keys outside this set are rejected by system.config.set to avoid corrupting the config.
//
// This list is a POLICY statement — which sections an agent may touch — and not
// a claim that they exist. Three entries name nothing on config.Config today:
// "security." and "workspace_path" are reserved (see blockedConfigKeys, which
// refuses them), and "heartbeat." is simply dead — heartbeats are a per-agent
// concern (HEARTBEAT.md), not a config section. That used to matter, because a
// write under a section that does not exist was reported as a success and
// dropped; validateConfigKeyLands is now the authority on whether a key exists,
// so a dead prefix here costs a clear refusal rather than a silent lie.
var knownConfigPrefixes = []string{
	"gateway.", "agents.", "sandbox.", "security.", "channels.", "tools.",
	"heartbeat.", "devices.", "providers", "workspace_path",
}

// blockedConfigKey names one config key — or one subtree root — that generic
// config mutation must never write, together with the reason it is off limits.
//
// It is a table rather than a run of `if` statements so the policy is data:
// one place to read, one place to extend, and directly enumerable by a test
// (see TestValidateConfigKey_BlockedKeys, which asserts every entry here is
// refused and that a representative legitimate key in the same section is
// still accepted).
type blockedConfigKey struct {
	// Key is a config key or the root of a subtree. A candidate key matches
	// when it is AT, UNDER or ABOVE Key — see configKeyCovers and
	// configKeyIsAncestorOf — compared case-insensitively.
	//
	// A segment may be configWildcardSegment ("*"), which matches any single
	// segment. That is for the config sections that are MAPS keyed by an
	// operator-chosen id rather than structs with fixed field names —
	// channels.<instance>.* is the only one today. Without it a map-keyed
	// section could only be protected by blocking the whole section, which
	// would freeze every ordinary setting in it.
	Key string
	// Reason is surfaced verbatim in the rejection so the caller — usually an
	// LLM — is told what it hit and where the setting really lives, instead of
	// retrying variations of the same key.
	Reason string

	// ReadReason, when non-empty, additionally makes the key unreadable via
	// get_config, and is surfaced verbatim the way Reason is. It is a separate
	// sentence from Reason because the two answer different questions: Reason
	// says why WRITING widens the agent's cage; ReadReason says why the VALUE
	// must not be disclosed.
	//
	// Exactly one of ReadReason / ReadOKReason must be set on every entry —
	// pinned by TestBlockedConfigKeys_ReadPolicyIsDecidedForEveryEntry. A new
	// entry therefore cannot be added without someone deciding, in writing,
	// whether it is readable; the alternative (an empty field meaning
	// "readable") would make every future addition silently fail open on the
	// read side.
	ReadReason string
	// ReadOKReason, when non-empty, records why this key is deliberately
	// READABLE even though it is not writable, and is never shown to the
	// caller. It exists so the carve-out is justified in the table rather than
	// inferred from the absence of a ReadReason.
	ReadOKReason string
}

// blockedConfigKeys is the security-critical surface that set_config refuses.
//
// The threat this closes: set_config takes no path argument and therefore
// bypasses the filesystem chokepoint entirely, yet the keys below are the very
// controls that define an agent's boundary. An agent able to write them can
// widen its own cage — turn the sandbox off, mint itself an API credential,
// point exec/browser/MCP at an arbitrary binary, or switch off the redaction
// that keeps secrets out of its own context. That is exactly the exposure
// ADR-062's secret set exists to prevent for config.json on disk; this tool
// edits the same settings through the front door.
//
// This is a DENY list layered on top of the knownConfigPrefixes ALLOW list, not
// a replacement for it: a key must both start with a known section AND avoid
// every entry here. Entries are matched before the allow list.
//
// The same table governs get_config, minus the entries that carry a
// ReadOKReason. Reads and writes are two different threats over one overlapping
// set of keys — writing widens the cage, reading discloses — so the policy is
// ONE table with a per-entry read decision rather than two tables that drift.
// Defaulting reads to the write list is what makes a future addition
// fail-closed on both axes.
//
// Nothing here is a tool-policy fallback (CLAUDE.md hard constraint 6). Tool
// policy stays explicit, seeded data; this list only decides which config keys
// one particular tool may write. sandbox.tool_policies remains fully
// operator-editable — via Settings, the REST API, or config.json — it just is
// not editable by the agent whose policies it governs.
var blockedConfigKeys = []blockedConfigKey{
	// ADR-054 §11 checklist item 6: agents.list (the retired agent-roster
	// entity blob) must be rejected specifically — NOT by removing the
	// "agents." prefix from knownConfigPrefixes, which gates the whole agents.*
	// namespace and would also break agents.defaults.* (including
	// agents.defaults.default_agent_id, D6.4).
	{
		Key: "agents.list",
		Reason: "the agent roster is managed by the agent store, not generic config — " +
			"use create_agent/update_agent/delete_agent instead",
		ReadOKReason: "the roster is neither credential material nor an enforcement control — " +
			"it is write-blocked because the agent store owns it, and list_agents already " +
			"returns the same data",
	},

	// ---- the sandbox enforcement surface (entire subtree) ----
	//
	// Every field under sandbox.* is a security control: mode, god_mode,
	// god_mode_allowed, tool_policies, shell_deny_patterns, allowed_paths,
	// allowed_exec_paths, filesystem_model, egress_allow_list,
	// egress_allow_cidrs, allow_network_outbound, ssrf.*, audit_log,
	// skill_trust, prompt_injection_level, browser_evaluate_enabled. There is
	// no non-security key in the namespace worth carving an exception for, and
	// a subtree block is the only form that stays correct when a field is added.
	{
		Key: "sandbox",
		Reason: "the whole sandbox namespace is the enforcement boundary itself (mode, god_mode, " +
			"tool_policies, shell_deny_patterns, allowed_paths, allowed_exec_paths, filesystem_model, " +
			"egress and SSRF allow-lists, audit_log, skill_trust, prompt_injection_level) — " +
			"change it in Settings → Security or config.json, never from an agent",
		ReadOKReason: "the agent's own enforcement configuration. OPERATOR DECISION 2026-08-12: an agent granted the config tool may READ its settings — that is what the tool is for, and it is how an agent can explain WHY it cannot do something instead of just failing. Writing this namespace stays blocked: reading the cage is diagnosis, opening it is escape",
	},

	// ---- gateway authentication, origin and approval controls ----
	{
		Key:          "gateway.dev_mode_bypass",
		Reason:       "it disables gateway authentication entirely, granting unauthenticated admin access to the whole API",
		ReadOKReason: "an agent that can see the front door is unauthenticated can say so; it still cannot change it",
	},
	{
		Key:    "gateway.users",
		Reason: "it is the bearer-token account list — writing it mints an API credential",
		ReadReason: "it holds the bcrypt password and session-token hashes of every account — " +
			"offline-cracking material, not configuration",
	},
	{
		// Also caught by the credential substring filter above; listed so this
		// predicate is correct standalone if that filter is ever narrowed.
		Key:        "gateway.token",
		Reason:     "it is the gateway bearer token — credentials live in the encrypted store",
		ReadReason: "it is the gateway bearer token in plaintext",
	},
	{
		Key:        "gateway.cli_token",
		Reason:     "it is the CLI bearer token — credentials live in the encrypted store",
		ReadReason: "it is the CLI token entry — its hash and issuance metadata",
	},
	{
		Key: "gateway.public_url",
		Reason: "it defines the canonical origin that CSP, CORS and the WebSocket CheckOrigin " +
			"all validate against (ADR-044 D2)",
		ReadOKReason: "it is the origin a browser types — public by construction — and agents build " +
			"web_serve preview links from it (ADR-044)",
	},
	{
		Key: "gateway.trust_xff",
		Reason: "it makes the client IP attacker-controlled, which is what auth rate limiting " +
			"and audit attribution key on",
		ReadOKReason: "enforcement configuration, readable for the same reason as sandbox; the write stays blocked",
	},
	{
		Key:          "gateway.validate_inbound",
		Reason:       "it is the inbound contract validator (Constraint #8)",
		ReadOKReason: "enforcement configuration, readable; the write stays blocked",
	},
	{
		Key:          "gateway.auth_mismatch_log_level",
		Reason:       "it controls whether failed authentication attempts are logged at all",
		ReadOKReason: "a log level. Knowing what is recorded is not an advantage worth withholding from a tool the operator granted",
	},
	{
		Key:    "gateway.tool_approval_timeout",
		Reason: "it governs the human approval gate that every ask-policy tool call passes through",
		ReadOKReason: "the timeout is user-visible in the approval prompt itself — an agent may " +
			"legitimately say how long a pending approval lives, and knowing it widens nothing",
	},
	{
		Key:    "gateway.tool_approval_max_pending",
		Reason: "it governs the human approval gate that every ask-policy tool call passes through",
		ReadOKReason: "a queue depth is operational, not secret, and knowing it does not let an " +
			"agent approve anything",
	},

	// ---- tool-surface boundary controls ----
	{
		Key:          "tools.allow_read_paths",
		Reason:       "it is the filesystem read boundary for every file tool",
		ReadOKReason: "the read fence's location. Post-ADR-062 reads are open anyway, so this discloses nothing an agent cannot already determine by reading",
	},
	{
		Key:          "tools.allow_write_paths",
		Reason:       "it is the filesystem write boundary for every file tool",
		ReadOKReason: "the write fence's location — precisely what an agent needs in order to explain a refused write rather than retrying blindly",
	},
	{
		Key: "tools.filter_sensitive_data",
		Reason: "it is the filter that strips API keys, tokens and secrets out of tool results " +
			"before they reach the model",
		ReadOKReason: "whether results are scrubbed. Readable; the write stays blocked",
	},
	{
		Key: "tools.filter_min_length",
		Reason: "it tunes that same secret-redaction filter — a large value switches redaction off " +
			"without appearing to disable anything",
		ReadOKReason: "readable with the write blocked. The chunk-a-secret concern it was denied for is not addressed by hiding a threshold that can be measured in a few calls",
	},
	{
		Key: "tools.exec",
		Reason: "it holds the exec binary allow-list, the exec approval mode and the egress proxy " +
			"toggle for spawned processes",
		ReadOKReason: "which binaries may be run and whether approval is required — the answer to 'why was my command refused'",
	},
	{
		Key: "tools.mcp",
		Reason: "an MCP server entry names a program the gateway launches — writing it is arbitrary " +
			"code execution, and it would bypass whatever policy governs add_mcp_server",
		ReadOKReason: "the configured servers. Embedded credentials are caught by redactConfigValue's field-name redaction at any depth, so the list is readable while its secrets are not",
	},
	{
		Key: "tools.browser",
		Reason: "exec_path and cdp_url make the gateway launch or attach to a chosen binary, " +
			"profile_dir points the browser profile anywhere, and evaluate_enabled turns on " +
			"arbitrary in-page JavaScript",
		ReadOKReason: "browser paths and endpoints. Reads are open post-ADR-062, so the filesystem layout it discloses is already visible",
	},
	{
		Key:          "tools.cron.allow_command",
		Reason:       "it lets scheduled jobs run shell commands",
		ReadOKReason: "readable; the write stays blocked",
	},
	{
		Key:          "tools.web.private_host_whitelist",
		Reason:       "it is the SSRF exemption list for private/internal hosts (SEC-24)",
		ReadOKReason: "the SSRF exemption list. Readable so an agent can explain a refused fetch; the write stays blocked",
	},
	{
		Key:          "tools.web.proxy",
		Reason:       "it routes every outbound web request through a chosen proxy",
		ReadOKReason: "the egress path. Readable; the write stays blocked",
	},
	{
		Key: "tools.skills.marketplaces",
		Reason: "it is the skill supply chain — a marketplace entry decides where installable " +
			"instructions are fetched from",
		ReadOKReason: "install_skill REQUIRES a registry name as an argument, so an agent that cannot " +
			"see which registries exist cannot install anything; find_skills already surfaces the " +
			"same list, and the credential-ref redaction below covers auth_token_ref/token_ref",
	},

	// ---- the channel ownership record (ADR-065 FR-5) ----
	//
	// ADR-065 makes a channel instance owned by exactly one (workspace, agent)
	// pair and gates every outbound send on that pair. The pair is stored right
	// here in config.json: channels.<instance>.identity (kind + id) and
	// channels.<instance>.workspace_id. set_config is `allow` in the global
	// tool-policy ceiling and "channels." is a permitted section, so before
	// these two entries an agent with shipped-default permissions could rewrite
	// its own ownership record and then egress through any instance it liked,
	// using that instance's bot token. A constraint the constrained party can
	// edit is not a constraint.
	//
	// The keys are wildcarded at the instance segment because Config.Channels is
	// a MAP keyed by an operator-chosen instance id ("telegram", "slack.eu"), so
	// there is no finite set of literal keys to enumerate. The wildcard is the
	// narrowest shape that works: blocking "channels" outright would freeze
	// enabled, base_url, mention_only and every other ordinary per-instance
	// setting an agent may legitimately manage.
	//
	// A single-segment wildcard is enough for BOTH shapes, bare and namespaced,
	// and the reason is worth writing down because it is not obvious. A
	// NAMESPACED instance id contains a dot itself ("slack.eu"), so naively
	// splitting "channels.slack.eu.workspace_id" on "." gives four segments,
	// and this three-segment pattern would not match it position-by-position.
	// configKeySegments (config.go, used by validateConfigKeyLands, dotGet,
	// dotSet — and, critically, by configKeyCovers/configKeyIsAncestorOf
	// below) closes that gap upstream of matching, not by widening the table:
	// when "slack.eu" is a real entry in cfg.Channels, "slack" and "eu" are
	// coalesced back into the single token "slack.eu" — exactly the map key
	// createChannelInstance (pkg/gateway/rest.go) wrote — so the candidate
	// actually compared here is the three-segment
	// [channels, "slack.eu", workspace_id], and the wildcard at position 1
	// matches that whole token, dot included, the same way it already matches
	// a bare "telegram". One entry, one wildcard segment, both shapes.
	//
	// Before configKeySegments learned to coalesce, this table's single
	// wildcard genuinely did NOT cover the namespaced shape, and the key was
	// safe for an entirely different, accidental reason: dotSet split on
	// every ".", so it could only ever build channels→slack→eu→…, addressing
	// a field "eu" on a DIFFERENT instance called "slack" — one that
	// json.Unmarshal then silently dropped. TestConfigSet_NamespacedInstanceOwnershipIsUnreachable
	// pinned that accident as a property (not a verdict) for exactly this
	// reason: so the day dotSet learned to address dotted map keys, the test
	// would fail loudly instead of the hole opening silently. That day is
	// this change. The test is now TestConfigSet_NamespacedInstanceOwnershipRefused
	// (config_channel_ownership_test.go) and asserts the SAME property — the
	// ownership record does not move — delivered by this table's explicit
	// match instead of by a lucky accident of the old writer.
	//
	// The key USED to be answered with a success the write never earned — the
	// unreachable member was dropped by json.Unmarshal and set_config reported
	// {"key":…,"value":…} anyway. validateConfigKeyLands refusing it outright
	// was the first fix — same safety, delivered visibly instead of by
	// accident. It is no longer what delivers it for THIS pair of fields:
	// configKeySegments now resolves a real namespaced instance correctly, so
	// validateConfigKeyLands would LAND the write (structurally, "eu" really is
	// the "workspace_id" field of a real "slack.eu" instance). The refusal for
	// identity/workspace_id specifically comes from here — this table, via the
	// wildcard match described above — the same layer that has always refused
	// the bare shape. FR-5 does not depend on which layer delivers it; the
	// test above asserts the ownership record does not move, not the verdict.
	//
	// The block is on the whole identity OBJECT rather than identity.id alone.
	// identity.kind is equally load-bearing — flipping it to "user" makes the
	// instance route as the user instead of as its owning agent (route.go's
	// documented default for an absent kind) — and a subtree entry stays correct
	// when a field is added. Consequence, by design: a bare write at
	// "channels.<instance>" or at "channels" is refused too, because
	// json.Unmarshal MERGES an object written at an ancestor path into the live
	// struct and would reach these fields (configKeyIsAncestorOf).
	{
		Key: "channels.*.identity",
		Reason: "it is half the channel's ownership record — identity names the agent that owns this " +
			"channel instance, and ADR-065 gates outbound sends on workspace-bound instances against " +
			"it, so rewriting it would let an agent egress through a channel it does not own, under " +
			"that channel's own bot token — change the owner in the Channels screen or config.json, " +
			"never from an agent",
		ReadOKReason: "the ownership record is enforcement configuration, not credential material, and " +
			"the same OPERATOR DECISION 2026-08-12 that made the sandbox namespace readable applies: an " +
			"agent refused a send under ADR-065 must be able to say WHICH instance it owns instead of " +
			"failing opaquely. The disclosure conceded is small and largely unavoidable — the instance " +
			"KEYS under channels are map keys and stay visible whatever the leaf policy is, and " +
			"enforcement now sits at the send tool and at dispatch, so knowing an owner buys nothing " +
			"actionable. Writing it stays blocked: reading the cage is diagnosis, opening it is escape",
	},
	{
		Key: "channels.*.workspace_id",
		Reason: "it is the other half of the channel's ownership record — it binds the instance to one " +
			"workspace (ADR-029) and ADR-065 gates outbound sends on the (workspace, agent) pair, so " +
			"rewriting it would move a channel into the agent's own workspace and hand it the keys — " +
			"rebind the instance in the Channels screen or config.json, never from an agent",
		ReadOKReason: "readable for the same reason as identity above — an agent that cannot see which " +
			"workspace its channel is bound to cannot explain a refused send, and the binding is " +
			"enforcement configuration rather than a secret. The write stays blocked",
	},

	// ---- reserved: no such config section exists TODAY ----
	//
	// "security" and "workspace_path" are listed in knownConfigPrefixes but
	// there is no matching field on config.Config, so a write under them is
	// currently a silent no-op that reports success (dotSet creates the key in
	// the generic map; the unmarshal back into *config.Config drops it).
	// Blocking them is behaviour-neutral today and fail-closed tomorrow: if a
	// `security` section is ever introduced, or `workspace_path` — the anchor
	// the filesystem confinement is computed from — is reintroduced, it does
	// not silently become agent-writable the moment the field lands.
	{
		Key: "security",
		Reason: "reserved — no such config section exists today, and a security section must " +
			"never be agent-writable by default if one is introduced",
		ReadOKReason: "reserved and empty. Nothing to disclose; the write stays blocked",
	},
	{
		Key: "workspace_path",
		Reason: "reserved — it names the workspace root that filesystem confinement is " +
			"anchored to, and must never be agent-writable if reintroduced",
		ReadOKReason: "the confinement root, which an agent already knows — it is its own working directory",
	},
}

// channelInstanceConfigType is reflect.TypeOf(config.ChannelInstanceConfig{}),
// computed once. configKeySegments uses it to ask "could this dot-segment
// name a FIELD of a channel instance" without allocating a fresh zero value
// on every call.
var channelInstanceConfigType = reflect.TypeOf(config.ChannelInstanceConfig{})

// configKeySegments splits a dot-notation config key into its segments. It does
// NOT trim: " sandbox.mode" keeps its leading space and therefore matches
// nothing, which is the correct outcome — that key names no real field, so it
// must be refused by the allow list rather than quietly normalised into one
// that does.
//
// cfg resolves the one ambiguity a channels.* path can carry: a namespaced
// channel instance id contains a dot itself (ADR-029, e.g. "telegram.one"),
// so "channels.telegram.one.enabled" splits into FOUR raw segments even
// though "telegram.one" — the literal createChannelInstance map key,
// pkg/gateway/rest.go — is really ONE token. Grammar alone cannot always
// tell which: a slug ("one") and a real ChannelInstanceConfig field name
// ("identity") share the same character set, so
// "channels.telegram.identity.id" — an existing, security-critical address,
// see TestConfigSet_ChannelOwnershipRecordRefused — is grammatically
// indistinguishable from a namespaced instance "telegram.identity"
// addressing field "id". cfg breaks the tie with ground truth instead:
// segments[1] is kept as the WHOLE instance key when it (a) already names a
// real entry in cfg.Channels AND (b) segments[2] could plausibly be one of
// ITS fields (lookupJSONField) — i.e. the bare reading is at least
// plausible — and segments[1]+"."+segments[2] is coalesced into one token
// only when that fails. config.ParseInstanceKey supplies the type/slug
// boundary (the first dot) rather than this function re-deriving it by hand,
// and config.ValidateInstanceKey confirms the coalesced candidate is even
// grammatically a channel instance key (known type, valid slug) before it is
// trusted — the SAME known-channel-types list createChannelInstance itself
// validates new instances against.
//
// cfg may be nil. Every caller that does not have a live config in scope —
// the blockedConfigKeys deny/allow-list matching used to run before
// WithConfig even started — gets the same unresolved split it always got.
// That is safe, never unsafe: an unresolved candidate for a channels.* path
// either fails to land (validateConfigKeyLands refuses "would create a new
// entry") or, for the deny list, is matched without knowing the real
// instance boundary — which can only make the deny list MORE conservative,
// never less (the same asymmetric-safety direction as configSegmentMatches'
// folding, see its comment).
//
// All three places that ever need to agree on where a channels.* instance
// key ends — the deny/allow-list matching (configKeyCovers,
// configKeyIsAncestorOf), the schema walk (validateConfigKeyLands), and the
// actual read/write (dotGet, dotSet) — call this ONE function with the SAME
// cfg, so a key the deny list allows and a key validateConfigKeyLands says
// lands are guaranteed to be the SAME field dotSet then writes. Before this,
// dotGet/dotSet called strings.Split directly: fixing only this function
// would have let a namespaced write pass validation and then land on the
// WRONG target underneath dotSet's literal, uncoalesced walk — a stray field
// bolted onto an unrelated bare-typed sibling, or a phantom top-level entry
// — while still reporting success, because observeConfigWrite reads back
// through that same wrong, uncoalesced path.
func configKeySegments(cfg *config.Config, key string) []string {
	segs := strings.Split(key, ".")
	if cfg == nil || len(segs) < 3 || !strings.EqualFold(segs[0], "channels") {
		return segs
	}
	// No built-in channel type name contains a dot (config.ParseInstanceKey's
	// documented invariant), so segments[1] is always the channel type.
	chType := segs[1]
	if _, bareExists := cfg.Channels[chType]; bareExists {
		if _, isField := lookupJSONField(channelInstanceConfigType, segs[2]); isField {
			// The bare instance exists AND segments[2] could be one of its
			// fields — the bare reading is at least plausible, and it must
			// win: "identity" is both a real field name and a syntactically
			// valid slug, so "channels.telegram.identity.id" has to keep
			// addressing the bare instance's Identity.ID, never a same-typed
			// namespaced sibling that happens to be slugged "identity".
			return segs
		}
	}
	// Either no bare instance exists, or segments[2] cannot be one of its
	// fields (the bare reading is impossible) — try the namespaced reading.
	tail := strings.Join(segs[1:], ".")
	parsedType, _ := config.ParseInstanceKey(tail)
	namespaced := parsedType + "." + segs[2]
	if err := config.ValidateInstanceKey(namespaced); err != nil {
		return segs // not even a grammatically valid instance key — leave alone
	}
	if _, namespacedExists := cfg.Channels[namespaced]; !namespacedExists {
		// set_config only updates existing settings (see validateConfigKeyLands's
		// DECISION note below) — an unresolved candidate is left uncoalesced
		// and falls through to the ordinary "would create a new entry" refusal.
		return segs
	}
	coalesced := make([]string, 0, len(segs)-1)
	coalesced = append(coalesced, segs[0], namespaced)
	coalesced = append(coalesced, segs[3:]...)
	return coalesced
}

// configWildcardSegment is the blocked-key segment that matches any single
// candidate segment. See blockedConfigKey.Key and configSegmentMatches.
const configWildcardSegment = "*"

// configSegmentMatches reports whether one key segment names the same field as
// one blocked-key segment.
//
// The comparison is case-INSENSITIVE, and that is the whole point of this
// function. The writer under this policy — dotSet, which round-trips through
// json.Unmarshal — matches struct fields case-insensitively, so a deny list
// that compares bytes is enforcing a different key space than the one that gets
// written. "gateway.Dev_Mode_Bypass" missed a byte-exact deny entry, passed the
// allow list, and landed on Gateway.DevModeBypass: unauthenticated admin access
// to the whole API, no restart needed. A comparison must match the semantics of
// the consumer it protects, not the spelling of the table it reads from.
//
// The folding is deliberately BROADER than the writer's (strings.EqualFold does
// full Unicode simple folding; encoding/json folds ASCII). That asymmetry is
// safe in this direction and only in this direction: over-matching here can
// only DENY a key that would otherwise have been allowed. The allow list below
// stays byte-exact for the mirror-image reason — under-matching there can only
// REFUSE a key, never admit one. This is the same asymmetric-folding rule the
// filesystem carve-out on this branch already follows.
//
// A trailing array index binds to the segment it indexes, so "sandbox[0]"
// matches the blocked key "sandbox".
//
// configWildcardSegment on the BLOCKED side matches any candidate segment. The
// asymmetry is deliberate and is the same fail-closed direction as the folding
// above: the wildcard is only ever honoured in blockedSeg, never in seg, so a
// caller cannot write the literal key "channels.*.enabled" and have its "*"
// match a blocked segment it does not name. Widening what is DENIED is safe;
// widening what a candidate can match is not.
func configSegmentMatches(seg, blockedSeg string) bool {
	if blockedSeg == configWildcardSegment {
		return true
	}
	if strings.EqualFold(seg, blockedSeg) {
		return true
	}
	if len(seg) > len(blockedSeg) && seg[len(blockedSeg)] == '[' &&
		strings.EqualFold(seg[:len(blockedSeg)], blockedSeg) {
		return true
	}
	return false
}

// configKeyCovers reports whether key is AT or UNDER blocked — the key itself,
// a child, a grandchild, or an index into it.
//
// cfg is threaded straight through to configKeySegments for both key and
// blocked, so a namespaced channel instance id in EITHER string is coalesced
// the same way before comparison. blocked is always a literal pattern out of
// blockedConfigKeys (e.g. "channels.*.identity") — its "*" segments never
// name a real channel type, so configKeySegments is always a no-op on it; the
// cfg argument only ever does real work on the candidate key.
func configKeyCovers(cfg *config.Config, key, blocked string) bool {
	ks, bs := configKeySegments(cfg, key), configKeySegments(cfg, blocked)
	if len(ks) < len(bs) {
		return false
	}
	for i, b := range bs {
		if !configSegmentMatches(ks[i], b) {
			return false
		}
	}
	return true
}

// configKeyIsAncestorOf reports whether key sits strictly ABOVE blocked — i.e.
// writing key rewrites the blocked subtree wholesale.
//
// This is the granularity the deny loop used to miss entirely, and it made most
// of the table decorative. dotSet writes a whole object at a path, and
// json.Unmarshal MERGES that object into the live struct, so one call with
// a bare section name and an object value reaches every blocked leaf in that
// section at once. key="gateway" with {"dev_mode_bypass": true} is the worst
// case and needs no second step: that flag is read per request, so gateway
// authentication is off for the entire API immediately, with no restart. The
// allow list even made this the documented shape — it explicitly accepts a bare
// section name (key == "gateway").
//
// The fix is structural rather than three more root entries in the table —
// adding roots by hand is how the list became inconsistent (sandbox and
// security had them; gateway, tools and agents did not). "At, under, or above"
// is a property of the key space, so a leaf entry added later cannot
// reintroduce the hole.
func configKeyIsAncestorOf(cfg *config.Config, key, blocked string) bool {
	ks, bs := configKeySegments(cfg, key), configKeySegments(cfg, blocked)
	if len(ks) >= len(bs) {
		return false
	}
	for i, k := range ks {
		if !configSegmentMatches(k, bs[i]) {
			return false
		}
	}
	return true
}

// validateConfigKey returns an error if key falls at, under or above a
// security-critical or otherwise blocked key (blockedConfigKeys), or if it does
// not start with a known config prefix (knownConfigPrefixes). Deny is evaluated
// first, so an entry in blockedConfigKeys always wins over its enclosing
// allowed section.
//
// cfg resolves a namespaced channel instance key exactly the way
// validateConfigKeyLands and dotSet do (configKeySegments) — see that
// function's comment. This is why the caller (ConfigSetTool.Execute) now
// calls validateConfigKey from INSIDE the same WithConfig closure that
// validateConfigKeyLands runs in, on the same locked cfg, rather than before
// it: checking the deny list against one snapshot and then writing against a
// later, different one would reopen the exact TOCTOU the lock exists to
// close — a namespaced instance created between the two reads could flip the
// deny-list verdict.
func validateConfigKey(cfg *config.Config, key string) error {
	for _, blocked := range blockedConfigKeys {
		if configKeyCovers(cfg, key, blocked.Key) {
			return fmt.Errorf("config key %q is not writable via this tool: %s", key, blocked.Reason)
		}
		if configKeyIsAncestorOf(cfg, key, blocked.Key) {
			return fmt.Errorf(
				"config key %q is not writable via this tool: writing it replaces %q, which is blocked because %s — set a specific key below %q instead",
				key, blocked.Key, blocked.Reason, key)
		}
	}
	for _, prefix := range knownConfigPrefixes {
		if strings.HasPrefix(key, prefix) || key == strings.TrimSuffix(prefix, ".") {
			return nil
		}
	}
	return fmt.Errorf("unknown config key %q — only known sections may be set via this tool", key)
}

// validateConfigReadKey returns an error if key is AT or UNDER a key the table
// marks unreadable (ReadReason). An ANCESTOR read is not refused here — it is
// served with the blocked subtrees redacted (redactConfigValue), because
// refusing it outright would make whole sections unreadable over one leaf.
//
// cfg is threaded through to configKeyCovers for the same reason as
// validateConfigKey above; ConfigGetTool.Execute fetches cfg once and reuses
// it for both this check and the read itself, so the two never disagree
// about which channel instance a namespaced key resolves to.
func validateConfigReadKey(cfg *config.Config, key string) error {
	for _, blocked := range blockedConfigKeys {
		if blocked.ReadReason == "" {
			continue
		}
		if configKeyCovers(cfg, key, blocked.Key) {
			return fmt.Errorf("config key %q is not readable via this tool: %s", key, blocked.ReadReason)
		}
	}
	return nil
}

// configReadIsBlocked reports whether a fully-qualified config path is one
// get_config must never disclose.
//
// It calls configKeyCovers with a nil cfg — deliberately, not an oversight.
// This function walks a VALUE TREE that redactConfigValue is already
// recursing over (built from data already read, not from a caller-supplied
// key string), and it runs with no live *config.Config in scope at all. That
// is safe today because no blockedConfigKeys entry under "channels." carries
// a ReadReason — channels.*.identity and channels.*.workspace_id are
// deliberately readable (ReadOKReason) — so configReadIsBlocked can never
// even reach a case where the channels instance-key ambiguity matters. If a
// channels.* entry ever gains a ReadReason, this comment is the trip-wire to
// come back and thread cfg through redactConfigValue too.
func configReadIsBlocked(path string) bool {
	for _, blocked := range blockedConfigKeys {
		if blocked.ReadReason != "" && configKeyCovers(nil, path, blocked.Key) {
			return true
		}
	}
	return false
}

// redactedConfigValue is what a blocked or credential-bearing value is replaced
// with in a get_config result. It matches the audit redaction marker (SEC-16)
// so one string means one thing across the product.
const redactedConfigValue = "[REDACTED]"

// sensitiveConfigNameFragments are the field-name fragments that mark a value
// as credential material wherever it appears. They are the same four fragments
// get_config already refuses on a directly-requested key; applying them at every
// depth is what stops the ancestor read (get_config "providers", "channels")
// from returning what the direct read refuses.
var sensitiveConfigNameFragments = []string{"api_key", "secret", "token", "password"}

func isSensitiveConfigName(name string) bool {
	lower := strings.ToLower(name)
	for _, frag := range sensitiveConfigNameFragments {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	return false
}

// redactConfigValue returns value with every unreadable descendant replaced by
// redactedConfigValue. path is the fully-qualified config path value was read
// from ("" for the config root).
//
// Without this, the deny list only ever protected an exact read: get_config
// "gateway" returned the users array — bcrypt password and session-token hashes
// — along with the bearer token and dev_mode_bypass, none of which the direct
// reads of those same keys will hand over. Redacting rather than refusing keeps
// section reads useful: get_config "gateway" still reports the port and the
// host, it just cannot be used as a credential dump.
func redactConfigValue(path string, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for name, sub := range typed {
			childPath := name
			if path != "" {
				childPath = path + "." + name
			}
			if isSensitiveConfigName(name) || configReadIsBlocked(childPath) {
				out[name] = redactedConfigValue
				continue
			}
			out[name] = redactConfigValue(childPath, sub)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, sub := range typed {
			// Array elements share their parent's config path: the policy table
			// addresses fields, not indices.
			out[i] = redactConfigValue(path, sub)
		}
		return out
	default:
		return value
	}
}

// isRestartRequired returns true for config keys that require a restart.
//
// This list must stay consistent with the gateway's RestartGatedKeys
// (pkg/gateway/rest_pending_restart.go), which is the authoritative set the
// REST layer honors. It is duplicated here (rather than imported) because
// pkg/gateway depends on this package, so importing RestartGatedKeys back would
// be a cycle. gateway.public_url is restart-gated (ADR-044 D2 / FR-007): it
// feeds the boot-frozen CanonicalGatewayOrigin that serve_web's "no desync"
// guarantee and CSP/CORS/WS CheckOrigin all depend on, so an agent that sets it
// must be told requires_restart:true — omitting it let serve_web/CSP silently
// desync until a manual restart.
func isRestartRequired(key string) bool {
	for _, prefix := range []string{"gateway.port", "gateway.host", "gateway.bind", "gateway.public_url", "sandbox.", "security."} {
		if strings.HasPrefix(key, prefix) || key == strings.TrimSuffix(prefix, ".") {
			return true
		}
	}
	return false
}

// ---- truth gates: a reported write must be a write that happened ----
//
// set_config used to answer {"key":…, "value":…} for every key that cleared the
// deny list and the allow list, whether or not the value reached config.Config.
// It cannot reach it in two ordinary cases, and both were silent:
//
//  1. The key names nothing. dotSet round-trips through JSON, and
//     json.Unmarshal DROPS an object member that matches no struct field. So
//     "heartbeat.enabled" — a section listed in knownConfigPrefixes that has no
//     field on config.Config at all — reported success and changed nothing. So
//     did the namespaced-channel case ("channels.slack.eu.workspace_id": dotSet
//     splits on ".", so "eu" lands as a field of a ChannelInstanceConfig and is
//     dropped). ADR-065 FR-5 leaned on that drop for its safety.
//  2. The key names a NEW entry in a map-keyed section, and so creates config
//     rather than updating it — "channels.foo.type" conjures a channel instance
//     that no operator ever configured.
//
// Neither is a security hole on its own; both are the same defect, which is
// that the tool's answer was not evidence of anything. An agent told "value:
// true" acts on it, and the operator then reads a config that disagrees with
// what the agent reported. The two gates below make the tool's success mean
// what a reader assumes it means: validateConfigKeyLands refuses before the
// write, observeConfigWrite verifies after it.
//
// This is deliberately NOT a value round-trip comparison of the whole config.
// json.Unmarshal MERGES an object into the live struct, `omitempty` erases a
// zero value on the way back out, and Config.MarshalJSON re-emits
// Config.UnknownFields (keys with no struct field, preserved verbatim for
// round-trip safety, FR-004) — so a whole-document strict decode or a naive
// equality check would reject perfectly legitimate writes. Each gate is scoped
// to the one key being written and tolerates all three behaviours.

// jsonAddressableField is one field of a struct type together with the name a
// config-key segment must use to reach it.
type jsonAddressableField struct {
	Name  string
	Field reflect.StructField
}

// jsonAddressableFields returns the fields of struct type t that a JSON member
// name can address, in the order encoding/json would consider them.
//
// It mirrors encoding/json's promotion rule rather than reflect.VisibleFields'
// looser one: the fields of an anonymous (embedded) struct are promoted ONLY
// when the embedded field carries no json name, because an embedded field WITH
// a name is an ordinary nested object. pkg/config depends on this — ToolWebConfig,
// ToolCronConfig, ToolExecConfig and ToolSkillsConfig all embed ToolConfig
// untagged, so "tools.web.enabled" must reach the promoted ToolConfig.Enabled.
//
// Shallower wins on a name collision and a collision at the SAME depth removes
// the name entirely, both matching encoding/json. The second rule is the
// fail-closed direction: an ambiguous name becomes unaddressable, so the write
// is refused rather than landing on whichever field reflection happened to
// enumerate first.
func jsonAddressableFields(t reflect.Type) []jsonAddressableField {
	var out []jsonAddressableField
	taken := map[string]bool{}
	type queued struct {
		typ   reflect.Type
		index []int
	}
	queue := []queued{{typ: t}}
	for len(queue) > 0 {
		var next []queued
		var level []jsonAddressableField
		count := map[string]int{}
		for _, q := range queue {
			for i := range q.typ.NumField() {
				f := q.typ.Field(i)
				if !f.IsExported() {
					continue
				}
				tag := f.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, _, _ := strings.Cut(tag, ",")
				index := append(append([]int{}, q.index...), i)
				if f.Anonymous && name == "" && derefType(f.Type).Kind() == reflect.Struct {
					next = append(next, queued{typ: derefType(f.Type), index: index})
					continue
				}
				if name == "" {
					name = f.Name
				}
				promoted := f
				promoted.Index = index
				level = append(level, jsonAddressableField{Name: name, Field: promoted})
				count[name]++
			}
		}
		for _, f := range level {
			if count[f.Name] == 1 && !taken[f.Name] {
				taken[f.Name] = true
				out = append(out, f)
			}
		}
		queue = next
	}
	return out
}

// lookupJSONField finds the field a config-key segment addresses on struct type
// t: an exact name match first, then a case-insensitive one, which is the
// precedence encoding/json itself applies when decoding.
func lookupJSONField(t reflect.Type, seg string) (reflect.StructField, bool) {
	fields := jsonAddressableFields(t)
	for _, f := range fields {
		if f.Name == seg {
			return f.Field, true
		}
	}
	for _, f := range fields {
		if strings.EqualFold(f.Name, seg) {
			return f.Field, true
		}
	}
	return reflect.StructField{}, false
}

// derefType strips pointer indirection from a type.
func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// derefValue strips pointer indirection from a type/value pair. A nil pointer
// still has a type and json.Unmarshal allocates one on the way down, so the
// walk continues on the type alone with no live value left to inspect.
func derefValue(t reflect.Type, v reflect.Value) (reflect.Type, reflect.Value) {
	for t.Kind() == reflect.Pointer {
		if v.IsValid() && !v.IsNil() {
			v = v.Elem()
		} else {
			v = reflect.Value{}
		}
		t = t.Elem()
	}
	return t, v
}

// fieldValue reads the (possibly promoted) field at index out of v, giving up
// and returning an invalid Value the moment it meets a nil embedded pointer.
// Losing the live value is harmless: it only costs the map-entry check below
// its evidence, and a map under a nil pointer has no entries anyway.
func fieldValue(v reflect.Value, index []int) reflect.Value {
	for _, i := range index {
		if !v.IsValid() {
			return reflect.Value{}
		}
		_, v = derefValue(v.Type(), v)
		if !v.IsValid() {
			return reflect.Value{}
		}
		v = v.Field(i)
	}
	return v
}

// validateConfigKeyLands returns an error unless key addresses a setting that
// exists in cfg and that a write can land on. It is the gate that turns the two
// silent no-ops described above into refusals.
//
// It answers three questions, walking cfg one key segment at a time:
//
//   - Does this segment name a field of the struct it sits in? If not, the
//     write would be dropped by json.Unmarshal. Refused.
//   - Does this segment name an EXISTING entry of a map-keyed section? If not,
//     the write would CREATE config rather than update it. Refused — see the
//     DECISION note below.
//   - Is there anything left to address? A segment below a string, an int or a
//     slice addresses nothing (dotSet cannot index a slice either). Refused.
//
// An `any`-typed field ends the walk successfully: everything below it is
// schema-free and lands verbatim.
//
// DECISION — set_config updates settings, it does not create them.
// Refusing a new map entry is the stricter reading, and it is the right one
// here. A map-keyed section is keyed by an operator-chosen id, so a new key is
// never a typo the tool can catch and never a setting the operator declared: it
// is a new object conjured mid-write, half-populated by construction, and
// indistinguishable in config.json from one the operator meant. The only such
// section an agent can reach today is channels.<instance>, where creation is
// also useless — the credential fields are refused by isSensitiveConfigName and
// the ownership record by blockedConfigKeys (ADR-065 FR-5) — so every instance
// an agent could conjure is a phantom that can never send anything. Creating
// one belongs to configure_channel and the Channels screen, which route secrets
// into the encrypted store; this tool points there instead.
//
// The cost is bounded and visible: an agent that wants to change a setting on a
// channel instance that does not exist yet now gets a refusal naming the
// creation path, instead of a success that leaves a phantom behind. Updating an
// instance that DOES exist is untouched — TestConfigSet_ChannelOwnershipBlockDoesNotOverBlock
// is the positive control.
func validateConfigKeyLands(cfg *config.Config, key string) error {
	t, v := reflect.TypeOf(cfg), reflect.ValueOf(cfg)
	segs := configKeySegments(cfg, key)
	for i, seg := range segs {
		t, v = derefValue(t, v)
		sofar := strings.Join(segs[:i+1], ".")
		parent := strings.Join(segs[:i], ".")
		if parent == "" {
			parent = "the config root"
		} else {
			parent = fmt.Sprintf("%q", parent)
		}
		switch t.Kind() {
		case reflect.Interface:
			// A free-form field: everything below it is stored as written.
			return nil
		case reflect.Struct:
			f, ok := lookupJSONField(t, seg)
			if !ok {
				return fmt.Errorf(
					"config key %q names no setting: %s has no field %q, so the write would be "+
						"silently dropped rather than applied", key, parent, seg)
			}
			v = fieldValue(v, f.Index)
			t = f.Type
		case reflect.Map:
			if t.Key().Kind() != reflect.String {
				return fmt.Errorf("config key %q cannot be written: %s is not addressable by name", key, parent)
			}
			var entry reflect.Value
			if v.IsValid() && !v.IsNil() {
				entry = v.MapIndex(reflect.ValueOf(seg).Convert(t.Key()))
			}
			if !entry.IsValid() {
				// The hint is section-specific because a wrong pointer is worse
				// than none: an agent told to use configure_channel for something
				// that is not a channel will just fail twice. channels is the only
				// map section reachable here today — every other map on
				// config.Config (session.identity_links, hooks.builtins,
				// hooks.processes, tools.mcp.servers, mailboxes, channel_policies,
				// sandbox.tool_policies) is refused earlier by knownConfigPrefixes
				// or blockedConfigKeys — so the generic branch is unreachable now
				// and correct if that ever changes.
				where := "; it must be created by whatever owns that section"
				if strings.EqualFold(segs[0], "channels") {
					where = "; create a channel instance with configure_channel or in the " +
						"Channels screen, which also stores its credentials encrypted"
				}
				return fmt.Errorf(
					"config key %q would CREATE a new %s entry (%q), not update an existing setting — "+
						"set_config only updates settings that already exist%s", key, parent, sofar, where)
			}
			v, t = entry, t.Elem()
		default:
			return fmt.Errorf(
				"config key %q cannot be written: %s is a %s value, so %q addresses nothing inside it",
				key, parent, t.Kind(), seg)
		}
	}
	return nil
}

// observeConfigWrite reads key back out of cfg after dotSet wrote value there
// and reports what actually happened.
//
// It returns the stored value, a non-nil error when the write did NOT land, and
// the leaf paths that landed in a different FORM than they were written in.
//
// The distinction between those last two is the whole design, and it is what
// keeps this check from breaking legitimate writes:
//
//   - MISSING — the path, or a non-zero leaf of the object written to it, is
//     absent afterwards. Nothing plausible explains that except a drop, so it
//     is an error and WithConfig rolls the whole write back.
//   - DIFFERENT — the leaf is there but does not equal what was sent. That is
//     ordinary, legitimate normalisation: config.FlexibleStringSlice stores the
//     bare string "alice" as ["alice"], and several config enums lower-case
//     themselves on the way in. Failing the call here would break real writes,
//     so it is reported instead, via stored_value.
//
// An absent leaf whose requested value is the JSON zero is NOT treated as
// missing: `omitempty` erases a zero on the way back out, so absence and zero
// are the same observable state — and validateConfigKeyLands has already proved
// the field exists, which is the fact that absence would otherwise cast doubt on.
func observeConfigWrite(cfg *config.Config, key string, value any) (stored any, normalised []string, missingErr error) {
	want, ok := normaliseJSONValue(value)
	if !ok {
		// value came in as JSON and must re-marshal; if it somehow cannot, say
		// nothing rather than invent a verdict.
		return nil, nil, nil
	}
	got, err := dotGet(cfg, key)
	if err != nil {
		if isJSONZero(want) {
			return want, nil, nil
		}
		return nil, nil, fmt.Errorf(
			"the write to %q did not take effect: the config holds no value there afterwards "+
				"(%v). The config was left unchanged", key, err)
	}
	missing, differing := configWriteDrift(key, got, want)
	if len(missing) > 0 {
		return got, nil, fmt.Errorf(
			"the write to %q did not take effect: %s %s absent from the config afterwards. "+
				"The config was left unchanged",
			key, strings.Join(missing, ", "), plural(len(missing), "is", "are"))
	}
	return got, differing, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// configWriteDrift compares what the config holds at a path (got) with what was
// written there (want), descending object members so that a partial write of an
// object is judged member by member.
//
// Object members present in got but absent from want are ignored ON PURPOSE:
// json.Unmarshal MERGES an object into the live struct, so writing
// {"model_name":"x"} at "agents.defaults" legitimately leaves every sibling
// setting standing. Judging the merge as drift would reject that write.
func configWriteDrift(path string, got, want any) (missing, differing []string) {
	wantMap, wantIsMap := want.(map[string]any)
	if !wantIsMap {
		if !reflect.DeepEqual(got, want) {
			differing = append(differing, path)
		}
		return nil, differing
	}
	gotMap, gotIsMap := got.(map[string]any)
	if !gotIsMap {
		// An object was written and something that is not an object came back.
		// Reported as a difference rather than a drop: a config type may
		// legitimately accept an object and marshal back as a scalar (several
		// in pkg/config have exactly that UnmarshalJSON/MarshalJSON pair).
		return nil, []string{path}
	}
	names := make([]string, 0, len(wantMap))
	for name := range wantMap {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic messages
	for _, name := range names {
		child := path + "." + name
		gotChild, present := gotMap[name]
		if !present {
			if isJSONZero(wantMap[name]) {
				continue
			}
			missing = append(missing, child)
			continue
		}
		m, d := configWriteDrift(child, gotChild, wantMap[name])
		missing, differing = append(missing, m...), append(differing, d...)
	}
	return missing, differing
}

// normaliseJSONValue puts a value into the same shape dotGet returns — float64
// numbers, map[string]any objects — so the two can be compared without every
// int/float64 pair reading as a difference.
func normaliseJSONValue(v any) (any, bool) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, false
	}
	return out, true
}

// isJSONZero reports whether v is the JSON zero for its kind — the values
// `omitempty` erases on the way out.
func isJSONZero(v any) bool {
	switch typed := v.(type) {
	case nil:
		return true
	case bool:
		return !typed
	case float64:
		return typed == 0
	case string:
		return typed == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

// dotGet reads a dot-notation key from cfg by marshaling to a generic map.
//
// The path is resolved via configKeySegments(cfg, key) rather than a bare
// strings.Split, so a namespaced channel instance key ("channels.telegram.one.enabled")
// walks the SAME map key ("telegram.one") that dotSet writes and
// validateConfigKeyLands validates — see configKeySegments' comment for why
// all three must agree.
func dotGet(cfg *config.Config, key string) (any, error) {
	m, err := configToMap(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	return walkDot(m, configKeySegments(cfg, key))
}

// dotSet writes a dot-notation key into cfg by round-tripping through JSON.
// This is a safe, reflection-free approach for dynamic config writes.
//
// See dotGet's comment: the path is resolved via configKeySegments(cfg, key)
// so a namespaced channel instance key lands on the real map entry instead of
// being split apart and written into an unrelated bare-typed sibling (or a
// brand-new phantom one).
func dotSet(cfg *config.Config, key string, value any) error {
	m, err := configToMap(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if setErr := setDot(m, configKeySegments(cfg, key), value); setErr != nil {
		return setErr
	}
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("re-marshal config: %w", err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("unmarshal into config: %w", err)
	}
	return nil
}

// configToMap marshals cfg to a generic map[string]any via JSON round-trip.
func configToMap(cfg *config.Config) (map[string]any, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// walkDot traverses a nested map by path segments.
func walkDot(m map[string]any, path []string) (any, error) {
	if len(path) == 0 {
		return m, nil
	}
	v, ok := m[path[0]]
	if !ok {
		return nil, fmt.Errorf("key %q not found", path[0])
	}
	if len(path) == 1 {
		return v, nil
	}
	sub, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("key %q is not an object", path[0])
	}
	return walkDot(sub, path[1:])
}

// setDot sets a value at the given dot-path in the nested map.
func setDot(m map[string]any, path []string, value any) error {
	if len(path) == 0 {
		return fmt.Errorf("empty path")
	}
	if len(path) == 1 {
		m[path[0]] = value
		return nil
	}
	sub, ok := m[path[0]]
	if !ok {
		// Create intermediate map.
		sub = map[string]any{}
		m[path[0]] = sub
	}
	subMap, ok := sub.(map[string]any)
	if !ok {
		return fmt.Errorf("key %q is not an object, cannot set nested key", path[0])
	}
	return setDot(subMap, path[1:], value)
}
