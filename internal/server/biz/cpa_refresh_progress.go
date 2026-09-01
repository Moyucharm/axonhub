package biz

// CPARefreshProgress is an in-memory snapshot of one manual credential refresh.
// It is only maintained for manual RefreshInstance calls; scheduled patrols do
// not record progress. The snapshot lives until the next manual refresh of the
// same instance or until the process restarts.
type CPARefreshProgress struct {
	Requested int
	Completed int
	Succeeded int
	Failed    int
	Skipped   int
	Running   bool
}

type cpaRefreshProgressState struct {
	progress CPARefreshProgress
}

// beginRefreshProgress resets and starts progress tracking for the instance.
// The returned state is the batch identity used to reject late callbacks from
// an older concurrent refresh of the same instance.
func (svc *CPAService) beginRefreshProgress(instanceID, requested, skipped int) *cpaRefreshProgressState {
	svc.refreshProgressMu.Lock()
	defer svc.refreshProgressMu.Unlock()
	state := &cpaRefreshProgressState{
		progress: CPARefreshProgress{
			Requested: requested,
			Skipped:   skipped,
			Running:   true,
		},
	}
	svc.refreshProgress[instanceID] = state
	return state
}

// recordRefreshOutcome counts one finished credential refresh when batch is
// still the current manual refresh for the instance.
func (svc *CPAService) recordRefreshOutcome(instanceID int, batch *cpaRefreshProgressState, outcome cpaQuotaExecutionOutcome) {
	svc.refreshProgressMu.Lock()
	defer svc.refreshProgressMu.Unlock()
	state, ok := svc.refreshProgress[instanceID]
	if !ok || state != batch || !state.progress.Running {
		return
	}
	switch outcome.status {
	case cpaQuotaExecutionSkipped:
		return
	case cpaQuotaExecutionSuccess:
		state.progress.Completed++
		state.progress.Succeeded++
	case cpaQuotaExecutionFailure:
		state.progress.Completed++
		state.progress.Failed++
	default:
		state.progress.Completed++
		state.progress.Failed++
	}
}

// finishRefreshProgress marks the batch as no longer running when it is still
// the current manual refresh for the instance.
func (svc *CPAService) finishRefreshProgress(instanceID int, batch *cpaRefreshProgressState) {
	svc.refreshProgressMu.Lock()
	defer svc.refreshProgressMu.Unlock()
	if state, ok := svc.refreshProgress[instanceID]; ok && state == batch {
		state.progress.Running = false
	}
}

// RefreshProgress returns the current progress snapshot for the instance, or
// nil when no manual refresh has been recorded for it in this process. It is
// polled by the frontend during a refresh, so it intentionally avoids any
// database access.
func (svc *CPAService) RefreshProgress(instanceID int) (*CPARefreshProgress, bool) {
	svc.refreshProgressMu.Lock()
	defer svc.refreshProgressMu.Unlock()
	state, ok := svc.refreshProgress[instanceID]
	if !ok {
		return nil, false
	}
	snapshot := state.progress
	return &snapshot, true
}
