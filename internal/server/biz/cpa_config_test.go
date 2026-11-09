package biz

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
)

func TestNormalizeCreateCPAInstanceConfig(t *testing.T) {
	t.Parallel()

	config, err := normalizeCreateCPAInstanceConfig(CreateCPAInstanceInput{
		Name:             "  Primary CPA  ",
		BaseURL:          "http://127.0.0.1:8317/",
		ManagementSecret: "  secret  ",
	})
	require.NoError(t, err)
	require.Equal(t, cpaInstanceConfig{
		name:                   "Primary CPA",
		baseURL:                "http://127.0.0.1:8317",
		managementSecret:       "secret",
		enabled:                true,
		insecureSkipTLS:        false,
		autoRefreshEnabled:     true,
		refreshIntervalMinutes: defaultCPARefreshIntervalMinutes,
		autoManageEnabled:      false,
		usageStreamEnabled:     false,
		enabledPatrolInterval:  defaultCPAEnabledPatrolMinutes,
		disabledPatrolInterval: defaultCPADisabledPatrolMinutes,
	}, config)
}

func TestNormalizeCreateCPAInstanceConfigRejectsMissingSecret(t *testing.T) {
	t.Parallel()

	_, err := normalizeCreateCPAInstanceConfig(CreateCPAInstanceInput{
		Name:    "Primary CPA",
		BaseURL: "http://127.0.0.1:8317",
	})
	require.EqualError(t, err, "CPA management secret is required")
}

func TestMergeUpdateCPAInstanceConfigPreservesSecretWhenEmpty(t *testing.T) {
	t.Parallel()

	current := &ent.CPAInstance{
		Name:                          "Primary CPA",
		BaseURL:                       "http://127.0.0.1:8317",
		Enabled:                       true,
		AutoRefreshEnabled:            true,
		RefreshIntervalMinutes:        5,
		EnabledPatrolIntervalMinutes:  5,
		DisabledPatrolIntervalMinutes: 480,
	}
	config, secretChanged, connectionChanged, err := mergeUpdateCPAInstanceConfig(current, UpdateCPAInstanceInput{
		ManagementSecret: new(string),
	})
	require.NoError(t, err)
	require.False(t, secretChanged)
	require.False(t, connectionChanged)
	require.Equal(t, current.BaseURL, config.baseURL)
	require.Empty(t, config.managementSecret)
}

func TestShouldRotateCPAUsageCollector(t *testing.T) {
	t.Parallel()

	current := &ent.CPAInstance{Enabled: true, UsageStreamEnabled: true}
	config := cpaInstanceConfig{usageStreamEnabled: true}
	require.False(t, shouldRotateCPAUsageCollector(current, config, false))
	require.True(t, shouldRotateCPAUsageCollector(current, config, true))

	config.usageStreamEnabled = false
	require.True(t, shouldRotateCPAUsageCollector(current, config, false))

	config.usageStreamEnabled = true
	current.UsageStreamEnabled = false
	require.True(t, shouldRotateCPAUsageCollector(current, config, false))
	require.True(t, shouldRotateCPAUsageCollector(nil, config, false))
}

func TestMergeUpdateCPAInstanceConfigRequiresSecretForURLChange(t *testing.T) {
	t.Parallel()

	current := &ent.CPAInstance{
		Name:                          "Primary CPA",
		BaseURL:                       "http://127.0.0.1:8317",
		Enabled:                       true,
		AutoRefreshEnabled:            true,
		RefreshIntervalMinutes:        5,
		EnabledPatrolIntervalMinutes:  5,
		DisabledPatrolIntervalMinutes: 480,
	}
	newURL := "http://127.0.0.1:8318"
	_, _, _, err := mergeUpdateCPAInstanceConfig(current, UpdateCPAInstanceInput{BaseURL: &newURL})
	require.EqualError(t, err, "CPA management secret is required when changing the base URL")
}

func TestCPAInstanceConfigAutomaticWritesDependOnRefresh(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		for _, manage := range []bool{false, true} {
			for _, reset := range []bool{false, true} {
				config, err := normalizeCreateCPAInstanceConfig(CreateCPAInstanceInput{
					Name: "dependent", BaseURL: "http://127.0.0.1:8317", ManagementSecret: "secret",
					AutoRefreshEnabled: &refresh, AutoManageEnabled: &manage, AutoResetEnabled: &reset,
				})
				require.NoError(t, err)
				require.Equal(t, refresh && manage, config.autoManageEnabled)
				require.Equal(t, refresh && reset, config.autoResetEnabled)
				current := &ent.CPAInstance{Name: "dependent", BaseURL: "http://127.0.0.1:8319",
					Enabled: true, AutoRefreshEnabled: refresh, AutoManageEnabled: manage, AutoResetEnabled: reset}
				merged, _, _, err := mergeUpdateCPAInstanceConfig(current, UpdateCPAInstanceInput{})
				require.NoError(t, err)
				require.Equal(t, refresh && manage, merged.autoManageEnabled)
				require.Equal(t, refresh && reset, merged.autoResetEnabled)
			}
		}
	}
	current := &ent.CPAInstance{Name: "dependent", BaseURL: "http://127.0.0.1:8319", Enabled: true,
		AutoRefreshEnabled: true, AutoManageEnabled: true, AutoResetEnabled: true}
	merged, _, _, err := mergeUpdateCPAInstanceConfig(current, UpdateCPAInstanceInput{AutoManageEnabled: new(bool)})
	require.NoError(t, err)
	require.False(t, merged.autoManageEnabled)
	require.True(t, merged.autoResetEnabled)
	merged, _, _, err = mergeUpdateCPAInstanceConfig(current, UpdateCPAInstanceInput{AutoRefreshEnabled: new(bool)})
	require.NoError(t, err)
	require.False(t, merged.autoManageEnabled)
	require.False(t, merged.autoResetEnabled)
}

func TestCPAEnablingRefreshDoesNotRestoreLegacyWritePermissions(t *testing.T) {
	current := &ent.CPAInstance{Name: "legacy", BaseURL: "http://127.0.0.1:8319", Enabled: true,
		AutoRefreshEnabled: false, AutoManageEnabled: true, AutoResetEnabled: true}
	on := true
	config, _, _, err := mergeUpdateCPAInstanceConfig(current, UpdateCPAInstanceInput{AutoRefreshEnabled: &on})
	require.NoError(t, err)
	require.True(t, config.autoRefreshEnabled)
	require.False(t, config.autoManageEnabled)
	require.False(t, config.autoResetEnabled)
	config, _, _, err = mergeUpdateCPAInstanceConfig(current, UpdateCPAInstanceInput{
		AutoRefreshEnabled: &on, AutoResetEnabled: &on,
	})
	require.NoError(t, err)
	require.False(t, config.autoManageEnabled)
	require.True(t, config.autoResetEnabled, "explicit reset permission is independent of management")
}
