// Omnipus — request builders shared by both tag sets.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// NO BUILD TAG. platform_refusal_test.go runs under `records_no_sqlite` and the
// rest of the suite does not, so anything both need lives here. Duplicating a
// builder into the untagged file is how the two copies quietly drift and one
// tag set starts testing a shape the other cannot express.

package knowledgefind

import "github.com/elicify-ai/omnipus/pkg/api/generated"

func req(mut ...func(*generated.VaultFindRequest)) generated.VaultFindRequest {
	r := generated.VaultFindRequest{}
	for _, m := range mut {
		m(&r)
	}
	return r
}

func withType(t string) func(*generated.VaultFindRequest) {
	return func(r *generated.VaultFindRequest) { r.Type = &t }
}

func withFilter(n generated.VaultFilterNode) func(*generated.VaultFindRequest) {
	return func(r *generated.VaultFindRequest) { r.Filter = &n }
}

// leaf builds one predicate. `IN` takes the whole list; every other operator
// takes at most one value, and the unary two take none.
func leaf(property, op string, value ...string) generated.VaultFilterNode {
	o := generated.VaultFilterNodeOp(op)
	n := generated.VaultFilterNode{Property: &property, Op: &o}
	switch {
	case op == "IN":
		vs := append([]string{}, value...)
		n.Values = &vs
	case len(value) == 1:
		n.Value = &value[0]
	}
	return n
}

func strPtr(s string) *string { return &s }
