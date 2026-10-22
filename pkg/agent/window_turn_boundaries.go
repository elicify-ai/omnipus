package agent

// wholeTurnStarts preserves the archive's stable turn ownership when retirement
// removes a user input from the model view. A settled effect must not be merged
// into an unrelated older turn merely because its original demand is excluded.
func (p *windowCheckpoint) wholeTurnStarts() []int {
	starts := map[int]bool{}
	for _, i := range parseTurnBoundaries(p.messages) {
		starts[i] = true
	}
	prior := 0
	for i, line := range p.lines {
		slot, found := p.snapshot.Slot(line)
		if !found || slot.UserTurn == 0 {
			continue
		}
		if prior != 0 && slot.UserTurn != prior {
			starts[i] = true
		}
		prior = slot.UserTurn
	}
	out := make([]int, 0, len(starts))
	for i := range p.messages {
		if starts[i] {
			out = append(out, i)
		}
	}
	return out
}
