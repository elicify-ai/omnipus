package session

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// roomProjection caches only content-free source facts for one bounded read.
type roomProjection struct {
	base    string
	scopes  map[RoomScopeRef][]RoomRequestRef
	retired map[RoomRequestRef]bool
}

func newRoomProjection(base string) *roomProjection {
	return &roomProjection{base: base, scopes: map[RoomScopeRef][]RoomRequestRef{}, retired: map[RoomRequestRef]bool{}}
}

func (p *roomProjection) requestRetired(ref RoomRequestRef) (bool, error) {
	if value, ok := p.retired[ref]; ok {
		return value, nil
	}
	value, err := RoomRequestRetired(p.base, ref)
	if err == nil {
		p.retired[ref] = value
	}
	return value, err
}

func (p *roomProjection) apply(slot ModelSlot) (ModelSlot, error) {
	source := slot.Source
	if source.RoomRequest != nil {
		retired, err := p.requestRetired(*source.RoomRequest)
		if err != nil {
			return slot, err
		}
		if retired {
			slot.OmitFromContext = true
			return slot, nil
		}
	}
	if source.RoomScope == nil {
		return slot, nil
	}
	refs, ok := p.scopes[*source.RoomScope]
	if !ok {
		var err error
		refs, err = ReadRoomRequestScope(p.base, source.RoomScope)
		if err != nil {
			return slot, fmt.Errorf("context_repair_required: derived room scope cannot be read: %w", err)
		}
		p.scopes[*source.RoomScope] = refs
	}
	retired := false
	for _, ref := range refs {
		value, err := p.requestRetired(ref)
		if err != nil {
			return slot, err
		}
		retired = retired || value
	}
	if !retired {
		return slot, nil
	}
	switch slot.Message.Role {
	case "tool":
		// Exact completed facts and engine recovery observations remain addressable.
	case "assistant":
		if len(slot.Message.ToolCalls) == 0 {
			slot.OmitFromContext = true
			break
		}
		slot.Message = providers.Message{Role: "assistant", ToolCalls: slot.Message.ToolCalls}
	default:
		slot.OmitFromContext = true
	}
	return slot, nil
}
