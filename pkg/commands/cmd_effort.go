package commands

// cmd_effort.go — the /effort command (effort-levels spec FR-023, S-36..S-42,
// S-70): conversation-scoped on messengers, never the agent-wide model switch
// or any agent writer. On the web the composer handles /effort itself
// (delivery client, like /model); a web arrival (omnipus run) gets the
// chip hint and changes nothing.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// EffortState is what /effort needs to know about the conversation: the
// serving model, its served reasoning capability, the conversation's own
// stored value and the agent's setting.
type EffortState struct {
	Model string
	// Known is false when the model has no catalog row (custom or local):
	// any value up to 32 characters is accepted there.
	Known   bool
	Reasons bool
	Levels  []string
	Switch  bool
	// Current is the conversation's stored value; "" = Not set.
	Current string
	// AgentSetting is the agent's own setting (or the default model's when
	// the agent rides it); "" = Not set.
	AgentSetting string
}

const (
	effortWebBranchMsg = "Use the effort chip next to the model in the web chat. Effort isn't available in omnipus run yet."
	effortInvalidMsg   = "That isn't a valid effort level."
	effortMaxChars     = 32
)

// effortRankedNames are the known level names of the rank table (DE-5).
var effortRankedNames = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

func effortCommand() Definition {
	return Definition{
		Name:         "effort",
		Description:  "Show or set reasoning effort for this conversation",
		Usage:        "/effort [level|on|off|default|reset]",
		ArgumentHint: "[level|on|off|default|reset]",
		Surfaces:     []Surface{SurfaceWeb, SurfaceChannel},
		Delivery:     DeliveryClient,
		Handler:      effortHandler,
	}
}

func effortHandler(_ context.Context, req Request, rt *Runtime) error {
	if SurfaceForChannel(req.Channel) == SurfaceWeb {
		return req.Reply(effortWebBranchMsg)
	}
	arg := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(req.Text), firstToken(req.Text))))
	if rt == nil || rt.GetEffortState == nil || rt.SetConversationEffort == nil {
		return req.Reply(unavailableMsg)
	}
	st, err := rt.GetEffortState()
	if err != nil {
		return err
	}
	if arg == "" {
		return req.Reply(effortStatus(st))
	}
	reply, value, change := decideEffort(st, arg)
	if change {
		if err := rt.SetConversationEffort(value); err != nil {
			return err
		}
	}
	return req.Reply(reply)
}

func firstToken(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// decideEffort validates one /effort argument against the conversation's
// model (FR-048: /effort checks the catalog) and returns the reply, the value
// to store and whether to store it.
func decideEffort(st EffortState, arg string) (reply, value string, change bool) {
	if utf8.RuneCountInString(arg) > effortMaxChars || strings.ContainsAny(arg, " \t") {
		return effortInvalidMsg, "", false
	}
	switch arg {
	case "default", "provider_default":
		return fmt.Sprintf("Effort for %s: Provider default (the provider decides).", st.Model), "provider_default", true
	case "reset":
		return fmt.Sprintf("Effort for %s: back to the agent's setting (%s).", st.Model, effortSettingLabel(st.AgentSetting)), "", true
	}
	if !st.Known {
		// No catalog row (custom or local): any value is accepted; such a
		// value is never sent (Ambiguity 4).
		return effortSetMsg(st.Model, arg), arg, true
	}
	if !st.Reasons {
		return fmt.Sprintf("%s doesn't support reasoning effort.", st.Model), "", false
	}
	if len(st.Levels) == 0 && !st.Switch {
		return fmt.Sprintf("Omnipus can't change effort for %s.", st.Model), "", false
	}
	switch arg {
	case "off", "none":
		if slices.Contains(st.Levels, "none") || st.Switch {
			return effortSetMsg(st.Model, "off"), "off", true
		}
		return fmt.Sprintf("%s can't turn reasoning off.", st.Model), "", false
	case "on":
		if st.Switch {
			return effortSetMsg(st.Model, "on"), "on", true
		}
		return effortUnoffered(st, arg), "", false
	}
	known := slices.Contains(effortRankedNames, arg) || slices.Contains(st.Levels, arg)
	if !known {
		return effortInvalidMsg, "", false
	}
	if len(st.Levels) == 0 {
		return fmt.Sprintf("%s only switches reasoning on or off. Use /effort on, /effort off or /effort default.", st.Model), "", false
	}
	if slices.Contains(st.Levels, arg) {
		return effortSetMsg(st.Model, arg), arg, true
	}
	return effortUnoffered(st, arg), "", false
}

func effortSetMsg(model, value string) string {
	return fmt.Sprintf("Effort for %s: %s.", model, effortSettingLabel(value))
}

func effortUnoffered(st EffortState, arg string) string {
	return fmt.Sprintf("%s doesn't offer %s. Options: %s. Nothing changed.", st.Model, arg, effortOptions(st))
}

// effortStatus is the bare /effort reply.
func effortStatus(st EffortState) string {
	current := st.Current
	if current == "" {
		current = st.AgentSetting
	}
	if !st.Known || !st.Reasons || (len(st.Levels) == 0 && !st.Switch) {
		return fmt.Sprintf("Effort for %s: %s. /effort reset returns to the agent's setting.", st.Model, effortSettingLabel(current))
	}
	return fmt.Sprintf("Effort for %s: %s. Options: %s. /effort reset returns to the agent's setting.",
		st.Model, effortSettingLabel(current), effortOptions(st))
}

// effortOptions lists what /effort accepts for the model, in order:
// Provider default, Off (where offered), the levels as published, On.
func effortOptions(st EffortState) string {
	opts := []string{"Provider default"}
	if slices.Contains(st.Levels, "none") || st.Switch {
		opts = append(opts, "Off")
	}
	for _, l := range st.Levels {
		if l != "none" {
			opts = append(opts, l)
		}
	}
	if st.Switch {
		opts = append(opts, "On")
	}
	return strings.Join(opts, ", ")
}

// effortSettingLabel is the display label of a stored value.
func effortSettingLabel(v string) string {
	switch v {
	case "":
		return "Not set"
	case "provider_default":
		return "Provider default"
	case "off", "none":
		return "Off"
	case "on":
		return "On"
	case "xhigh":
		return "XHigh"
	}
	r := []rune(v)
	return strings.ToUpper(string(r[:1])) + string(r[1:])
}
