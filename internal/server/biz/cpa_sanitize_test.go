package biz

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeCPAErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "bearer", raw: "request failed: Bearer secret-value", want: "request failed: Bearer [REDACTED]"},
		{name: "authorization", raw: "Authorization: Bearer secret-value", want: "Authorization: Bearer [REDACTED]"},
		{name: "token field", raw: `access_token="secret-value"`, want: `access_token=[REDACTED]`},
		{name: "jwt", raw: "upstream abcdefghij.klmnopqrst.uvwxyzabcd", want: "upstream [REDACTED]"},
		{name: "normal message", raw: "payment_required", want: "payment_required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, sanitizeCPAErrorMessage(tt.raw))
		})
	}

	require.Len(t, sanitizeCPAErrorMessage("x"+string(make([]byte, 600))), maxCPAErrorMessageLength)
}
