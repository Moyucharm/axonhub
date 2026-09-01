package biz

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

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
