package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestWU15Rooms_UserDefaultAllNamesPickerAndMuteFanout(t *testing.T) {
	cases := []struct {
		name, text           string
		mentions, want, mute []string
	}{
		{"default", "status?", nil, []string{"mia", "jim", "researcher2"}, nil},
		{"typed_all", "@all go", nil, []string{"mia", "jim", "researcher2"}, nil},
		{"one_name", "@Jim plan it", nil, []string{"jim"}, nil},
		{"two_names", "@Mia @Jim compare", nil, []string{"mia", "jim"}, nil},
		{"picker_authoritative", "@old name plan", []string{"jim"}, []string{"jim"}, nil},
		{"muted_default", "status?", nil, []string{"mia", "jim"}, []string{"researcher2"}},
		{"muted_all", "@all status?", nil, []string{"mia", "jim"}, []string{"researcher2"}},
		{"muted_explicit", "@Researcher2 anything?", nil, []string{"researcher2"}, []string{"researcher2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := wu15NewHarness(t)
			room := h.create("squad", "Launch", "mia", "jim", "researcher2")
			for _, id := range []string{"mia", "jim", "researcher2"} {
				h.stopMain(id)
			}
			if len(tc.mute) != 0 {
				status, body := h.http(http.MethodPatch, "/api/v1/rooms/"+room.Id+"/members", gen.RoomMembersUpdateRequest{Action: gen.RoomMembersUpdateRequestActionMute, AgentIds: tc.mute})
				require.Equal(t, http.StatusOK, status, "FR-158: mute: %s", body)
			}
			h.post(room.Id, "fanout", tc.text, tc.mentions...)
			entry := wu15FindEntry(t, h.snapshot(room.Id), "fanout")
			require.ElementsMatch(t, tc.want, entry.Addressees, "FR-164: exact target set")
			require.Equal(t, "user", entry.Author.Kind)
			require.Equal(t, tc.text, entry.Text, "K26: transcript is verbatim")
			for _, id := range tc.want {
				delivery := wu15Delivery(t, entry, id)
				capture, found, err := h.al.RequestLedger().Get(h.main(id), delivery.RequestId)
				require.NoError(t, err)
				require.True(t, found, "FR-200/216: actual receiver capture")
				require.Equal(t, delivery.RequestId, capture.RequestID)
				require.Equal(t, h.main(id), capture.ReceiverSessionID, "FR-200: own main, no room session")
				require.Equal(t, "room", string(capture.Source.Kind), "K34/FR-203: room-origin ledger")
				require.Contains(t, capture.Text, tc.text)
				require.True(t, capture.Held, "FR-252: stopped receiver keeps the request held")
				var requests []gen.Message
				for _, msg := range h.messages(id) {
					if msg.RoomRef != nil && msg.RoomRef.RoomId == room.Id {
						requests = append(requests, msg)
					}
				}
				require.Len(t, requests, 1, "FR-202: one actual collapsed-entry carrier")
				require.Equal(t, delivery.RequestId, requests[0].RoomRef.RequestId)
				require.Equal(t, gen.MessageRoomRefRoleRequest, requests[0].RoomRef.Role)
			}
			for _, id := range []string{"mia", "jim", "researcher2"} {
				if wu15HasID(tc.want, id) {
					continue
				}
				for _, msg := range h.messages(id) {
					if msg.RoomRef != nil {
						require.NotEqual(t, room.Id, msg.RoomRef.RoomId, "unaddressed member receives no room request")
					}
				}
			}
		})
	}
}

