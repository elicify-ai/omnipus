package gateway

import (
	"net/http"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/commands"
)

// HandleListCommands handles GET /api/v1/commands?surface=web|cli|channel.
// It returns the canonical, surface-applicable commands as SlashCommand[].
// Aliases and deprecated/hidden commands are NOT returned as entries.
// The ?surface param defaults to "web" for unknown or absent values (FR-015).
//
// operationId: listCommands
func (a *restAPI) HandleListCommands(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	surfaceParam := r.URL.Query().Get("surface")
	surface := parseSurface(surfaceParam)

	defs := commands.BuiltinDefinitions()
	result := make([]gen.SlashCommand, 0, len(defs))
	for _, def := range defs {
		if def.Hidden {
			continue
		}
		if !def.AllowsSurface(surface) {
			continue
		}
		result = append(result, defToSlashCommand(def))
	}

	jsonOK(w, result)
}

// parseSurface maps the ?surface= query parameter to a commands.Surface.
// Unknown or absent values default to SurfaceWeb (FR-015).
func parseSurface(param string) commands.Surface {
	switch param {
	case "cli":
		return commands.SurfaceCLI
	case "channel":
		return commands.SurfaceChannel
	default:
		// "web", absent, or unknown → SurfaceWeb (lenient, FR-015)
		return commands.SurfaceWeb
	}
}

// defToSlashCommand converts a commands.Definition to the generated SlashCommand wire type.
// The Delivery field maps the internal DeliveryMode string directly to SlashCommandDelivery.
func defToSlashCommand(def commands.Definition) gen.SlashCommand {
	sc := gen.SlashCommand{
		Name:        def.Name,
		Label:       "/" + def.Name,
		Description: def.Description,
		Delivery:    gen.SlashCommandDelivery(def.Delivery),
	}

	// Usage: only populate when non-empty.
	if usage := def.EffectiveUsage(); usage != "" {
		sc.Usage = &usage
	}

	// AvailableWhileStreaming: only /cancel is available mid-turn.
	if def.Name == "cancel" {
		t := true
		sc.AvailableWhileStreaming = &t
	}

	// Aliases: populate when non-empty (informational for the SPA).
	if len(def.Aliases) > 0 {
		aliases := make([]string, len(def.Aliases))
		copy(aliases, def.Aliases)
		sc.Aliases = &aliases
	}

	return sc
}
