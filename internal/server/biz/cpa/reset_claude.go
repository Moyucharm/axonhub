package cpa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/objects"
)

// Claude reset grants ("cedar_ember") follow the contract used by Claude Code
// and the CPA management center: read the block from oauth/usage, then claim
// one use through the organization's reset_rate_limits endpoint.
const (
	claudeAPIOrigin         = "https://api.anthropic.com"
	claudeResetProgram      = "cedar_ember"
	claudeResetStatusURL    = claudeAPIOrigin + "/api/oauth/usage?cedar_ember=1&skip_spend=1"
	claudeResetClaimURLTmpl = claudeAPIOrigin + "/api/organizations/%s/reset_rate_limits"
	// claudeCLIUserAgent marks requests as coming from the Claude Code CLI.
	// Without it the provider reports the account ineligible ("surface") and
	// lists no grants.
	claudeCLIUserAgent = "claude-cli/2.1.280 (external, cli)"
)

var (
	claudeResetGrantIDPattern   = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)
	claudeResetRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	claudeOrganizationPattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	claudeResetControlChars     = regexp.MustCompile(`[\x00-\x1f\x7f-\x9f]`)
)

// claudeResetWindows are the usage windows a grant can clear; others are dropped.
var claudeResetWindows = []string{"five_hour", "seven_day", "seven_day_overage_included"}

var claudeIneligibleReasons = map[string]bool{
	"config_off": true, "tier": true, "seat": true, "mobile": true, "surface": true,
	"cli_version": true, "no_grant": true, "tenure": true, "other_experiment": true,
	"unavailable": true, "unknown": true,
}

var claudeResetResults = map[string]objects.CPAClaudeResetResult{
	"reset":        objects.CPAClaudeResetResultReset,
	"already_used": objects.CPAClaudeResetResultAlreadyUsed,
	"not_limited":  objects.CPAClaudeResetResultNotLimited,
	"cooldown":     objects.CPAClaudeResetResultCooldown,
	"ineligible":   objects.CPAClaudeResetResultIneligible,
	"unavailable":  objects.CPAClaudeResetResultUnavailable,
}

// ErrClaudeResetOutcomeUnknown means the claim was or may have been sent and
// no settled answer came back, so the grant use may have been spent.
var ErrClaudeResetOutcomeUnknown = errors.New("Claude reset claim outcome is unknown")

func claudeRequestHeaders() map[string]string {
	return map[string]string{
		"Authorization":  "Bearer $TOKEN$",
		"Content-Type":   "application/json",
		"anthropic-beta": "oauth-2025-04-20",
		"User-Agent":     claudeCLIUserAgent,
	}
}

// ParseClaudeReset parses the cedar_ember block. It returns false for a missing
// or malformed block; one malformed grant, a duplicate ID, or a bad timestamp
// rejects the whole block. Missing usability flags default to the refusing side.
func ParseClaudeReset(raw any) (*objects.CPAClaudeReset, bool) {
	block, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	eligible, ok := block["eligible"].(bool)
	if !ok {
		return nil, false
	}
	status := &objects.CPAClaudeReset{Eligible: eligible, Grants: []objects.CPAClaudeResetGrant{}}
	switch reason := block["ineligible_reason"].(type) {
	case nil:
	case string:
		status.IneligibleReason = reason
		if !claudeIneligibleReasons[reason] {
			status.IneligibleReason = "unknown"
		}
	default:
		return nil, false
	}
	if status.AtLimit, ok = optionalBool(block["at_limit"], false); !ok {
		return nil, false
	}
	if status.WeeklyResetsAt, ok = optionalTimestamp(block["weekly_resets_at"]); !ok {
		return nil, false
	}
	if status.CooldownUntil, ok = optionalTimestamp(block["cooldown_until"]); !ok {
		return nil, false
	}
	var rawGrants []any
	switch grants := block["grants"].(type) {
	case nil:
	case []any:
		rawGrants = grants
	default:
		return nil, false
	}
	seen := make(map[string]bool, len(rawGrants))
	for _, rawGrant := range rawGrants {
		grant, ok := parseClaudeResetGrant(rawGrant)
		if !ok || seen[grant.ID] {
			return nil, false
		}
		seen[grant.ID] = true
		status.Grants = append(status.Grants, grant)
	}
	if next, ok := block["next_grant_id"].(string); ok && seen[next] {
		status.NextGrantID = next
	}
	return status, true
}

