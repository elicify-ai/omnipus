// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/plan"
)

// I-2 / S-111 (ruling-check-delta "S-111 writers"): every field update_plan
// changes is an instruction, so the executing turn's provenance is joined
// into the plan in the same write. An unresolved turn makes the plan
// unresolved; it is never skipped.
func TestUpdatePlan_JoinsTheWritingTurnsProvenance(t *testing.T) {
	rows := []struct {
		name   string
		args   map[string]any
		writer *bus.Provenance
		want   []string
	}{
		{"objective from a Telegram turn", map[string]any{"objective": "new objective"}, ptrProvenance(bus.NewProvenance(bus.ConnectorOrigin("telegram"))), []string{"connector:telegram", "web"}},
		{"title from a Telegram turn", map[string]any{"title": "new title"}, ptrProvenance(bus.NewProvenance(bus.ConnectorOrigin("telegram"))), []string{"connector:telegram", "web"}},
		{"description from a web turn keeps web", map[string]any{"description": "new description"}, ptrProvenance(bus.NewProvenance(bus.OriginWeb)), []string{"web"}},
		{"objective from an unresolved turn", map[string]any{"objective": "new objective"}, ptrProvenance(bus.UnresolvedProvenance()), nil},
		{"objective from a context with no turn origin", map[string]any{"objective": "new objective"}, nil, nil},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			store := plan.New(filepath.Join(t.TempDir(), "plans"))
			p := &plan.Plan{Title: "S-111 plan", WorkspaceID: "ws-1", OwnerAgentID: "dev", Provenance: []string{"web"}}
			if err := store.Create(p); err != nil {
				t.Fatalf("Create: %v", err)
			}
			ctx := WithAgentID(context.Background(), "dev")
			if row.writer != nil {
				origin := NewTurnOrigin()
				origin.AddProvenance(*row.writer)
				ctx = WithTurnOrigin(ctx, origin)
			}
			args := map[string]any{"plan_id": p.ID}
			for k, v := range row.args {
				args[k] = v
			}
			res := NewPlanUpdateTool(store).Execute(ctx, args)
			if res.IsError {
				t.Fatalf("update_plan failed: %s", res.ForLLM)
			}
			got, err := store.Get(p.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if !reflect.DeepEqual(got.Provenance, row.want) {
				t.Fatalf("plan provenance after update_plan = %v, want %v", got.Provenance, row.want)
			}
		})
	}
}

func ptrProvenance(p bus.Provenance) *bus.Provenance { return &p }
