package subscription

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseBillingResponses_merges_weekly_and_monthly_usage(t *testing.T) {
	// Given
	weekly := []byte(`{
		"config": {
			"currentPeriod": {"type":"WEEKLY","start":"2026-07-09T03:25:00Z","end":"2026-07-16T03:25:00Z"},
			"creditUsagePercent": 82.5,
			"productUsage": [{"product":"Api","usagePercent":82.5}],
			"prepaidBalance": {"val": 12},
			"onDemandCap": {"val": 100},
			"onDemandUsed": {"val": 5},
			"isUnifiedBillingUser": true
		}
	}`)
	monthly := []byte(`{
		"config": {
			"monthlyLimit": {"val": 15000},
			"used": {"val": 7500},
			"billingPeriodStart": "2026-07-01T00:00:00Z",
			"billingPeriodEnd": "2026-08-01T00:00:00Z"
		}
	}`)

	// When
	result, err := ParseBillingResponses(weekly, monthly)

	// Then
	require.NoError(t, err)
	require.Empty(t, result.Plan, "monthly credit limit must not be mapped to a plan name")
	require.Equal(t, 82.5, result.Weekly.UsagePercent)
	require.Equal(t, "2026-07-16T03:25:00Z", result.Weekly.ResetAt)
	require.Equal(t, 50.0, result.Monthly.UsagePercent)
	require.Equal(t, 150.0, result.Monthly.LimitUSD)
	require.Equal(t, 75.0, result.Monthly.UsedUSD)
	require.Equal(t, "2026-08-01T00:00:00Z", result.Monthly.ResetAt)
}

func TestParseBillingResponses_rejects_missing_billing_config(t *testing.T) {
	// When
	_, err := ParseBillingResponses([]byte(`{}`), []byte(`{}`))

	// Then
	require.Error(t, err)
}

func TestParseBillingResponses_preserves_missing_windows(t *testing.T) {
	// Given
	weekly := []byte(`{"config":{}}`)
	monthly := []byte(`{"config":{}}`)

	// When
	result, err := ParseBillingResponses(weekly, monthly)

	// Then
	require.NoError(t, err)
	require.Nil(t, result.Weekly)
	require.Nil(t, result.Monthly)
}
