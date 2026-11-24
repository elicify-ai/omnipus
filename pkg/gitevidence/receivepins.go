package gitevidence

// H55 v2 (#1960 S1, architect ruling ruling-H55-v2.md): the evidence
// repository refuses pushes by its OWN local config. A push from an agent's
// write mount runs git receive-pack on this repository, and receive-pack reads
// every config level. A global or GIT_CONFIG_GLOBAL setting
// (receive.denyCurrentBranch=ignore, core.bare=true) would otherwise switch off
// git's refusal to move the checked-out branch. Local config ranks above
// global and system config, so these pins close that route for every caller;
// only command-line and environment config rank higher, and the exec guard's
// Rule A (pkg/sandbox) refuses those.

import (
	"fmt"
	"strings"

	git "github.com/go-git/go-git/v5"
	formatcfg "github.com/go-git/go-git/v5/plumbing/format/config"
)

// receivePins are the [receive] settings written into the evidence
// repository's local config. They restate git's defaults; the point is that
// they are LOCAL, so a global setting cannot override them.
var receivePins = []struct{ key, value string }{
	{"denyCurrentBranch", "refuse"},
	{"denyNonFastForwards", "true"},
	{"denyDeletes", "true"},
}

// pinReceiveConfig writes core.bare=false and the receive pins into repo's
// local config when any of them is missing or different, and drops any
// include/includeIf section, whose included file could set a later value.
// It writes nothing when everything is already in place.
func pinReceiveConfig(repo *git.Repository) error {
	cfg, err := repo.Config()
	if err != nil {
		return fmt.Errorf("gitevidence: read local config for the receive pins: %w", err)
	}
	if receivePinsHold(cfg.Raw) && !cfg.Core.IsBare {
		return nil
	}
	cfg.Core.IsBare = false
	cfg.Raw.Section("core").SetOption("bare", "false")
	cfg.Raw.RemoveSection("include")
	cfg.Raw.RemoveSection("includeIf")
	// Rebuild one [receive] section at the end of the file (git: the last
	// value wins), keeping any other receive.* option the repo had.
	var kept formatcfg.Options
	for _, s := range cfg.Raw.Sections {
		if !strings.EqualFold(s.Name, "receive") {
			continue
		}
		for _, o := range s.Options {
			if !isReceivePinKey(o.Key) {
				kept = append(kept, o)
			}
		}
	}
	cfg.Raw.RemoveSection("receive")
	receive := cfg.Raw.Section("receive")
	receive.Options = kept
	for _, p := range receivePins {
		receive.AddOption(p.key, p.value)
	}
	if err := repo.SetConfig(cfg); err != nil {
		return fmt.Errorf("gitevidence: write the receive pins into the local config: %w", err)
	}
	return nil
}

func isReceivePinKey(key string) bool {
	for _, p := range receivePins {
		if strings.EqualFold(p.key, key) {
			return true
		}
	}
	return false
}

// receivePinsHold reports whether raw already holds every pin exactly once
// with its value, core.bare is false and no include section is present.
func receivePinsHold(raw *formatcfg.Config) bool {
	for _, s := range raw.Sections {
		if strings.EqualFold(s.Name, "include") || strings.EqualFold(s.Name, "includeIf") {
			return false
		}
	}
	if !raw.HasSection("core") || !strings.EqualFold(raw.Section("core").Option("bare"), "false") {
		return false
	}
	receiveSections := 0
	for _, s := range raw.Sections {
		if strings.EqualFold(s.Name, "receive") {
			receiveSections++
		}
	}
	if receiveSections != 1 {
		return false
	}
	receive := raw.Section("receive")
	if len(receive.Subsections) > 0 {
		return false
	}
	for _, p := range receivePins {
		all := receive.OptionAll(p.key)
		if len(all) != 1 || all[0] != p.value {
			return false
		}
	}
	return true
}
