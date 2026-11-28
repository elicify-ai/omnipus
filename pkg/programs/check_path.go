// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agenthome"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// ProgramPathCheck decides whether the program found on the gateway PATH for
// an account check may run with gateway authority (FR-005, S-08). It returns
// the file to execute, or an error when the program lies in a folder agents
// can write to or its location cannot be checked. The gateway installs the
// shared third-party program check (thirdpartycli.CheckProgramPath over
// AgentWritableRoots), which this package cannot import without a cycle.
type ProgramPathCheck func(programWord, foundPath string, cfg *config.Config) (string, error)

// SetProgramPathCheck installs the account check's program location check.
// Call it before the first check starts. Without it, checkProgramLocation
// refuses programs under the Omnipus data folder, every agent home and every
// sandbox allowed path.
func (s *Service) SetProgramPathCheck(fn ProgramPathCheck) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pathCheck = fn
}

func (s *Service) programPathCheck() ProgramPathCheck {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pathCheck != nil {
		return s.pathCheck
	}
	return checkProgramLocation
}

// errAgentWritableProgram is the default check's refusal.
var errAgentWritableProgram = errors.New("the program is in a folder agents can write to")

// checkProgramLocation is the in-package location check: the resolved program
// must not lie inside the Omnipus data folder, an agent home or a sandbox
// allowed path. Both the written and the symlink-resolved spelling of every
// folder are compared; a folder that exists but cannot be resolved refuses.
func checkProgramLocation(_ string, foundPath string, cfg *config.Config) (string, error) {
	resolved, err := filepath.EvalSymlinks(foundPath)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", foundPath, err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	roots := []string{config.OmnipusHomeDir()}
	if cfg != nil {
		roots = append(roots, agenthome.Resolve(nil, &cfg.Agents.Defaults))
		for i := range cfg.Agents.List {
			roots = append(roots, agenthome.Resolve(&cfg.Agents.List[i], &cfg.Agents.Defaults))
		}
		roots = append(roots, cfg.Sandbox.AllowedPaths...)
	}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs, aerr := filepath.Abs(agenthome.ExpandHome(root))
		if aerr != nil {
			return "", fmt.Errorf("the agent-writable folder %q could not be made absolute: %w", root, aerr)
		}
		spellings := []string{abs}
		if rootResolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
			spellings = append(spellings, rootResolved)
		} else if !errors.Is(rerr, os.ErrNotExist) {
			return "", fmt.Errorf("the agent-writable folder %q could not be examined: %w", abs, rerr)
		}
		for _, sp := range spellings {
			if within(resolved, sp) {
				return "", errAgentWritableProgram
			}
		}
	}
	return resolved, nil
}

func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
