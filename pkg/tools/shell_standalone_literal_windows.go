//go:build windows

package tools

// rootIsWritable is spec §3.1 condition 5 on Windows: the root always counts
// as writable, so the stand-alone quoted-literal rule is off and every
// outside reference keeps today's refusal.
func rootIsWritable() bool { return true }
