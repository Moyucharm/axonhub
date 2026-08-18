package biz

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/objects"
)

func TestNormalizeAPIKeyPoolSettingsRetryCount(t *testing.T) {
	tests := []struct {
		name       string
		retryCount int
		wantError  bool
	}{
		{name: "negative total request count", retryCount: -1, wantError: true},
		{name: "zero total request count", retryCount: 0, wantError: true},
		{name: "one total request", retryCount: 1},
		{name: "three total requests", retryCount: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &objects.ChannelSettings{APIKeyPool: &objects.APIKeyPoolSettings{RetryCount: &tt.retryCount}}

			err := NormalizeAPIKeyPoolSettings(settings)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