func parseClaudeResetGrant(raw any) (objects.CPAClaudeResetGrant, bool) {
	record, ok := raw.(map[string]any)
	if !ok {
		return objects.CPAClaudeResetGrant{}, false
	}
	id, _ := record["id"].(string)
	total, totalOK := countValue(record["resets_total"])
	left, leftOK := countValue(record["resets_left"])
	if !claudeResetGrantIDPattern.MatchString(id) || !totalOK || !leftOK || left > total {
		return objects.CPAClaudeResetGrant{}, false
	}
	grant := objects.CPAClaudeResetGrant{ID: id, Label: safeClaudeLabel(record["label"]), ResetsTotal: total, ResetsLeft: left}
	var valid [5]bool
	grant.StartsAt, valid[0] = optionalTimestamp(record["starts_at"])
	grant.EndsAt, valid[1] = optionalTimestamp(record["ends_at"])
	grant.Paused, valid[2] = optionalBool(record["paused"], false)
	grant.UsableNow, valid[3] = optionalBool(record["usable_now"], false)
	grant.UseRequiresLimit, valid[4] = optionalBool(record["use_requires_limit"], true)
	for _, fieldOK := range valid {
		if !fieldOK {
			return objects.CPAClaudeResetGrant{}, false
		}
	}
	switch clears := record["clears"].(type) {
	case nil:
	case []any:
		for _, window := range claudeResetWindows {
			for _, value := range clears {
				if value == window {
					grant.Clears = append(grant.Clears, window)
					break
				}
			}
		}
	default:
		return objects.CPAClaudeResetGrant{}, false
	}
	return grant, true
}

func countValue(value any) (int, bool) {
	number, ok := value.(float64)
	if !ok || number < 0 || number != math.Trunc(number) || number > math.MaxInt32 {
		return 0, false
	}
	return int(number), true
}

func optionalBool(value any, fallback bool) (bool, bool) {
	if value == nil {
		return fallback, true
	}
	typed, ok := value.(bool)
	return typed, ok
}

func optionalTimestamp(value any) (*time.Time, bool) {
	if value == nil {
		return nil, true
	}
	text, ok := value.(string)
	if !ok {
		return nil, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return nil, false
	}
	parsed = parsed.UTC()
	return &parsed, true
}

// safeClaudeLabel strips untrusted control characters and bounds display text.
func safeClaudeLabel(value any) string {
	text, _ := value.(string)
	text = strings.Join(strings.Fields(claudeResetControlChars.ReplaceAllString(text, " ")), " ")
	if runes := []rune(text); len(runes) > 120 {
		text = string(runes[:120])
	}
	return text
}

// ClaudeResetBlocker explains why a grant cannot be claimed now, or returns ""
// when it can.
func ClaudeResetBlocker(status *objects.CPAClaudeReset, grantID string, now time.Time) string {
	if status == nil || !status.Eligible {
		return "ineligible"
	}
	if status.CooldownUntil != nil && status.CooldownUntil.After(now) {
		return "cooldown"
	}
	var grant *objects.CPAClaudeResetGrant
	for index := range status.Grants {
		if status.Grants[index].ID == grantID {
			grant = &status.Grants[index]
			break
		}
	}
	switch {
	case grant == nil:
		return "unknown_grant"
	case grant.Paused:
		return "paused"
	case !grant.UsableNow:
		return "not_usable"
	case grant.ResetsLeft <= 0:
		return "exhausted"
	case grant.StartsAt != nil && grant.StartsAt.After(now):
		return "not_started"
	case grant.EndsAt != nil && !grant.EndsAt.After(now):
		return "expired"
	case grant.UseRequiresLimit && !status.AtLimit:
		return "not_limited"
	}
	return ""
}

