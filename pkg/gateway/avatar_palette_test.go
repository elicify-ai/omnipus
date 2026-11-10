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

// Founder ruling 2026-10-12 (greenfield): the previous ten-colour palette is gone.
// Its values are invalid like any unknown colour.
var removedPalette = []string{
	"#3B82F6", "#38BDF8", "#22D3EE", "#818CF8", "#A78BFA",
	"#E879F9", "#FB923C", "#9CA3AF", "#F472B6", "#C084FC",
}

func newPaletteAgent(t *testing.T, api *restAPI, color string) string {
	t.Helper()
	rec := postAgent(t, api, `{"name":"Palette `+strings.TrimPrefix(color, "#")+`","type":"Main","soul":"s","color":"`+color+`"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	id, ok := decodeObject(t, rec.Body.Bytes())["id"].(string)
	require.True(t, ok)
	return id
}

// Write: a value outside the palette (a removed previous-palette value included) is
// refused with 400 and nothing is written.
func TestAvatarPalette_PutRefusesAValueOutsideThePalette(t *testing.T) {
	for _, bad := range append([]string{"#D4AF37", "#ff0000", "#6E8BA9", "nope"}, removedPalette...) {
		t.Run(bad, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			id := newPaletteAgent(t, api, "#6E8BA8")
			rec := putAgent(t, api, id, `{"color":"`+bad+`"}`)
			require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
			assert.Equal(t, "#6E8BA8", savedAgent(t, api, id)["color"], "a refused update writes nothing")
		})
	}
}

// Create refuses a removed previous-palette value too.
func TestAvatarPalette_CreateRefusesARemovedValue(t *testing.T) {
	api := buildExecutorTestAPI(t)
	rec := postAgent(t, api, `{"name":"Old Colour","type":"Main","soul":"s","color":"#C084FC"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

// setStoredColor puts a colour into the agent's stored record AND the loaded
// configuration the read path serves, as an install from before the palette
// change would present it.
func setStoredColor(t *testing.T, api *restAPI, id, color string) {
	t.Helper()
	_, err := agentstore.New(api.homePath).Update(id, func(a *config.AgentConfig) error {
		a.Color = color
		return nil
	})
	require.NoError(t, err)
	cfg := api.agentLoop.GetConfig()
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == id {
			cfg.Agents.List[i].Color = color
			return
		}
	}
	t.Fatalf("agent %s is not in the loaded configuration", id)
}

// Read: a stored value of the removed palette reads as the default (detail and
// list), and the stored record is left exactly as it was (no rewrite).
func TestAvatarPalette_ReadOfARemovedStoredValueIsTheDefault(t *testing.T) {
	for _, old := range removedPalette {
		t.Run(old, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			id := newPaletteAgent(t, api, "#6E8BA8")
			setStoredColor(t, api, id, old)

			assert.Equal(t, "#8E9196", getAgentObject(t, api, id)["color"], "detail read")
			assert.Equal(t, "#8E9196", findListedAgent(t, api, id)["color"], "list read")
			assert.Equal(t, old, savedAgent(t, api, id)["color"], "no stored-file rewrite")
		})
	}
}

// Read: a stored value in neither set reads as the default, never as itself.
func TestAvatarPalette_ReadOfAnUnknownStoredValueIsTheDefault(t *testing.T) {
	api := buildExecutorTestAPI(t)
	id := newPaletteAgent(t, api, "#6E8BA8")
	setStoredColor(t, api, id, "#D4AF37")
	assert.Equal(t, "#8E9196", getAgentObject(t, api, id)["color"])
}
