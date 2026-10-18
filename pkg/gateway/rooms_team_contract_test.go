package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/rooms"
)

type wholeTeamDeps struct {
	mu     sync.Mutex
	ws     rooms.WorkspaceInfo
	frames []any
}

func (d *wholeTeamDeps) LookupWorkspace(string) (rooms.WorkspaceInfo, bool, error) {
	return d.ws, true, nil
}
func (d *wholeTeamDeps) AgentName(id string) (string, bool)   { return id, true }
func (*wholeTeamDeps) SessionInWorkspace(string, string) bool { return false }
func (*wholeTeamDeps) Limits() (int, int)                     { return 60, 65536 }
func (*wholeTeamDeps) Admit(_ context.Context, a rooms.Admission) (rooms.AdmitResult, error) {
	return rooms.AdmitResult{ReceiverSessionID: "main-" + a.Receiver.AgentID, Held: true}, nil
}
func (*wholeTeamDeps) Facts(addressing.Pair, string) (rooms.ReceiverFacts, error) {
	return rooms.ReceiverFacts{}, nil
}
func (*wholeTeamDeps) Discard(string, string) error { return nil }
func (*wholeTeamDeps) Mirror(rooms.Mirror) error    { return nil }
func (*wholeTeamDeps) CancelApproval(string)        {}
func (*wholeTeamDeps) Audit(rooms.AuditEvent)       {}
func (d *wholeTeamDeps) Publish(_ string, frame any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.frames = append(d.frames, frame)
}

func requireRoomContract(t *testing.T, name string, value any) {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(newYAMLSchemaLoader(t))
	path := filepath.Join(filepath.Dir(gatewayTestCallerFile(t)), "..", "..", "contracts", "components", "schemas", name+".yaml")
	schema, err := compiler.Compile("file://" + path)
	require.NoError(t, err)
	data, err := json.Marshal(value)
	require.NoError(t, err)
	var instance any
	require.NoError(t, json.Unmarshal(data, &instance))
	require.NoError(t, schema.Validate(instance), "real %s output with the entire workspace team", name)
}

// Chief's accepted team-cap proposal: exercise real service outputs, not
// only synthetic validator fixtures. Receivers are held to avoid 257 paid
// turns; room fanout, journals, metadata and snapshot production stay real.
func TestRoomsFix2_WholeTeamFramesValidateAbove256(t *testing.T) {
	d := &wholeTeamDeps{ws: rooms.WorkspaceInfo{ID: "sales", Name: "Sales"}}
	ids := make([]string, 257)
	for i := range ids {
		ids[i] = fmt.Sprintf("agent-%03d", i)
		d.ws.Agents = append(d.ws.Agents, rooms.Agent{ID: ids[i], Name: ids[i]})
	}
	svc := rooms.New(rooms.NewFileJournal(t.TempDir()), rooms.DepsFrom(d))
	t.Cleanup(svc.Close)
	list, err := svc.List("sales")
	require.NoError(t, err)
	require.Len(t, list, 1)
	team := list[0]
	require.Equal(t, ids, team.Members)
	requireRoomContract(t, "Room", team)
	get, err := svc.Get(team.Id)
	require.NoError(t, err)
	require.Equal(t, team, get)
	// No explicit mentions: the default broadcast emits 257 addressees and
	// deliveries even though any ONE mentions request stays capped at 256.
	require.NoError(t, svc.PostMessage(team.Id, gen.RoomMessageRequest{ClientMessageId: "all", Text: "status", Mentions: []string{}}, "user"))
	svc.WaitIdleForTest()
	require.NoError(t, svc.Attach(team.Id, 0, func(frame gen.RoomSnapshotFrame) {
		require.Equal(t, ids, frame.Members)
		require.Len(t, frame.MemberStates, 257)
		require.Len(t, frame.Entries, 1)
		require.Equal(t, ids, frame.Entries[0].Addressees)
		require.Len(t, frame.Entries[0].Deliveries, 257)
		requireRoomContract(t, "RoomSnapshotFrame", frame)
	}))
	for _, batch := range [][]string{ids[:256], ids[256:]} {
		_, err = svc.UpdateMembers(team.Id, gen.RoomMembersUpdateRequest{Action: "mute", AgentIds: batch}, "user")
		require.NoError(t, err)
	}
	get, err = svc.Get(team.Id)
	require.NoError(t, err)
	require.Equal(t, ids, get.Muted)
	requireRoomContract(t, "Room", get)
	d.mu.Lock()
	frames := append([]any(nil), d.frames...)
	d.mu.Unlock()
	meta, entry := 0, 0
	for _, frame := range frames {
		switch value := frame.(type) {
		case gen.RoomMetaFrame:
			requireRoomContract(t, "RoomMetaFrame", value)
			meta++
		case gen.RoomEntryFrame:
			requireRoomContract(t, "RoomEntryFrame", value)
			entry++
		}
	}
	require.Positive(t, meta, "instrument: metadata frames were produced")
	require.Equal(t, 1, entry, "instrument: one default fanout entry")
	_, err = svc.Create("sales", gen.RoomCreateRequest{Kind: "adhoc", AgentIds: ids}, "user")
	require.Error(t, err)
	require.Equal(t, 422, rooms.StatusOf(err), "squad/ad-hoc creation limit unchanged")
	_, err = svc.UpdateMembers(team.Id, gen.RoomMembersUpdateRequest{Action: "unmute", AgentIds: ids}, "user")
	require.Error(t, err)
	require.Equal(t, 422, rooms.StatusOf(err), "individual mutation-request limit unchanged")
}
