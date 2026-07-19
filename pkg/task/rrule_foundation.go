package task

// This file pins github.com/teambition/rrule-go (pure Go, MIT, no CGo —
// Constraint #2) as a real module dependency ahead of the RRULE validation
// and occurrence-expansion logic (Calendar Recurrence Redesign spec, Wire &
// Engine Design §Validation / §Occurrence expansion endpoint). Wave 0
// (Foundation) intentionally ships no expansion/validation logic — only the
// wire contract, the TriggerConfig struct fields, and this dependency pin —
// so `go mod tidy` does not prune the module before the downstream PR2 slice
// (validation, pkg/agent/task_trigger.go scheduler re-arm, the occurrences
// endpoint) lands and imports it directly. Delete this blank import once that
// slice's real usage supersedes it.
import (
	_ "github.com/teambition/rrule-go"
)
