package biz

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRefreshProgressLifecycle(t *testing.T) {
	svc := &CPAService{AbstractService: &AbstractService{}, refreshProgress: make(map[int]*cpaRefreshProgressState)}

	_, ok := svc.RefreshProgress(42)
	require.False(t, ok, "unknown instance must report no progress")

	batch := svc.beginRefreshProgress(42, 5, 2)
	progress, ok := svc.RefreshProgress(42)
	require.True(t, ok)
	require.Equal(t, &CPARefreshProgress{Requested: 5, Skipped: 2, Running: true}, progress)

	svc.recordRefreshOutcome(42, batch, cpaQuotaExecutionOutcome{status: cpaQuotaExecutionSuccess})
	svc.recordRefreshOutcome(42, batch, cpaQuotaExecutionOutcome{status: cpaQuotaExecutionFailure})
	svc.recordRefreshOutcome(42, batch, cpaQuotaExecutionOutcome{status: cpaQuotaExecutionSuccess})
	svc.recordRefreshOutcome(42, batch, cpaQuotaExecutionOutcome{status: cpaQuotaExecutionSkipped})
	progress, _ = svc.RefreshProgress(42)
	require.Equal(t, 3, progress.Completed)
	require.Equal(t, 2, progress.Succeeded)
	require.Equal(t, 1, progress.Failed)
	require.True(t, progress.Running)

	svc.finishRefreshProgress(42, batch)
	progress, _ = svc.RefreshProgress(42)
	require.False(t, progress.Running)
	require.Equal(t, 3, progress.Completed)
}

func TestBeginRefreshProgressResetsPreviousRun(t *testing.T) {
	svc := &CPAService{AbstractService: &AbstractService{}, refreshProgress: make(map[int]*cpaRefreshProgressState)}

	firstBatch := svc.beginRefreshProgress(7, 3, 0)
	svc.recordRefreshOutcome(7, firstBatch, cpaQuotaExecutionOutcome{status: cpaQuotaExecutionSuccess})
	svc.finishRefreshProgress(7, firstBatch)

	secondBatch := svc.beginRefreshProgress(7, 9, 1)
	progress, ok := svc.RefreshProgress(7)
	require.True(t, ok)
	require.Equal(t, &CPARefreshProgress{Requested: 9, Skipped: 1, Running: true}, progress)

	// Late results and completion from the stale batch must not affect the new one.
	svc.recordRefreshOutcome(7, firstBatch, cpaQuotaExecutionOutcome{status: cpaQuotaExecutionFailure})
	svc.finishRefreshProgress(7, firstBatch)
	progress, _ = svc.RefreshProgress(7)
	require.Zero(t, progress.Completed)
	require.True(t, progress.Running)

	svc.recordRefreshOutcome(7, secondBatch, cpaQuotaExecutionOutcome{status: cpaQuotaExecutionSuccess})
	svc.finishRefreshProgress(7, secondBatch)
	progress, _ = svc.RefreshProgress(7)
	require.Equal(t, 1, progress.Completed)
	require.Equal(t, 1, progress.Succeeded)
	require.False(t, progress.Running)
}

func TestAccumulateCPARefreshResultUsesOutcomeStatus(t *testing.T) {
	result := &CPARefreshResult{}
	for _, status := range []cpaQuotaExecutionStatus{
		cpaQuotaExecutionSuccess,
		cpaQuotaExecutionFailure,
		cpaQuotaExecutionSkipped,
		"unknown",
	} {
		accumulateCPARefreshResult(result, cpaQuotaExecutionOutcome{status: status})
	}
	require.Equal(t, &CPARefreshResult{Succeeded: 1, Failed: 2, Skipped: 1}, result)
}

func TestRecordRefreshResultWithoutRunningBatchIsNoop(t *testing.T) {
	svc := &CPAService{AbstractService: &AbstractService{}, refreshProgress: make(map[int]*cpaRefreshProgressState)}
	unknownBatch := &cpaRefreshProgressState{}

	svc.recordRefreshOutcome(99, unknownBatch, cpaQuotaExecutionOutcome{status: cpaQuotaExecutionFailure})
	_, ok := svc.RefreshProgress(99)
	require.False(t, ok)
}
