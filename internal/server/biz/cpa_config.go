package biz

import (
	"fmt"
	"strings"

	"github.com/looplj/axonhub/internal/ent"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

type cpaInstanceConfig struct {
	name                   string
	baseURL                string
	managementSecret       string
	enabled                bool
	insecureSkipTLS        bool
	autoRefreshEnabled     bool
	refreshIntervalMinutes int
	autoManageEnabled      bool
	autoResetEnabled       bool
	usageStreamEnabled     bool
	enabledPatrolInterval  int
	disabledPatrolInterval int
}

func normalizeCreateCPAInstanceConfig(input CreateCPAInstanceInput) (cpaInstanceConfig, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return cpaInstanceConfig{}, fmt.Errorf("CPA instance name is required")
	}
	baseURL, err := cpaclient.NormalizeBaseURL(input.BaseURL)
	if err != nil {
		return cpaInstanceConfig{}, err
	}
	secret := strings.TrimSpace(input.ManagementSecret)
	if secret == "" {
		return cpaInstanceConfig{}, fmt.Errorf("CPA management secret is required")
	}
	refreshInterval, err := normalizeCPARefreshInterval(input.RefreshIntervalMinutes)
	if err != nil {
		return cpaInstanceConfig{}, err
	}
	enabledPatrolInterval, err := normalizeCPAEnabledPatrolInterval(input.EnabledPatrolIntervalMinutes)
	if err != nil {
		return cpaInstanceConfig{}, err
	}
	disabledPatrolInterval, err := normalizeCPADisabledPatrolInterval(input.DisabledPatrolIntervalMinutes)
	if err != nil {
		return cpaInstanceConfig{}, err
	}

	// Reset-card auto use runs inside the credential patrol, so it can only be
	// enabled together with the patrol that refreshes quota and reset credits.
	autoManageEnabled := boolOrDefault(input.AutoManageEnabled, false)
	return cpaInstanceConfig{
		name:                   name,
		baseURL:                baseURL,
		managementSecret:       secret,
		enabled:                boolOrDefault(input.Enabled, true),
		insecureSkipTLS:        input.InsecureSkipTLS,
		autoRefreshEnabled:     boolOrDefault(input.AutoRefreshEnabled, true),
		refreshIntervalMinutes: refreshInterval,
		autoManageEnabled:      autoManageEnabled,
		autoResetEnabled:       autoManageEnabled && boolOrDefault(input.AutoResetEnabled, false),
		usageStreamEnabled:     boolOrDefault(input.UsageStreamEnabled, false),
		enabledPatrolInterval:  enabledPatrolInterval,
		disabledPatrolInterval: disabledPatrolInterval,
	}, nil
}

func mergeUpdateCPAInstanceConfig(current *ent.CPAInstance, input UpdateCPAInstanceInput) (cpaInstanceConfig, bool, bool, error) {
	config := cpaInstanceConfig{
		name:                   current.Name,
		baseURL:                current.BaseURL,
		enabled:                current.Enabled,
		insecureSkipTLS:        current.InsecureSkipTLS,
		autoRefreshEnabled:     current.AutoRefreshEnabled,
		refreshIntervalMinutes: current.RefreshIntervalMinutes,
		autoManageEnabled:      current.AutoManageEnabled,
		autoResetEnabled:       current.AutoResetEnabled,
		usageStreamEnabled:     current.UsageStreamEnabled,
		enabledPatrolInterval:  current.EnabledPatrolIntervalMinutes,
		disabledPatrolInterval: current.DisabledPatrolIntervalMinutes,
	}
	if input.Name != nil {
		config.name = strings.TrimSpace(*input.Name)
		if config.name == "" {
			return cpaInstanceConfig{}, false, false, fmt.Errorf("CPA instance name is required")
		}
	}
	var err error
	if input.BaseURL != nil {
		config.baseURL, err = cpaclient.NormalizeBaseURL(*input.BaseURL)
		if err != nil {
			return cpaInstanceConfig{}, false, false, err
		}
	}
	if input.Enabled != nil {
		config.enabled = *input.Enabled
	}
	if input.InsecureSkipTLS != nil {
		config.insecureSkipTLS = *input.InsecureSkipTLS
	}
	if input.AutoRefreshEnabled != nil {
		config.autoRefreshEnabled = *input.AutoRefreshEnabled
	}
	if input.RefreshIntervalMinutes != nil {
		config.refreshIntervalMinutes, err = normalizeCPARefreshInterval(input.RefreshIntervalMinutes)
		if err != nil {
			return cpaInstanceConfig{}, false, false, err
		}
	}
	if input.AutoManageEnabled != nil {
		config.autoManageEnabled = *input.AutoManageEnabled
	}
	if input.AutoResetEnabled != nil {
		config.autoResetEnabled = *input.AutoResetEnabled
	}
	if input.UsageStreamEnabled != nil {
		config.usageStreamEnabled = *input.UsageStreamEnabled
	}
	if input.EnabledPatrolIntervalMinutes != nil {
		config.enabledPatrolInterval, err = normalizeCPAEnabledPatrolInterval(input.EnabledPatrolIntervalMinutes)
		if err != nil {
			return cpaInstanceConfig{}, false, false, err
		}
	}
	if input.DisabledPatrolIntervalMinutes != nil {
		config.disabledPatrolInterval, err = normalizeCPADisabledPatrolInterval(input.DisabledPatrolIntervalMinutes)
		if err != nil {
			return cpaInstanceConfig{}, false, false, err
		}
	}

	secretChanged := input.ManagementSecret != nil && strings.TrimSpace(*input.ManagementSecret) != ""
	if secretChanged {
		config.managementSecret = strings.TrimSpace(*input.ManagementSecret)
	}
	baseURLChanged := config.baseURL != current.BaseURL
	if baseURLChanged && !secretChanged {
		return cpaInstanceConfig{}, false, false, fmt.Errorf("CPA management secret is required when changing the base URL")
	}
	if !config.autoManageEnabled {
		config.autoResetEnabled = false
	}
	connectionChanged := baseURLChanged || config.insecureSkipTLS != current.InsecureSkipTLS || secretChanged || (!current.Enabled && config.enabled)
	return config, secretChanged, connectionChanged, nil
}

func shouldRotateCPAUsageCollector(current *ent.CPAInstance, config cpaInstanceConfig, connectionChanged bool) bool {
	return current == nil || connectionChanged || current.UsageStreamEnabled != config.usageStreamEnabled
}

func boolOrDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}
