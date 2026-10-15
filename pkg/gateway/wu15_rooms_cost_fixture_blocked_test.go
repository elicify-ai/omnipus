package gateway

import "testing"

// Chief Q4 A: exact numeric pricing is deferred to a later QA RED round after
// the developer names a public deterministic pricing boundary. The live tests
// still verify allocation, deduplication and unknown-price handling through real
// turns. These failures establish a missing testability seam, not sensitivity.
func TestWU15Rooms_ExactThreeInputThirtyMilliDollarFixture(t *testing.T) {
	t.Fatal("BLOCKED: public deterministic turn-pricing boundary for one A request, one B request and one web input at $0.03 not implemented — required by BDD-17.30 / §6.14; each room must allocate 10000 micro-USD")
}

func TestWU15Rooms_ExactPricingAndMicroDollarRoundingFixture(t *testing.T) {
	t.Fatal("BLOCKED: public deterministic pricing boundary for exact micro-dollar and cent-rounding fixtures not implemented — required by §6.14 / FR-171; no private rate-table oracle or guessed rounding rule")
}
