package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

var (
	ErrInvalidRevision       = errors.New("workspace: invalid revision")
	ErrRevisionConflict      = errors.New("workspace: revision conflict")
	ErrDelegationUnreadable  = errors.New("workspace: delegation state is unreadable")
	workspaceRevisionPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type State struct {
	Workspace  Workspace
	Delegation []DelegationEdge
	Revision   string
}

func ValidateRevision(revision string) error {
	if !workspaceRevisionPattern.MatchString(revision) {
		return fmt.Errorf("%w: expected 64 lowercase hex characters", ErrInvalidRevision)
	}
	return nil
}

func RevisionForState(w Workspace, edges []DelegationEdge) (string, error) {
	w.UpdatedAt = ""
	canonical, err := json.Marshal(struct {
		Workspace  Workspace        `json:"workspace"`
		Delegation []DelegationEdge `json:"delegation"`
	}{w, edges})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// ReadStateLocked reads the authoritative record and graph while the caller
// holds LockID(id). It never reacquires the non-reentrant workspace lock.
func ReadStateLocked(home, id string) (State, error) {
	w, err := loadWorkspaceRecord(home, id)
	if err != nil {
		return State{}, err
	}
	edges, ok := LoadDelegation(home, id)
	if !ok {
		return State{}, ErrDelegationUnreadable
	}
	revision, err := RevisionForState(w, edges)
	return State{Workspace: w, Delegation: edges, Revision: revision}, err
}

func ReadState(home, id string) (State, error) {
	unlock := LockID(id)
	defer unlock()
	return ReadStateLocked(home, id)
}

func CheckRevisionLocked(home, id, expected string) (State, error) {
	if err := ValidateRevision(expected); err != nil {
		return State{}, err
	}
	state, err := ReadStateLocked(home, id)
	if err != nil {
		return State{}, err
	}
	if state.Revision != expected {
		return state, fmt.Errorf("%w: current revision %s", ErrRevisionConflict, state.Revision)
	}
	return state, nil
}