// SelectClaudeResetGrant returns the grant a claim should use: the provider's
// next_grant_id when it is claimable, otherwise the first claimable grant by ID.
func SelectClaudeResetGrant(status *objects.CPAClaudeReset, now time.Time) *objects.CPAClaudeResetGrant {
	if status == nil {
		return nil
	}
	claimable := make([]*objects.CPAClaudeResetGrant, 0, len(status.Grants))
	for index := range status.Grants {
		grant := &status.Grants[index]
		if ClaudeResetBlocker(status, grant.ID, now) != "" {
			continue
		}
		if grant.ID == status.NextGrantID {
			return grant
		}
		claimable = append(claimable, grant)
	}
	if len(claimable) == 0 {
		return nil
	}
	sort.Slice(claimable, func(i, j int) bool { return claimable[i].ID < claimable[j].ID })
	return claimable[0]
}

// ReadClaudeReset reads the live reset-grant status without persisting it.
func ReadClaudeReset(ctx context.Context, client ManagementClient, authIndex string) (*objects.CPAClaudeReset, error) {
	var body map[string]any
	if _, err := callJSON(ctx, client, ProviderCall{
		AuthIndex: authIndex,
		Method:    http.MethodGet,
		URL:       claudeResetStatusURL,
		Headers:   claudeRequestHeaders(),
	}, &body); err != nil {
		return nil, fmt.Errorf("Claude reset status provider request failed")
	}
	status, ok := ParseClaudeReset(body["cedar_ember"])
	if !ok {
		return nil, fmt.Errorf("Claude reset status provider response is malformed")
	}
	return status, nil
}

// ReadClaudeOrganization reads the organization UUID the claim is addressed to.
func ReadClaudeOrganization(ctx context.Context, client ManagementClient, authIndex string) (string, error) {
	var profile map[string]any
	if _, err := callJSON(ctx, client, ProviderCall{
		AuthIndex: authIndex,
		Method:    http.MethodGet,
		URL:       claudeProfileURL,
		Headers:   claudeRequestHeaders(),
	}, &profile); err != nil {
		return "", fmt.Errorf("Claude profile provider request failed")
	}
	organization := strings.ToLower(stringValue(firstValue(asMap(firstValue(profile, "organization")), "uuid")))
	if !claudeOrganizationPattern.MatchString(organization) {
		return "", fmt.Errorf("Claude profile provider response has no organization")
	}
	return organization, nil
}

// ClaimClaudeReset sends exactly one claim. It returns ErrClaudeResetOutcomeUnknown
// whenever the claim may have been sent without a settled answer; callers hold
// the unique DB claim and reuse requestID for any retry.
func ClaimClaudeReset(ctx context.Context, client ManagementClient, authIndex, organization, grantID, requestID string) (objects.CPAClaudeResetResult, error) {
	if !claudeOrganizationPattern.MatchString(organization) || !claudeResetGrantIDPattern.MatchString(grantID) || !claudeResetRequestIDPattern.MatchString(requestID) {
		return "", fmt.Errorf("invalid Claude reset claim identity")
	}
	data, err := json.Marshal(map[string]string{"program": claudeResetProgram, "grant_id": grantID, "request_id": requestID})
	if err != nil {
		return "", fmt.Errorf("encode Claude reset request: %w", err)
	}
	result, err := client.CallProvider(ctx, ProviderCall{
		AuthIndex: authIndex,
		Method:    http.MethodPost,
		URL:       fmt.Sprintf(claudeResetClaimURLTmpl, organization),
		Headers:   claudeRequestHeaders(),
		Body:      string(data),
	})
	if err != nil {
		return "", ErrClaudeResetOutcomeUnknown
	}
	switch result.StatusCode {
	case http.StatusTooManyRequests:
		return objects.CPAClaudeResetResultRateLimited, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return objects.CPAClaudeResetResultAuthError, nil
	}
	if result.StatusCode >= http.StatusOK && result.StatusCode < http.StatusMultipleChoices {
		var response struct {
			Result string `json:"result"`
		}
		if json.Unmarshal(result.Body, &response) == nil {
			if settled, ok := claudeResetResults[response.Result]; ok {
				return settled, nil
			}
		}
	}
	return "", ErrClaudeResetOutcomeUnknown
}
