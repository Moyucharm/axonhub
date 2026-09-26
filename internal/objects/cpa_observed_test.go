package objects

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCPAQuotaObservedMigratesLegacyWindow(t *testing.T) {
	t.Parallel()
	percent, eventID := 12.5, 42
	legacy := CPAQuotaObserved{SecondaryUsedPercent: &percent, SecondaryLatestEventID: &eventID}
	window, ok := legacy.Window(CPAWeeklyPeriodSeconds)
	require.True(t, ok)
	require.Equal(t, percent, *window.UsedPercent)
	require.Equal(t, eventID, *window.CheckpointEventID)

	fiveHour := CPAQuotaWindowObservation{PeriodSeconds: CPAFiveHourPeriodSeconds}
	migrated := legacy.WithWindow(fiveHour)
	require.Len(t, migrated.ObservedWindows(), 2)
	require.Nil(t, migrated.SecondaryUsedPercent)
	require.Equal(t, percent, *migrated.ObservedWindows()[0].UsedPercent)
	require.Equal(t, CPAFiveHourPeriodSeconds, migrated.ObservedWindows()[1].PeriodSeconds)

	migrated.SecondaryUsedPercent = &percent
	require.Len(t, migrated.ObservedWindows(), 2)
	observedAt := time.Now()
	require.Empty(t, (CPAQuotaObserved{ObservedAt: &observedAt}).ObservedWindows())
}