func wu15HasID(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func TestWU15Rooms_AgentRepliesOnlyMentionedMembersAndNeverSelf(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       []string
	}{
		{"plain", "done", []string{}},
		{"named", "@Mia draft the email", []string{"mia"}},
		{"all", "@all regroup", []string{"mia", "researcher2"}},
		{"unresolved", "@Nobody hello", []string{}},
		{"command_is_inert", "/pause", []string{}},
		{"membership_is_inert", "/members add Ray", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := wu15NewHarness(t)
			room := h.create("squad", "Launch", "mia", "jim", "researcher2")
			h.post(room.Id, "request", "@Jim answer")
			entry := wu15FindEntry(t, h.snapshot(room.Id), "request")
			delivery := wu15Delivery(t, entry, "jim")
			// Existing real reply entry point; no speculative room API names.
			_, err := h.al.NewAddressRouter().Reply(context.Background(), tools.ReplyRequest{
				ReplyTo: delivery.RequestId, Content: tc.text, ResponderSessionID: h.main("jim"),
				Author: tools.SendOrigin{WorkspaceID: wu15Workspace, AgentID: "jim"},
			})
			require.NoError(t, err, "FR-215: real room-origin reply branch")
			snap := h.snapshot(room.Id)
			var answers []gen.RoomEntry
			for _, answer := range snap.Entries {
				if answer.Author.Kind == "agent" && answer.Text == tc.text {
					answers = append(answers, answer)
				}
			}
			require.Len(t, answers, 1, "FR-215: exactly one room answer")
			require.NotNil(t, answers[0].Author.AgentId, "K26: agent answer names its author")
			require.Equal(t, "jim", *answers[0].Author.AgentId)
			require.ElementsMatch(t, tc.want, answers[0].Addressees, "FR-165: sender excluded from @all; plain/unknown names reach nobody")
			require.False(t, h.room(room.Id).Paused, "FR-168: agent /pause is text only")
			require.ElementsMatch(t, room.Members, h.room(room.Id).Members, "FR-168/206: agent text never edits membership")
		})
	}
}

