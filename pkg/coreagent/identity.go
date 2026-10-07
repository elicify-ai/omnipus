package coreagent

import (
	"math"
	"strconv"
	"strings"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Defaults applied when a create omits identity, and when a stored value is
// missing or not in the closed set. Boot is what persists them; a read only
// emits them.
const (
	DefaultFigure = string(generated.AgentFigureOmnipus)
	DefaultRole   = string(generated.AgentRoleGeneral)
	DefaultColor  = string(generated.AgentColorHash9CA3AF)
)

// paletteHexes is the published order. Tie-break keeps the earlier entry.
// Grey is last and is never a hue-distance candidate.
var paletteHexes = []string{
	string(generated.AgentColorHash3B82F6),
	string(generated.AgentColorHash38BDF8),
	string(generated.AgentColorHash22D3EE),
	string(generated.AgentColorHash818CF8),
	string(generated.AgentColorHashA78BFA),
	string(generated.AgentColorHashC084FC),
	string(generated.AgentColorHashE879F9),
	string(generated.AgentColorHashF472B6),
	string(generated.AgentColorHashFB923C),
}

type paletteHue struct {
	hex string
	hue float64
}

var paletteHues []paletteHue

func init() {
	paletteHues = make([]paletteHue, len(paletteHexes))
	for i, hex := range paletteHexes {
		hue, _, ok := identityHSV(hex)
		if !ok {
			panic("coreagent: palette hex is not # plus six digits: " + hex)
		}
		paletteHues[i] = paletteHue{hex: hex, hue: hue}
	}
}

// CanonicalFigure reports whether raw is one of the four figure words, exact case.
func CanonicalFigure(raw string) (string, bool) {
	f := generated.AgentFigure(raw)
	if !f.Valid() {
		return "", false
	}
	return string(f), true
}

// CanonicalRole reports whether raw is one of the 31 role slugs, exact case.
func CanonicalRole(raw string) (string, bool) {
	r := generated.AgentRole(raw)
	if !r.Valid() {
		return "", false
	}
	return string(r), true
}

// CanonicalColor accepts any letter-case of the ten palette hexes and returns
// the uppercase enum value. Any other string, including a well-formed hex
// outside the palette, is not canonical.
func CanonicalColor(raw string) (string, bool) {
	c := generated.AgentColor(strings.ToUpper(raw))
	if !c.Valid() {
		return "", false
	}
	return string(c), true
}

// WireIdentity is the read backstop. A stored value already in the enum is
// emitted (colour letter-case is normalized). Anything else emits the default.
// It does not map an old hex and it does not write.
func WireIdentity(figure, role, color string) (generated.AgentFigure, generated.AgentRole, generated.AgentColor) {
	fig := generated.AgentFigure(DefaultFigure)
	if f := generated.AgentFigure(figure); f.Valid() {
		fig = f
	}
	outRole := generated.AgentRole(DefaultRole)
	if r := generated.AgentRole(role); r.Valid() {
		outRole = r
	}
	outColor := generated.AgentColor(DefaultColor)
	if canon, ok := CanonicalColor(color); ok {
		outColor = generated.AgentColor(canon)
	} else if color != "" {
		// A not-yet-migrated hex cannot be emitted (the wire enum would
		// reject it). Show the palette colour boot will persist. Do not write.
		outColor = generated.AgentColor(NearestPalette(color))
	}
	return fig, outRole, outColor
}

// RoleFromLegacyIcon maps a stored Phosphor icon name to a role slug.
// The five spec keys are matched after trim and lower-case, without stripping
// hyphens. An icon that is already exactly one of the 31 slugs is kept.
// Anything else is general.
func RoleFromLegacyIcon(icon string) string {
	folded := strings.ToLower(strings.TrimSpace(icon))
	switch folded {
	case "code":
		return string(generated.AgentRoleDeveloper)
	case "chat":
		return string(generated.AgentRoleGeneral)
	case "magnifyingglass":
		return string(generated.AgentRoleResearcher)
	case "pencilsimple":
		return string(generated.AgentRoleWriter)
	case "shield":
		return string(generated.AgentRoleSecurity)
	}
	trimmed := strings.TrimSpace(icon)
	if generated.AgentRole(trimmed).Valid() {
		return trimmed
	}
	return DefaultRole
}

// NearestPalette maps one stored hex onto the ten-colour palette.
// A value that is not exactly "#" plus six hex digits is Grey.
// Saturation is HSV saturation (max-min)/max; under 0.25 is Grey.
// Otherwise the nearest non-Grey hue wins, and a tie keeps the earlier entry.
func NearestPalette(hex string) string {
	hue, sat, ok := identityHSV(hex)
	if !ok || sat < 0.25 {
		return DefaultColor
	}
	best := math.MaxFloat64
	winner := DefaultColor
	for _, entry := range paletteHues {
		dist := hueDistance(hue, entry.hue)
		if dist < best {
			best = dist
			winner = entry.hex
		}
	}
	return winner
}

// migrateUnenforcedAgentIdentity rewrites figure, role, and colour on agents
// SeedConfig does not enforce (not a compiled built-in, not a system agent).
// A value already in the enum is left alone, apart from colour letter-case.
// Icon is never written. Returns whether any field changed.
func migrateUnenforcedAgentIdentity(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	changed := false
	for i := range cfg.Agents.List {
		a := &cfg.Agents.List[i]
		if identityEnforced(a.ID) {
			continue
		}
		if !generated.AgentRole(a.Role).Valid() {
			next := RoleFromLegacyIcon(a.Icon)
			if a.Role != next {
				a.Role = next
				changed = true
			}
		}
		if !generated.AgentFigure(a.Figure).Valid() {
			if a.Figure != DefaultFigure {
				a.Figure = DefaultFigure
				changed = true
			}
		}
		if canon, ok := CanonicalColor(a.Color); ok {
			if a.Color != canon {
				a.Color = canon
				changed = true
			}
		} else {
			next := NearestPalette(a.Color)
			if a.Color != next {
				a.Color = next
				changed = true
			}
		}
	}
	return changed
}

func identityEnforced(id string) bool {
	if ByID(CoreAgentID(id)) != nil {
		return true
	}
	for _, sa := range SystemAgents() {
		if string(sa.ID) == id {
			return true
		}
	}
	return false
}

func identityHSV(hex string) (hue, saturation float64, ok bool) {
	if len(hex) != 7 || hex[0] != '#' || !hexDigits(hex[1:]) {
		return 0, 0, false
	}
	r16, _ := strconv.ParseUint(hex[1:3], 16, 8)
	g16, _ := strconv.ParseUint(hex[3:5], 16, 8)
	b16, _ := strconv.ParseUint(hex[5:7], 16, 8)
	r := float64(r16) / 255
	g := float64(g16) / 255
	b := float64(b16) / 255
	mx := math.Max(r, math.Max(g, b))
	mn := math.Min(r, math.Min(g, b))
	d := mx - mn
	if mx == 0 {
		saturation = 0
	} else {
		saturation = d / mx
	}
	if d == 0 {
		return 0, saturation, true
	}
	switch mx {
	case r:
		hue = positiveMod((g-b)/d, 6) * 60
	case g:
		hue = ((b-r)/d + 2) * 60
	default:
		hue = ((r-g)/d + 4) * 60
	}
	hue = positiveMod(hue, 360)
	return hue, saturation, true
}

func hexDigits(s string) bool {
	if len(s) != 6 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

func positiveMod(a, b float64) float64 {
	r := math.Mod(a, b)
	if r < 0 {
		r += b
	}
	return r
}

func hueDistance(a, b float64) float64 {
	d := math.Abs(a - b)
	wrapped := math.Abs(360 - d)
	if wrapped < d {
		return wrapped
	}
	return d
}
