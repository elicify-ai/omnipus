// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Founder decision 2026-10-11 (metallic avatar palette). One old -> new table
// serves both directions; no stored file is rewritten.
var oldToNewPalette = [][2]string{
	{"#3B82F6", "#6E8BA8"}, {"#38BDF8", "#7A8C9E"}, {"#22D3EE", "#4F8A80"}, {"#818CF8", "#5E6973"},
	{"#A78BFA", "#7B7290"}, {"#E879F9", "#B76E79"}, {"#FB923C", "#A97142"}, {"#9CA3AF", "#8E9196"},
	{"#F472B6", "#CDBA96"}, {"#C084FC", "#9C8A5B"},
}

func newPaletteAgent(t *testing.T, api *restAPI, color string) string {
	t.Helper()
	rec := postAgent(t, api, `{"name":"Palette `+strings.TrimPrefix(color, "#")+`","type":"Main","soul":"s","color":"`+color+`"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	id, ok := decodeObject(t, rec.Body.Bytes())["id"].(string)
	require.True(t, ok)
	return id
}

// Write: an old value on PUT is mapped to its new equivalent and stored so.
func TestAvatarPalette_PutMapsAnOldValue(t *testing.T) {
	for _, pair := range oldToNewPalette {
		t.Run(pair[0], func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			id := newPaletteAgent(t, api, "#6E8BA8")
			rec := putAgent(t, api, id, `{"color":"`+pair[0]+`"}`)
			require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
			assert.Equal(t, pair[1], decodeObject(t, rec.Body.Bytes())["color"], "the response carries the new value")
			assert.Equal(t, pair[1], savedAgent(t, api, id)["color"], "the stored value is the new one")
		})
	}
}

// Write: a lower-case old value maps too (the letter-case rule is unchanged).
func TestAvatarPalette_PutMapsALowerCaseOldValue(t *testing.T) {
	api := buildExecutorTestAPI(t)
	id := newPaletteAgent(t, api, "#6E8BA8")
	rec := putAgent(t, api, id, `{"color":"#fb923c"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "#A97142", savedAgent(t, api, id)["color"])
}

// Write: a value in neither set is refused with 400 and nothing is written.
func TestAvatarPalette_PutRefusesAValueInNeitherSet(t *testing.T) {
	for _, bad := range []string{"#D4AF37", "#ff0000", "#6E8BA9", "nope"} {
		t.Run(bad, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			id := newPaletteAgent(t, api, "#6E8BA8")
			rec := putAgent(t, api, id, `{"color":"`+bad+`"}`)
			require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
			assert.Equal(t, "#6E8BA8", savedAgent(t, api, id)["color"], "a refused update writes nothing")
		})
	}
}

// Create maps an old value as well, so a stale client cannot store the old set.
func TestAvatarPalette_CreateMapsAnOldValue(t *testing.T) {
	api := buildExecutorTestAPI(t)
	id := newPaletteAgent(t, api, "#C084FC")
	assert.Equal(t, "#9C8A5B", savedAgent(t, api, id)["color"])
}

// Read: a stored OLD value is returned as the new one (detail and list), and the
// stored record is left exactly as it was (greenfield: no rewrite).
func TestAvatarPalette_ReadReturnsTheNewValueAndWritesNothing(t *testing.T) {
	for _, pair := range oldToNewPalette {
		t.Run(pair[0], func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			id := newPaletteAgent(t, api, "#6E8BA8")
			_, err := agentstore.New(api.homePath).Update(id, func(a *config.AgentConfig) error {
				a.Color = pair[0] // an install that predates the new palette
				return nil
			})
			require.NoError(t, err)

			assert.Equal(t, pair[1], getAgentObject(t, api, id)["color"], "detail read")
			assert.Equal(t, pair[1], findListedAgent(t, api, id)["color"], "list read")
			assert.Equal(t, pair[0], savedAgent(t, api, id)["color"], "no stored-file rewrite")
		})
	}
}

// Read: a stored value in neither set reads as the default, never as itself.
func TestAvatarPalette_ReadOfAnUnknownStoredValueIsTheDefault(t *testing.T) {
	api := buildExecutorTestAPI(t)
	id := newPaletteAgent(t, api, "#6E8BA8")
	_, err := agentstore.New(api.homePath).Update(id, func(a *config.AgentConfig) error {
		a.Color = "#D4AF37"
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "#8E9196", getAgentObject(t, api, id)["color"])
}
