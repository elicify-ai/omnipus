//go:build linux

package sandbox

import "testing"

func TestWU23R1_RuntimeAllowRootsNeverReopenInventory_Landlock(t *testing.T) {
	if wu23LandlockChild() {
		return
	}
	wu23R1RuntimeRootsPreserveInventory(t)
}
