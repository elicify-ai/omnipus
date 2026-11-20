//go:build !linux

// packages_t5_openwatch_other_test.go: non-Linux stub of the FR-013 open
// watch. There is no inotify here, so the no-open half is not asserted on
// these platforms; the secret-absence assertions in
// exportOmitsStructuredSecrets still run and the Linux CI leg owns the
// no-open proof.
package gateway

import "testing"

type openWatch struct{}

func newOpenWatch(*testing.T, []string) *openWatch { return &openWatch{} }

func (*openWatch) requireNoOpens(*testing.T, string)   {}
func (*openWatch) requireProbeSeen(*testing.T, string) {}
