package biz

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

func TestDecideCPAAutomation(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	used := 100.0
	reset := now.Add(time.Hour)

	tests := []struct {
		name       string
		credential *ent.CPACredential
		action     cpaAutomationAction
		reason     string
	}{
		{
			name: "enabled exhausted credential is disabled",
			credential: &ent.CPACredential{QuotaData: objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{{
				ID: "weekly", UsedPercent: &used, ResetAt: &reset,
			}}}},
			action: cpaAutomationDisable,
			reason: "quota exhausted",
		},
		{
			name:       "disabled recovered credential is enabled",
			credential: &ent.CPACredential{Disabled: true, QuotaState: string(objects.CPAQuotaStateSuccess)},
			action:     cpaAutomationEnable,
			reason:     "quota recovered",
		},
		{
			name:       "disabled failed credential remains unchanged",
			credential: &ent.CPACredential{Disabled: true, QuotaState: string(objects.CPAQuotaStateError)},
			action:     cpaAutomationNone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := decideCPAAutomation(tt.credential, now)
			require.Equal(t, tt.action, decision.action)
			require.Equal(t, tt.reason, decision.reason)
		})
	}
}

func TestCPACredentialDisableReason(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	used := 100.0
	reset := now.Add(time.Hour)

	reason, item := cpaCredentialDisableReason(&ent.CPACredential{
		QuotaData: objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{{ID: "weekly", UsedPercent: &used, ResetAt: &reset}}},
	}, now)
	require.Equal(t, "quota exhausted", reason)
	require.NotNil(t, item)
	require.Equal(t, "weekly", item.ID)

	reason, item = cpaCredentialDisableReason(&ent.CPACredential{
		Status:        "error",
		StatusMessage: "unauthorized",
	}, now)
	require.Equal(t, "expired", reason)
	require.Nil(t, item)

	reason, item = cpaCredentialDisableReason(&ent.CPACredential{
		QuotaData: objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{{ID: "weekly", UsedPercent: floatPtr(50)}}},
	}, now)
	require.Empty(t, reason)
	require.Nil(t, item)
}
