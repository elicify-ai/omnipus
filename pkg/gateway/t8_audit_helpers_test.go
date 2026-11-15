package gateway

// Shared by the platform-independent Settings audit tests as well as the
// POSIX-only route fixtures. This data traversal has no platform dependency.
func t8AuditScalars(v any) []string {
	var out []string
	switch x := v.(type) {
	case string:
		out = append(out, x)
	case map[string]any:
		for _, child := range x {
			out = append(out, t8AuditScalars(child)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, t8AuditScalars(child)...)
		}
	}
	return out
}
