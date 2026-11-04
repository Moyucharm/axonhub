package cpa

import (
	"context"
	"net/http"
	"testing"

	"github.com/looplj/axonhub/internal/objects"
)

// Keep the quota request identity aligned with the official CPA management UI.
// Its constants.ts requires Hub >= 2.23.0 for newer Cloud Code models.
func TestAntigravityQuotaHubRequest(t *testing.T) {
	t.Parallel()
	const body = `{"groups":[{"displayName":"Gemini Models","buckets":[{"bucketId":"gemini-5h","window":"5h","remainingFraction":0.82,"resetTime":"2030-01-01T00:00:00Z"},{"bucketId":"gemini-weekly","window":"weekly","remainingFraction":0}]},{"displayName":"Claude and GPT models","buckets":[{"bucketId":"3p-5h","remainingFraction":1,"window":"5h"},{"bucketId":"3p-weekly","remainingFraction":0.64,"window":"weekly"}]}]}`
	calls := 0
	client := stubManagementClient{call: func(_ context.Context, call ProviderCall) (*ProviderCallResult, error) {
		calls++
		if got := call.Headers["User-Agent"]; got != "antigravity/hub/2.23.0 darwin/arm64" {
			t.Errorf("User-Agent = %q, want official CPA Hub quota identity", got)
		}
		if call.Headers["Authorization"] != "Bearer $TOKEN$" {
			t.Error("quota must use CPA token substitution")
		}
		if call.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", call.Method)
		}
		if call.URL == antigravitySubscriptionURL {
			return &ProviderCallResult{StatusCode: http.StatusOK, Body: []byte(`{}`)}, nil
		}
		if call.Body != `{"project":"project"}` {
			t.Errorf("unexpected quota body: %s", call.Body)
		}
		return &ProviderCallResult{StatusCode: http.StatusOK, Body: []byte(body)}, nil
	}}
	result, err := (antigravityQuotaAdapter{}).Fetch(context.Background(), client, CredentialInput{AuthIndex: "ag", ProjectID: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want quota and subscription", calls)
	}
	if result.State != objects.CPAQuotaStateSuccess || len(result.Snapshot.Items) != 4 {
		t.Fatalf("expected both Gemini and Claude/GPT windows, got %+v", result)
	}
	for i, want := range []float64{82, 0, 100, 64} {
		item := result.Snapshot.Items[i]
		if item.RemainingPercent == nil || *item.RemainingPercent != want {
			t.Fatalf("item %s remaining = %v, want %v", item.ID, item.RemainingPercent, want)
		}
		group := "Gemini Models"
		if i >= 2 {
			group = "Claude and GPT models"
		}
		if item.Group != group {
			t.Fatalf("item %s group = %s, want %s", item.ID, item.Group, group)
		}
		period := 18000
		if i%2 == 1 {
			period = 604800
		}
		if item.PeriodSeconds == nil || *item.PeriodSeconds != period {
			t.Fatalf("item %s period = %v, want %d", item.ID, item.PeriodSeconds, period)
		}
	}
	if result.Snapshot.Items[0].ResetAt == nil {
		t.Fatal("Gemini reset time missing")
	}
}