func TestWU15Rooms_MentionNamespaceIsExactAndAmbiguityRefuses(t *testing.T) {
	h := wu15NewHarness(t)
	h.create("squad", "Control", "mia", "jim")
	h.shutdown()
	// Custom IDs avoid changing protected built-in identities. Names are
	// current production config, not an in-memory registry-only side door.
	h.cfg.Agents.List = append(h.cfg.Agents.List,
		config.AgentConfig{ID: "member-a", Name: "Mia", Type: config.AgentTypeCustom},
		config.AgentConfig{ID: "member-b", Name: "Mia", Type: config.AgentTypeCustom},
		config.AgentConfig{ID: "member-c", Name: "Jim Smith", Type: config.AgentTypeCustom})
	// Q-E: rename persists through agentstore; config-only agents are not
	// writable entities. Seed the same real entity API existing gateway tests
	// use, including the original roster so write-triggered reload preserves it.
	store := agentstore.New(h.home)
	for _, configured := range h.cfg.Agents.List {
		copyOfConfig := configured
		require.NoError(t, store.Create(configured.ID, &copyOfConfig), "SETUP: real agentstore creation for %s", configured.ID)
	}
	seedBindingWorkspace(t, h.home, wu15Workspace, "member-a", "member-b", "member-c")
	h = wu15Boot(t, h.home, h.cfg)
	room := h.create("squad", "Launch", "member-a", "member-b", "member-c")
	status, body := h.http(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", gen.RoomMessageRequest{ClientMessageId: "ambiguous", Text: "@Mia hello", Mentions: []string{}})
	wu15RequireError(t, status, body, http.StatusUnprocessableEntity, "Several members are called Mia — pick from the list")
	h.post(room.Id, "short", "@Jim go")
	require.Empty(t, wu15FindEntry(t, h.snapshot(room.Id), "short").Addressees, "BDD-17.31: short names never promoted to a match")
	h.post(room.Id, "full", "@jIM sMITH go")
	require.Equal(t, []string{"member-c"}, wu15FindEntry(t, h.snapshot(room.Id), "full").Addressees, "§6.13: exact full name is case-insensitive")
	h.renameAgent("member-c", "New Jim")
	h.post(room.Id, "stable", "@Jim Smith go", "member-c")
	require.Equal(t, []string{"member-c"}, wu15FindEntry(t, h.snapshot(room.Id), "stable").Addressees, "picker id survives real rename")
	h.renameAgent("member-a", "all")
	h.post(room.Id, "reserved", "@all go")
	require.ElementsMatch(t, room.Members, wu15FindEntry(t, h.snapshot(room.Id), "reserved").Addressees, "§6.13: all remains reserved")
	h.post(room.Id, "pick-all-name", "hello", "member-a")
	require.Equal(t, []string{"member-a"}, wu15FindEntry(t, h.snapshot(room.Id), "pick-all-name").Addressees)
}

func TestWU15Rooms_PickerRejectsNonmembersAndRetryUsesCurrentMembersOnly(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim", "researcher2")
	status, body := h.http(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", gen.RoomMessageRequest{ClientMessageId: "foreign", Text: "go", Mentions: []string{"ray"}})
	wu15RequireError(t, status, body, http.StatusUnprocessableEntity, "")
	h.post(room.Id, "retry", "status?")
	before := wu15FindEntry(t, h.snapshot(room.Id), "retry")
	status, body = h.http(http.MethodPatch, "/api/v1/rooms/"+room.Id+"/members", gen.RoomMembersUpdateRequest{Action: gen.RoomMembersUpdateRequestActionRemove, AgentIds: []string{"jim"}})
	require.Equal(t, http.StatusOK, status, "FR-252: membership removal: %s", body)
	h.post(room.Id, "retry", "status?")
	after := wu15FindEntry(t, h.snapshot(room.Id), "retry")
	require.Equal(t, before.Id, after.Id, "FR-216: caller id Retry records one entry")
	delivery := wu15Delivery(t, after, "jim")
	require.Equal(t, "cancelled", delivery.State)
	require.NotNil(t, delivery.Reason, "K27: cancelled removal reason")
	require.Equal(t, "removed", *delivery.Reason, "BDD-17.33: removed is not readmitted")
	for _, id := range []string{"mia", "researcher2"} {
		d := wu15Delivery(t, before, id)
		var requests []gen.Message
		for _, msg := range h.messages(id) {
			if msg.RoomRef != nil && msg.RoomRef.RequestId == d.RequestId {
				requests = append(requests, msg)
			}
		}
		require.Len(t, requests, 1, "Retry never appends a second request to remaining members")
	}
}

func TestWU15Rooms_MessageUTF8ByteLimits(t *testing.T) {
	for _, size := range []int{0, 1, 2, 65535, 65536, 65537} {
		t.Run(fmt.Sprintf("bytes_%d", size), func(t *testing.T) {
			h := wu15NewHarness(t)
			room := h.create("squad", "Launch", "mia", "jim")
			text := strings.Repeat("x", size)
			before := h.snapshot(room.Id)
			status, data := h.http(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", gen.RoomMessageRequest{ClientMessageId: "limit", Text: text, Mentions: []string{}})
			if size == 0 || size > 65536 {
				wu15RequireError(t, status, data, http.StatusUnprocessableEntity, "")
				wu15UnchangedSnapshot(t, before, h.snapshot(room.Id))
			} else {
				require.Equal(t, http.StatusAccepted, status, "FR-176: valid byte boundary: %s", data)
			}
		})
	}
	t.Run("unicode_is_bytes_not_runes", func(t *testing.T) {
		h := wu15NewHarness(t)
		room := h.create("squad", "Launch", "mia", "jim")
		text := strings.Repeat("界", 21845) + "x" // 21845 × 3 UTF-8 bytes + 1 = 65536.
		h.post(room.Id, "utf8-max", text)
		before := h.snapshot(room.Id)
		status, data := h.http(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", gen.RoomMessageRequest{ClientMessageId: "utf8-over", Text: text + "x", Mentions: []string{}})
		wu15RequireError(t, status, data, http.StatusUnprocessableEntity, "")
		wu15UnchangedSnapshot(t, before, h.snapshot(room.Id))
	})
}
