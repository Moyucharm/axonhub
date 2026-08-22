package biz

import (
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

func TestDeriveCPAExpired(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		credential *ent.CPACredential
		want       bool
	}{
		{
			name: "successful quota refresh overrides stale subscription end",
			credential: &ent.CPACredential{
				QuotaState:   string(objects.CPAQuotaStateSuccess),
				QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "2026-06-10T07:20:05+00:00"},
			},
			want: false,
		},
		{
			name:       "empty subscription end and active status",
			credential: &ent.CPACredential{Status: "active"},
			want:       false,
		},
		{
			name: "date-only subscription end passed",
			credential: &ent.CPACredential{
				QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "2026-08-20"},
			},
			want: true,
		},
		{
			name: "date-only subscription end still active through the last day",
			credential: &ent.CPACredential{
				QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "2026-08-21"},
			},
			want: false,
		},
		{
			name: "rfc3339 subscription end passed",
			credential: &ent.CPACredential{
				QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "2026-08-01T00:00:00Z"},
			},
			want: true,
		},
		{
			name: "unix seconds subscription end future",
			credential: &ent.CPACredential{
				QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "2900000000"},
			},
			want: false,
		},
		{
			name:       "invalid subscription end ignored",
			credential: &ent.CPACredential{QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "not-a-date"}},
			want:       false,
		},
		{
			name:       "error status with unauthorized message",
			credential: &ent.CPACredential{Status: "error", StatusMessage: "unauthorized"},
			want:       true,
		},
		{
			name:       "error status with payment required message",
			credential: &ent.CPACredential{Status: "ERROR", StatusMessage: "payment_required"},
			want:       true,
		},
		{
			name:       "error status with forbidden message",
			credential: &ent.CPACredential{Status: "error", StatusMessage: "Forbidden"},
			want:       true,
		},
		{
			name:       "error status with not found message",
			credential: &ent.CPACredential{Status: "error", StatusMessage: "not_found"},
			want:       true,
		},
		{
			name:       "error status with unrelated message",
			credential: &ent.CPACredential{Status: "error", StatusMessage: "transient upstream error"},
			want:       false,
		},
		{
			name:       "active status with unauthorized message",
			credential: &ent.CPACredential{Status: "active", StatusMessage: "unauthorized"},
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveCPAExpired(tt.credential, now); got != tt.want {
				t.Fatalf("deriveCPAExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCPACredentialRecovered(t *testing.T) {
	svc := &CPAService{}
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		credential *ent.CPACredential
		want       bool
	}{
		{
			name: "success state without exhausted windows recovers",
			credential: &ent.CPACredential{
				QuotaState: string(objects.CPAQuotaStateSuccess),
				QuotaData: objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
					{ID: "w1", UsedPercent: floatPtr(50)},
				}},
			},
			want: true,
		},
		{
			name:       "unsupported state counts as usable",
			credential: &ent.CPACredential{QuotaState: string(objects.CPAQuotaStateUnsupported)},
			want:       true,
		},
		{
			name: "exhausted window blocks recovery",
			credential: &ent.CPACredential{
				QuotaState: string(objects.CPAQuotaStateSuccess),
				QuotaData: objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
					{ID: "w1", UsedPercent: floatPtr(100)},
				}},
			},
			want: false,
		},
		{
			// 0/0 quota means a usable credential, never an exhausted one.
			name: "zero limit quota recovers",
			credential: &ent.CPACredential{
				QuotaState: string(objects.CPAQuotaStateSuccess),
				QuotaData: objects.CPAQuotaSnapshot{Items: []objects.CPAQuotaItem{
					{ID: "w1", Limit: floatPtr(0), Remaining: floatPtr(0)},
				}},
			},
			want: true,
		},
		{
			name:       "error state keeps credential disabled",
			credential: &ent.CPACredential{QuotaState: string(objects.CPAQuotaStateError)},
			want:       false,
		},
		{
			name:       "pending state keeps credential disabled",
			credential: &ent.CPACredential{QuotaState: string(objects.CPAQuotaStatePending)},
			want:       false,
		},
		{
			// A successful refresh proves the credential is alive even when the
			// cached JWT subscription date has passed.
			name: "stale subscription end with successful quota recovers",
			credential: &ent.CPACredential{
				QuotaState:   string(objects.CPAQuotaStateSuccess),
				QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "2026-01-01"},
			},
			want: true,
		},
		{
			name: "failed quota with passed subscription end does not recover",
			credential: &ent.CPACredential{
				QuotaState:   string(objects.CPAQuotaStateError),
				QuotaContext: objects.CPAQuotaContext{SubscriptionEnd: "2026-01-01"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := svc.cpaCredentialRecovered(tt.credential, now); got != tt.want {
				t.Fatalf("cpaCredentialRecovered() = %v, want %v", got, tt.want)
			}
		})
	}
}

func floatPtr(value float64) *float64 {
	return &value
}
