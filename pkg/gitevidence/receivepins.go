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
//
// Re-review F6 (h55-review-2.md): a global core.hooksPath would make the
// receiving git run a hook from an agent-written folder inside the evidence
// repository, and core.alternateRefsCommand names a program it may run. Both
// are pinned locally as well.

import (
	"fmt"
	"os"
	"path/filepath"
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

// noHooksDirName is the engine-owned hooks folder the evidence repository's
// core.hooksPath points at, inside its .git (which the exec guard protects).
// The engine keeps it absent, so no hook ever runs in the evidence repository.
const noHooksDirName = "omnipus-no-hooks"

// alternateRefsNoop is the pinned core.alternateRefsCommand: a shell no-op
// that names no alternate refs, instead of a program a global config chose.
const alternateRefsNoop = "true"

// corePins returns the [core] settings pinned for the repository at dir.
func corePins(dir string) ([]struct{ key, value string }, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("gitevidence: resolve %s for the hooks pin: %w", dir, err)
	}
	return []struct{ key, value string }{
		{"bare", "false"},
		{"hooksPath", filepath.ToSlash(filepath.Join(abs, ".git", noHooksDirName))},
		{"alternateRefsCommand", alternateRefsNoop},
	}, nil
}

// pinReceiveConfig writes the core and receive pins into the local config of
// the repository at dir when any of them is missing or different, drops any
// include/includeIf section (an included file could set a later value) and
// removes anything placed in the pinned hooks folder. It writes no config when
// everything is already in place.
func pinReceiveConfig(repo *git.Repository, dir string) error {
	core, err := corePins(dir)
	if err != nil {
		return err
	}
	noHooks := filepath.Join(dir, ".git", noHooksDirName)
	if _, statErr := os.Lstat(noHooks); statErr == nil {
		if rmErr := os.RemoveAll(noHooks); rmErr != nil {
			return fmt.Errorf("gitevidence: clear the pinned hooks folder %s: %w", noHooks, rmErr)
		}
	}
	cfg, err := repo.Config()
	if err != nil {
		return fmt.Errorf("gitevidence: read local config for the receive pins: %w", err)
	}
	if receivePinsHold(cfg.Raw, core) && !cfg.Core.IsBare {
		return nil
	}
	cfg.Core.IsBare = false
	cfg.Raw.RemoveSection("include")
	cfg.Raw.RemoveSection("includeIf")
	// Rebuild one [core] and one [receive] section (git: the last value in the
	// file wins), keeping every other option the repo had in them.
	rebuildPinnedSection(cfg.Raw, "core", core)
	rebuildPinnedSection(cfg.Raw, "receive", receivePins)
	if err := repo.SetConfig(cfg); err != nil {
		return fmt.Errorf("gitevidence: write the receive pins into the local config: %w", err)
	}
	return nil
}

// rebuildPinnedSection replaces every section called name with one section at
// the end of raw that keeps the other options and holds each pin once.
func rebuildPinnedSection(raw *formatcfg.Config, name string, pins []struct{ key, value string }) {
	var kept formatcfg.Options
	for _, s := range raw.Sections {
		if !strings.EqualFold(s.Name, name) {
			continue
		}
		for _, o := range s.Options {
			if !isPinKey(pins, o.Key) {
				kept = append(kept, o)
			}
		}
	}
	raw.RemoveSection(name)
	section := raw.Section(name)
	section.Options = kept
	for _, p := range pins {
		section.AddOption(p.key, p.value)
	}
}

func isPinKey(pins []struct{ key, value string }, key string) bool {
	for _, p := range pins {
		if strings.EqualFold(p.key, key) {
			return true
		}
	}
	return false
}

// receivePinsHold reports whether raw already holds every pin exactly once
// with its value and no include section is present.
func receivePinsHold(raw *formatcfg.Config, core []struct{ key, value string }) bool {
	receiveSections, coreSections := 0, 0
	for _, s := range raw.Sections {
		switch {
		case strings.EqualFold(s.Name, "include") || strings.EqualFold(s.Name, "includeIf"):
			return false
		case strings.EqualFold(s.Name, "receive"):
			receiveSections++
		case strings.EqualFold(s.Name, "core"):
			coreSections++
		}
	}
	if receiveSections != 1 || coreSections != 1 {
		return false
	}
	if !pinsHoldIn(raw.Section("core"), core) {
		return false
	}
	receive := raw.Section("receive")
	if len(receive.Subsections) > 0 {
		return false
	}
	return pinsHoldIn(receive, receivePins)
}

func pinsHoldIn(s *formatcfg.Section, pins []struct{ key, value string }) bool {
	for _, p := range pins {
		all := s.OptionAll(p.key)
		if len(all) != 1 || all[0] != p.value {
			return false
		}
	}
	return true
}
