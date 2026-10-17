package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestWU15Rooms_MembershipArrayUpperBounds(t *testing.T) {
	// K24 caps agent_ids at 256. Construction of the large native roster is
	// after the public route control: the RED base never does a large boot.
	for _, size := range []int{255, 256, 257} {
		t.Run(fmt.Sprintf("agents_%d", size), func(t *testing.T) {
			h := wu15NewHarness(t)
			h.create("squad", "Control", "mia", "jim")
			h.shutdown()
			ids := make([]string, min(size, 256))
			for index := range ids {
				ids[index] = fmt.Sprintf("member-%d", index)
				h.cfg.Agents.List = append(h.cfg.Agents.List, config.AgentConfig{ID: ids[index], Name: ids[index], Type: config.AgentTypeCustom})
			}
			seedBindingWorkspace(t, h.home, wu15Workspace, ids...)
			h = wu15Boot(t, h.home, h.cfg)
			before := h.listRooms(wu15Workspace)
			name := "Maximum membership"
			requestedIDs := append([]string(nil), ids...)
			if size > 256 {
				// Exercise the raw array bound without an out-of-contract
				// 257-member team room. There are still 256 distinct eligible
				// members; the extra occurrence violates maxItems only.
				requestedIDs = append(requestedIDs, ids[0])
			}
			status, data := h.http(http.MethodPost, "/api/v1/workspaces/sales/rooms", gen.RoomCreateRequest{Kind: gen.RoomCreateRequestKindSquad, Name: &name, AgentIds: requestedIDs})
			if size > 256 {
				wu15RequireError(t, status, data, http.StatusUnprocessableEntity, "")
				require.ElementsMatch(t, before, h.listRooms(wu15Workspace), "K24: over-limit input saves nothing")
			} else {
				require.Equal(t, http.StatusCreated, status, "K24: upper boundary valid: %s", data)
				var room gen.Room
				require.NoError(t, json.Unmarshal(data, &room))
				require.ElementsMatch(t, ids, room.Members)
			}
		})
	}
}

func TestWU15Rooms_MentionArrayBoundariesDeduplicateOneDeliveryPerMember(t *testing.T) {
	for _, count := range []int{0, 1, 2, 255, 256, 257} {
		t.Run(fmt.Sprintf("mentions_%d", count), func(t *testing.T) {
			h := wu15NewHarness(t)
			room := h.create("squad", "Launch", "mia", "jim")
			mentions := make([]string, count)
			for index := range mentions {
				mentions[index] = "mia"
			}
			before := h.snapshot(room.Id)
			status, data := h.http(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", gen.RoomMessageRequest{ClientMessageId: "mentions", Text: "hello", Mentions: mentions})
			if count > 256 {
				wu15RequireError(t, status, data, http.StatusBadRequest, "")
				wu15UnchangedSnapshot(t, before, h.snapshot(room.Id))
			} else {
				require.Equal(t, http.StatusAccepted, status, "K26: mention array within bound: %s", data)
				entry := wu15FindEntry(t, h.snapshot(room.Id), "mentions")
				want := []string{"mia"}
				if count == 0 {
					want = []string{"mia", "jim"}
				}
				require.ElementsMatch(t, want, entry.Addressees)
				require.Len(t, entry.Deliveries, len(want), "FR-216: one delivery per member, not per duplicated token")
			}
		})
	}
}

func TestWU15Rooms_SquadNameUnicodeCharacterBoundaries(t *testing.T) {
	for _, count := range []int{1, 80, 81} {
		t.Run(fmt.Sprintf("unicode_name_%d", count), func(t *testing.T) {
			h := wu15NewHarness(t)
			h.create("squad", "Control", "mia", "jim")
			before := h.listRooms(wu15Workspace)
			name := strings.Repeat("界", count) // JSON Schema maxLength counts characters, not UTF-8 bytes.
			status, data := h.http(http.MethodPost, "/api/v1/workspaces/sales/rooms", gen.RoomCreateRequest{Kind: gen.RoomCreateRequestKindSquad, Name: &name, AgentIds: []string{"mia", "jim"}})
			if count > 80 {
				wu15RequireError(t, status, data, http.StatusUnprocessableEntity, "")
				require.ElementsMatch(t, before, h.listRooms(wu15Workspace))
			} else {
				require.Equal(t, http.StatusCreated, status, "K24: Unicode character bound: %s", data)
				var room gen.Room
				require.NoError(t, json.Unmarshal(data, &room))
				require.Equal(t, name, room.Name)
			}
		})
	}
}
