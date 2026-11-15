package config

import (
	"runtime"
	"sync"
	"testing"
)

// Oracle: issue #1819. A goroutine an earlier test left running (a steered
// turn building its system prompt calls FormatVersion) must not race the
// next test's edition swap. Run under -race: without the guard the detector
// reports the read in FormatVersion against the write in SetEdition's restore.
func TestEdition_SwapDoesNotRaceALingeringReader_1819(t *testing.T) {
	initial := CurrentEdition()
	stop := make(chan struct{})
	started := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		for {
			select {
			case <-stop:
				return
			default:
				_ = FormatVersion()
				_ = EditionAuthMode()
				_ = KnownEdition()
			}
		}
	}()
	<-started
	for i := 0; i < 20000; i++ {
		restore := SetEdition(EditionHosted)
		runtime.Gosched()
		restore()
	}
	close(stop)
	wg.Wait()
	if got := CurrentEdition(); got != initial {
		t.Fatalf("edition after all swaps restored = %q, want the initial %q", got, initial)
	}
}
