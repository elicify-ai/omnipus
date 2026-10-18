package systools

import (
	"errors"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// The gateway callback is the only cleanup writer. A standalone tool may
// proceed without that dependency only when its current config proves that
// no channel is bound; missing/unknown config must never bypass cleanup.
func unbindWorkspaceChannels(d *Deps, workspaceID string) ([]string, error) {
	if d != nil && d.UnbindWorkspaceChannels != nil {
		return d.UnbindWorkspaceChannels(workspaceID)
	}
	if d == nil || d.GetCfg == nil {
		return nil, errors.New("workspace channel cleanup is not wired and channel configuration is unavailable")
	}
	cfg := d.GetCfg()
	if cfg == nil {
		return nil, errors.New("workspace channel cleanup is not wired and channel configuration is unavailable")
	}
	for _, instance := range cfg.Channels {
		if instance.WorkspaceID == workspaceID {
			return nil, errors.New("workspace channel cleanup is not wired for this bound workspace")
		}
	}
	return nil, nil
}

// A failed live refresh can follow a durable config write. Those channel
// changes remain saved even though the workspace deletion is refused.
func workspaceDeleteChannelFailure(id string, changedChannels []string, reason string) *tools.ToolResult {
	if len(changedChannels) == 0 {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", reason, ""))
	}
	return changeErrorWithOutcome(map[string]any{
		"success": false, "id": id,
		"error":                         map[string]any{"code": "SAVE_FAILED", "message": reason},
		"channels_disabled_and_unbound": changedChannels,
		"changed_fields":                []string{"channels"},
	}, partlySaved(reason, "channel cleanup persisted; workspace record retained", []string{"workspace deletion"}))
}
