package cpa

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseClaudeResetRejectsWholeBlockOnBadGrant(t *testing.T) {
	valid := map[string]any{"id": "launch", "resets_total": float64(1), "resets_left": float64(1), "usable_now": true}
	status, ok := ParseClaudeReset(map[string]any{
		"eligible": true, "next_grant_id": "launch", "ineligible_reason": "brand_new",
		"grants": []any{valid},
	})
	require.True(t, ok)
	require.Equal(t, "launch", status.NextGrantID)
	require.Equal(t, "unknown", status.IneligibleReason)
	// Missing flags fall to the refusing side.
	require.True(t, status.Grants[0].UseRequiresLimit)

	for name, grants := range map[string][]any{
		"duplicate id":         {valid, valid},
		"left above total":     {map[string]any{"id": "x", "resets_total": float64(1), "resets_left": float64(2)}},
		"bad timestamp":        {map[string]any{"id": "x", "resets_total": float64(1), "resets_left": float64(1), "ends_at": "soon"}},
		"invalid id":           {map[string]any{"id": "Bad ID", "resets_total": float64(1), "resets_left": float64(1)}},
		"fractional count":     {map[string]any{"id": "x", "resets_total": 1.5, "resets_left": float64(1)}},
		"non-bool usable flag": {map[string]any{"id": "x", "resets_total": float64(1), "resets_left": float64(1), "usable_now": "yes"}},
	} {
		_, ok := ParseClaudeReset(map[string]any{"eligible": true, "grants": grants})
		require.False(t, ok, name)
	}
	_, ok = ParseClaudeReset(nil)
	require.False(t, ok)
}
