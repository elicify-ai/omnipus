//go:build windows

package tools

// rootWritability is spec §3.1 condition 5 on Windows: the root always counts
// as writable, so the stand-alone quoted-literal rule is off and every
// outside reference keeps today's refusal.
func rootWritability() (bool, error) { return true, nil }
