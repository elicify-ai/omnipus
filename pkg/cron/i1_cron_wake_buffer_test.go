package cron

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A next-fire update may finish before runLoop reaches its select. Retain one
// notification, coalescing repeated updates rather than losing the first wake.
func TestI1CronWakeRetainedBeforeLoopWait(t *testing.T) {
	for _, entry := range []string{"constructor", "run_initialization"} {
		t.Run(entry, func(t *testing.T) {
			cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"))
			if entry == "run_initialization" {
				cs.wakeChan = nil
				require.NoError(t, cs.startNoLoop())
				t.Cleanup(cs.Stop)
			}
			cs.notify()
			cs.notify()
			select {
			case <-cs.wakeChan:
			default:
				t.Fatal("next-fire wake was lost before the loop could wait")
			}
			select {
			case <-cs.wakeChan:
				t.Fatal("repeated next-fire updates were not coalesced")
			default:
			}
		})
	}
}
