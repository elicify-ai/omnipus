package cron

import (
	"path/filepath"
	"reflect"
	"testing"
)

// I-2 / S-111 (ruling-check-delta "S-111 writers"): AddJobFull stores the
// creator's provenance; UpdateJob is a whole-record replace that never
// replaces or shrinks it: an empty job.Provenance leaves it unchanged and a
// non-empty one is joined; the join survives a reload.
func TestCronProvenance_AddStoresAndUpdateJoinsNeverShrinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	cs := NewCronService(path)
	every := int64(60_000)
	created, err := cs.AddJobFull(JobSpec{
		Name: "p", Schedule: CronSchedule{Kind: "every", EveryMS: &every}, Message: "m", AgentID: "dev",
		Provenance: []string{"web"},
	})
	if err != nil {
		t.Fatalf("AddJobFull: %v", err)
	}
	got, ok := cs.GetJob(created.ID)
	if !ok || !reflect.DeepEqual(got.Provenance, []string{"web"}) {
		t.Fatalf("created provenance = %v (found %t), want [web]", got.Provenance, ok)
	}

	// A person's edit (empty provenance) leaves the stored provenance unchanged.
	edit := got
	edit.Name = "renamed"
	edit.Provenance = nil
	if err := cs.UpdateJob(&edit); err != nil {
		t.Fatalf("UpdateJob (no provenance): %v", err)
	}
	got, _ = cs.GetJob(created.ID)
	if got.Name != "renamed" {
		t.Fatalf("instrument: the update did not apply, name = %q", got.Name)
	}
	if !reflect.DeepEqual(got.Provenance, []string{"web"}) {
		t.Fatalf("UpdateJob with empty provenance changed it to %v, want unchanged [web]", got.Provenance)
	}

	// An agent change joins its turn's origins; it never replaces them.
	agentEdit := got
	agentEdit.Payload.Message = "changed by an agent"
	agentEdit.Provenance = []string{"connector:telegram"}
	if err := cs.UpdateJob(&agentEdit); err != nil {
		t.Fatalf("UpdateJob (join): %v", err)
	}
	want := []string{"connector:telegram", "web"}
	got, _ = cs.GetJob(created.ID)
	if !reflect.DeepEqual(got.Provenance, want) {
		t.Fatalf("UpdateJob stored %v, want the join %v", got.Provenance, want)
	}
	reloaded, ok := NewCronService(path).GetJob(created.ID)
	if !ok || !reflect.DeepEqual(reloaded.Provenance, want) {
		t.Fatalf("persisted provenance = %v (found %t), want %v", reloaded.Provenance, ok, want)
	}
}
