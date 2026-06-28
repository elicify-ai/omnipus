package commands

// Surface identifies the origin surface on which a command is being executed.
// It is derived at dispatch time from Request.Channel.
type Surface int

const (
	// SurfaceWeb is the web chat UI (Channel == "webchat").
	SurfaceWeb Surface = iota
	// SurfaceCLI is the omnipus CLI REPL (Channel == "cli").
	SurfaceCLI
	// SurfaceChannel is any third-party messaging channel (Telegram, Discord, etc.).
	SurfaceChannel
)

// SurfaceForChannel maps a channel origin string to a Surface.
// "webchat" → Web, "cli" → CLI, everything else → Channel.
// Verified against runtime sources:
//   - pkg/gateway/websocket.go sets Channel="webchat"
//   - CLI agent REPL sets Channel="cli"
//   - Channel adapters set their factory name (e.g. "telegram", "discord")
func SurfaceForChannel(channel string) Surface {
	switch channel {
	case "webchat":
		return SurfaceWeb
	case "cli":
		return SurfaceCLI
	default:
		return SurfaceChannel
	}
}

// DeliveryMode controls how the web SPA dispatches a command.
// It is only meaningful for web-surfaced commands; non-web commands default to Agent.
type DeliveryMode string

const (
	// DeliveryClient means the SPA handles the command locally and does NOT
	// send it to the agent (e.g. /clear, /model, /help, /cancel).
	DeliveryClient DeliveryMode = "client"
	// DeliveryAgent means the SPA inserts the command text and forwards it
	// via the message frame (e.g. /skill).
	DeliveryAgent DeliveryMode = "agent"
)

// AllowsSurface reports whether definition d is available on surface s.
// An empty Surfaces slice means the command is available on all surfaces (back-compat default).
func (d Definition) AllowsSurface(s Surface) bool {
	if len(d.Surfaces) == 0 {
		return true
	}
	for _, allowed := range d.Surfaces {
		if allowed == s {
			return true
		}
	}
	return false
}
