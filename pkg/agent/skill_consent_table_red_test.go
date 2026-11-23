//go:build goolm && stdjson

package agent

// CHECK F22: TestSkillConsent_Table moved to
// /workspace/wt/skills-tools-1519f/pkg/gateway/skill_consent_table_red_test.go.
// The former adapter accepted pre-decided PermissionOpen/TrustOpen flags and
// returned StoredValue/Audit strings. It was not the native consent path.
// The replacement has literal raw D10 inputs and drives the real turn, approval
// registry, offered-actions snapshot, HTTP answers and stored effects. Do not
// restore a decision-returning test seam here. Tests' GREEN/mutation and
// proof-of-failability remain deferred to a separate CHECK instance.
