package cron

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestI1PhysicalBootRestoreRejectsReusedInstance(t *testing.T) {
	for _, first := range []string{"normal_start", "physical_start"} {
		t.Run(first, func(t *testing.T) {
			cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"))
			if first == "normal_start" {
				require.NoError(t, cs.Start())
			} else {
				require.NoError(t, cs.StartAfterPhysicalBoot())
			}
			cs.Stop()
			err := cs.StartAfterPhysicalBoot()
			require.EqualError(t, err, "cron: physical-boot restore requires an unused service instance")
			require.NoError(t, cs.Start(), "normal same-process Start remains available after Stop")
			cs.Stop()
		})
	}
}
