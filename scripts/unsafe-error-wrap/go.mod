// Nested module so `go run` of this scanner does not pull in the parent
// module's dependency graph. The scanner is stdlib-only; compiling it
// through github.com/elicify-ai/omnipus takes tens of seconds per
// invocation, which makes the proof-of-failure companion (9 runs) look
// hung. A nested module compiles in well under a second.
module github.com/elicify-ai/omnipus/scripts/unsafe-error-wrap

go 1.22.0
