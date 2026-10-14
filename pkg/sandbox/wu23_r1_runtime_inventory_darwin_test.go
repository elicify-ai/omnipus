//go:build darwin

package sandbox

import "testing"

func TestWU23R1_RuntimeAllowRootsNeverReopenInventory_Seatbelt(t *testing.T) {
	wu23R1RuntimeRootsPreserveInventory(t)
}
