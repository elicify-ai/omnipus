// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package taskwire

import (
	"encoding/json"
	"fmt"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
)

// Goal converts a goal record to the generated wire type. pkg/goal's Goal
// mirrors contracts/components/schemas/Goal.yaml field for field and tag for
// tag (see that type's doc comment), so the conversion is the record's own
// JSON decoded into the generated type: every property keeps the name and
// value the record carries, typed by the generated Goal. A record that does
// not decode is an error, never a partial object.
func Goal(g *goal.Goal) (gen.Goal, error) {
	if g == nil {
		return gen.Goal{}, fmt.Errorf("goal wire: no goal record")
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return gen.Goal{}, fmt.Errorf("goal wire: encode goal %q: %w", g.GoalID, err)
	}
	var out gen.Goal
	if err := json.Unmarshal(raw, &out); err != nil {
		return gen.Goal{}, fmt.Errorf("goal wire: decode goal %q into the generated type: %w", g.GoalID, err)
	}
	return out, nil
}
