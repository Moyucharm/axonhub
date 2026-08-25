package cpa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestClientUsesManagementAPIOnly(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	paths := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if got := r.Header.Get("Authorization"); got != "Bearer management-key" {
			t.Errorf("unexpected authorization header: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/proxy/v0/management/auth-files":
			w.Header().Set("X-CPA-VERSION", "7.1.0")
			_, _ = w.Write([]byte(`[{"auth_index":"auth-1","name":"codex.json","type":"codex","email":"user@example.com"}]`))
		case "/proxy/v0/management/usage-queue":
			if got := r.URL.Query().Get("count"); got != "20" {
				t.Errorf("unexpected usage queue count: %q", got)
			}
			_, _ = w.Write([]byte(`[{"timestamp":"2026-08-24T12:00:00Z","auth_index":"auth-1","provider":"codex","model":"gpt-5.2","tokens":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}]`))
		case "/proxy/v0/management/api-call":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode api-call payload: %v", err)
			}
			if got := payload["url"]; got != codexUsageURL {
				t.Errorf("unexpected provider URL in CPA request: %v", got)
			}
			if got := payload["auth_index"]; got != "auth-1" {
				t.Errorf("unexpected auth index: %v", got)
			}
			_, _ = w.Write([]byte(`{"status_code":200,"header":{"Content-Type":["application/json"]},"body":"{\"plan_type\":\"plus\"}"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL + "/proxy", ManagementSecret: "management-key"})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.CloseIdleConnections()

	files, build, err := client.ListCredentials(context.Background())
	if err != nil {
		t.Fatalf("list credentials: %v", err)
	}
	if build.Version != "7.1.0" {
		t.Fatalf("unexpected version: %q", build.Version)
	}
	if len(files.Files) != 1 || files.Files[0].AuthIndex != "auth-1" {
		t.Fatalf("unexpected auth files: %#v", files.Files)
	}

	events, err := client.ListUsageQueue(context.Background(), 20)
	if err != nil {
		t.Fatalf("list usage queue: %v", err)
	}
	if len(events) != 1 || events[0].AuthIndex != "auth-1" || events[0].Tokens.TotalTokens != 15 {
		t.Fatalf("unexpected usage events: %#v", events)
	}

	result, err := client.CallProvider(context.Background(), ProviderCall{
		AuthIndex: "auth-1",
		Method:    http.MethodGet,
		URL:       codexUsageURL,
		Headers:   map[string]string{"Authorization": "Bearer $TOKEN$"},
	})
	if err != nil {
		t.Fatalf("call provider through CPA: %v", err)
	}
	if result.StatusCode != http.StatusOK || !strings.Contains(string(result.Body), "plus") {
		t.Fatalf("unexpected provider result: %#v", result)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 3 || paths[0] != "/proxy/v0/management/auth-files" || paths[1] != "/proxy/v0/management/usage-queue" || paths[2] != "/proxy/v0/management/api-call" {
		t.Fatalf("unexpected dialed paths: %#v", paths)
	}
}

func TestClientRejectsCrossHostRedirects(t *testing.T) {
	t.Parallel()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirector.Close()

	client, err := NewClient(Config{BaseURL: redirector.URL, ManagementSecret: "management-key"})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.CloseIdleConnections()
	if _, _, err := client.ListCredentials(context.Background()); err == nil || !strings.Contains(err.Error(), "changed host") {
		t.Fatalf("expected cross-host redirect rejection, got: %v", err)
	}
}

func TestNormalizeBaseURLRejectsUnsafeValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"ftp://example.com",
		"https://user:pass@example.com",
		"https://example.com?secret=value",
		"https://example.com/#fragment",
	} {
		if _, err := NormalizeBaseURL(value); err == nil {
			t.Fatalf("expected URL %q to be rejected", value)
		}
	}

	normalized, err := NormalizeBaseURL("127.0.0.1:8317/v0/management/")
	if err != nil {
		t.Fatalf("normalize URL: %v", err)
	}
	if normalized != "http://127.0.0.1:8317" {
		t.Fatalf("unexpected normalized URL: %q", normalized)
	}
}
