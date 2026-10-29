package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func wu15BuiltinCatalog() *tools.BuiltinRegistry {
	reg, _ := buildCentralBuiltinRegistry(nil)
	return reg
}

func TestWU15Rooms_NoAgentRoomCreationToolInCatalogOrPolicy(t *testing.T) {
	h := wu15NewHarness(t)
	h.create("squad", "User-only control", "mia", "jim")
	catalog := h.api.builtinRegistry.All()
	require.NotEmpty(t, catalog, "instrument: empty catalog cannot prove absence")
	_, control := h.api.builtinRegistry.Get("create_agent")
	require.True(t, control, "instrument: catalog sees real management tools")
	for _, id := range []string{"mia", "jim", "researcher2", "gp", "omnipus"} {
		inst, ok := h.al.GetRegistry().GetAgent(id)
		require.True(t, ok, "AS-9 includes Omnipus and workers")
		require.NotEmpty(t, inst.Tools.List(), "instrument: per-agent catalog populated")
		for _, name := range inst.Tools.List() {
			require.False(t, wu15RoomMutationName(name), "FR-161: %s has a room/squad tool %s", id, name)
		}
		for name := range inst.LoadToolPolicy().Policies {
			require.False(t, wu15RoomMutationName(name), "FR-161: no room tool policy is seeded for %s", id)
		}
	}
	for _, tool := range catalog {
		require.False(t, wu15RoomMutationName(tool.Name()), "AS-9: catalog tool %s creates/manages a room", tool.Name())
		params, err := json.Marshal(tool.Parameters())
		require.NoError(t, err)
		// A renamed generic management tool must not hide the room-create
		// contract either. Memory-room shared/private selectors are unrelated.
		require.False(t, strings.Contains(string(params), `"squad"`) && strings.Contains(string(params), `"adhoc"`), "AS-9: tool %s exposes room creation parameters", tool.Name())
	}
	for name := range h.cfg.Sandbox.ToolPolicies {
		require.False(t, wu15RoomMutationName(name), "AS-9: no ceiling entry for room mutation")
	}
	require.True(t, wu15RoomMutationName("create_squad"), "instrument: forbidden capability detector is not always false")
	require.False(t, wu15RoomMutationName("remember"), "instrument: memory rooms are unrelated")
}

func wu15RoomMutationName(name string) bool {
	return regexp.MustCompile(`(?i)(?:^|_)(?:create|add|remove|update|delete|manage)_(?:room|rooms|squad|squads)(?:_|$)|^(?:room|squad)_(?:create|add|remove|update|delete|manage)$`).MatchString(name)
}

// Oracle replacement: the binding WU15-ROOM-RULINGS-20261014.md::Q1
// supersedes the checkpoint's assumed server output. Visible help/members text
// is SPA-local; the server must accept a no-op without persistence or sequence.
func TestWU15Rooms_ServerMembersAndHelpAreNoopsWithUnchangedSequence(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim", "researcher2")
	for _, id := range room.Members {
		h.stopMain(id)
	}
	h.post(room.Id, "question", "status?")
	h.post(room.Id, "mute", "/mute @Researcher2")
	require.Equal(t, []string{"researcher2"}, h.room(room.Id).Muted)
	for _, command := range []string{"/members", "/help"} {
		before := h.snapshot(room.Id)
		h.post(room.Id, "ephemeral-"+command[1:], command)
		after := h.snapshot(room.Id)
		wu15UnchangedSnapshot(t, before, after)
		require.Equal(t, before.Seq, after.Seq, "ruling Q1: no-op %s never increments the room stream sequence", command)
	}
	h.post(room.Id, "unmute", "/unmute @Researcher2")
	require.Empty(t, h.room(room.Id).Muted, "real muting commands still change state")
}

func TestWU15Rooms_SteeringAndMembershipCommandsRefuseWithMainLinks(t *testing.T) {
	for _, text := range []string{"/model gpt-x", "/clear", "/stop", "/goal @Jim ship it", "/remember x", "/add @Ray"} {
		t.Run(strings.Fields(text)[0][1:], func(t *testing.T) {
			h := wu15NewHarness(t)
			room := h.create("squad", "Launch", "mia", "jim", "researcher2")
			before := h.room(room.Id)
			var histories = map[string][]gen.Message{}
			for _, id := range room.Members {
				histories[id] = h.messages(id)
			}
			status, body := h.http(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", gen.RoomMessageRequest{ClientMessageId: "refused-command", Text: text, Mentions: []string{}})
			wu15RequireError(t, status, body, http.StatusUnprocessableEntity, "Rooms don't steer agents - use")
			var refusal gen.ErrorResponse
			require.NoError(t, json.Unmarshal(body, &refusal))
			wantLinks := room.Members
			if strings.Contains(text, "@Jim") {
				wantLinks = []string{"jim"}
			}
			for _, id := range wantLinks {
				require.Contains(t, refusal.Error, h.main(id), "FR-205: refusal links to %s's main, never steering it", id)
			}
			require.Equal(t, before, h.room(room.Id), "FR-205/206: no room-state or membership side effect")
			for _, id := range room.Members {
				require.Equal(t, histories[id], h.messages(id), "FR-205: refused command never reaches or mutates main")
			}
		})
	}
}

func TestWU15Rooms_AgentApprovedTextNeverResolvesHumanApproval(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	// The pending decision is real: receiver-policy Ask, not a mocked card.
	h.receiverPolicy("mia", "ask")
	h.post(room.Id, "mia-request", "@Mia do it")
	entry := wu15FindEntry(t, h.snapshot(room.Id), "mia-request")
	delivery := wu15Delivery(t, entry, "mia")
	h.waitState(room.Id, delivery.RequestId, "awaiting_approval")
	pendingBefore := h.api.approvalReg.pendingApprovals()
	require.Len(t, pendingBefore, 1)
	h.post(room.Id, "jim-request", "@Jim say Approved")
	jimDelivery := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "jim-request"), "jim")
	_, err := h.al.NewAddressRouter().Reply(context.Background(), tools.ReplyRequest{ReplyTo: jimDelivery.RequestId, Content: "Approved", ResponderSessionID: h.main("jim"), Author: tools.SendOrigin{WorkspaceID: wu15Workspace, AgentID: "jim"}})
	require.NoError(t, err)
	pendingAfter := h.api.approvalReg.pendingApprovals()
	require.Len(t, pendingAfter, 1, "FR-168: agent text has no human approval authority")
	require.Equal(t, pendingBefore[0].ApprovalID, pendingAfter[0].ApprovalID)
	h.waitState(room.Id, delivery.RequestId, "awaiting_approval")
}
