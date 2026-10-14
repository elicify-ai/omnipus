package gateway

import "github.com/elicify-ai/omnipus/pkg/config"

// submittedBuiltinToolPolicyNames is the replacement-map universe: every
// implemented static tool in the same central catalogue GET /tools publishes,
// plus declared future reservations only when the caller actually supplies them.
// The broader seed/ceiling inventory stays unchanged. Requiring its future names
// would reject a complete map authored from GET /tools during staged delivery.
func submittedBuiltinToolPolicyNames[V ~string](submitted map[string]V) map[string]struct{} {
	registry, _ := buildCentralBuiltinRegistry(nil)
	known := make(map[string]struct{})
	for _, tool := range registry.All() {
		known[tool.Name()] = struct{}{}
	}
	declared := buildKnownBuiltinToolNames()
	for name := range submitted {
		if _, recognized := declared[name]; recognized {
			known[name] = struct{}{}
		}
	}
	return known
}

// validateSubmittedBuiltinToolPolicyMap leaves key legitimacy and the missing
// implemented-tool refusals with the existing validator. Unknown/wildcard names
// are never added to the universe; MCP keys retain their documented exception.
func validateSubmittedBuiltinToolPolicyMap[V ~string](submitted map[string]V) config.SubmittedToolPolicyDefects {
	return config.ValidateSubmittedToolPolicyMap(submitted, submittedBuiltinToolPolicyNames(submitted))
}

// retainReservedBuiltinToolPolicies preserves only an existing stored entry for
// a declared, not-yet-implemented tool that the request could not learn from GET
// /tools and omitted. Call inside the store mutation, using its current record.
// This never seeds/backfills an absent entry. Supplied reservations follow normal
// replacement/override intent, including an explicit removal of a local override.
func retainReservedBuiltinToolPolicies[V ~string](replacement, current map[string]config.ToolPolicy, submitted map[string]V) {
	implemented := submittedBuiltinToolPolicyNames(map[string]string(nil))
	declared := buildKnownBuiltinToolNames()
	for name, policy := range current {
		if _, supplied := submitted[name]; supplied {
			continue
		}
		if _, live := implemented[name]; live {
			continue
		}
		if _, reserved := declared[name]; reserved {
			replacement[name] = policy
		}
	}
}
