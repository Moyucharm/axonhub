package biz

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/objects"
)

// QueryCPACredentialsInput contains one-instance list filters.
type QueryCPACredentialsInput struct {
	InstanceID   int
	First        int
	After        *string
	Search       *string
	Provider     *string
	Statuses     []string
	PlanTypes    []string
	AbnormalOnly bool
}

// CPACredentialView adds computed health fields to a stored credential.
type CPACredentialView struct {
	ID                 int
	InstanceID         int
	RemoteName         string
	DisplayName        string
	Provider           string
	Email              string
	Status             string
	StatusMessage      string
	Disabled           bool
	Unavailable        bool
	RuntimeOnly        bool
	Priority           int
	PlanType           string
	QuotaState         string
	QuotaData          objects.CPAQuotaSnapshot
	QuotaLastAttemptAt *time.Time
	QuotaLastSuccessAt *time.Time
	QuotaLastFailureAt *time.Time
	QuotaLastError     string
	Available          bool
	Abnormal           bool
	Stale              bool
	Expired            bool
	Cooling            bool
	CooldownUntil      *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
	credential         *ent.CPACredential
}

// CPACredentialEdge is one page edge.
type CPACredentialEdge struct {
	Cursor string
	Node   *CPACredentialView
}

// CPAPageInfo describes forward cursor pagination.
type CPAPageInfo struct {
	HasNextPage     bool
	HasPreviousPage bool
	StartCursor     *string
	EndCursor       *string
}

// CPACredentialConnection is the CPA credential list response.
type CPACredentialConnection struct {
	Edges      []*CPACredentialEdge
	PageInfo   *CPAPageInfo
	TotalCount int
}

// CPACredentialStats contains the selected instance summary.
type CPACredentialStats struct {
	Available int
	Total     int
	Abnormal  int
}

// CPAProviderCount is one dynamic provider tab count.
type CPAProviderCount struct {
	Provider string
	Count    int
}

type cpaCredentialCursor struct {
	Priority    int    `json:"priority"`
	DisplayName string `json:"display_name"`
	ID          int    `json:"id"`
}

func (svc *CPAService) QueryCredentials(ctx context.Context, input QueryCPACredentialsInput) (*CPACredentialConnection, error) {
	instance, err := svc.entFromContext(ctx).CPAInstance.Get(ctx, input.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("get CPA instance for credential query: %w", err)
	}
	query := svc.entFromContext(ctx).CPACredential.Query().
		Where(cpacredential.CpaInstanceIDEQ(input.InstanceID))
	if input.Search != nil && strings.TrimSpace(*input.Search) != "" {
		search := strings.TrimSpace(*input.Search)
		query = query.Where(cpacredential.Or(
			cpacredential.DisplayNameContainsFold(search),
			cpacredential.RemoteNameContainsFold(search),
			cpacredential.EmailContainsFold(search),
		))
	}
	if input.Provider != nil && strings.TrimSpace(*input.Provider) != "" {
		query = query.Where(cpacredential.ProviderEQ(strings.TrimSpace(*input.Provider)))
	}
	if len(input.PlanTypes) > 0 {
		query = query.Where(cpacredential.PlanTypeIn(input.PlanTypes...))
	}
	statusFilter := parseCPAStatusFilter(input.Statuses)
	// Enabled/disabled map directly onto a column, so push them into SQL and
	// keep cursor pagination consistent. When derived flags (abnormal/cooldown)
	// are also selected the result must stay a union of all requested states,
	// so the whole filter is evaluated in memory instead.
	if !statusFilter.abnormal && !statusFilter.cooldown {
		if statusFilter.enabled && !statusFilter.disabled {
			query = query.Where(cpacredential.DisabledEQ(false))
		} else if statusFilter.disabled && !statusFilter.enabled {
			query = query.Where(cpacredential.DisabledEQ(true))
		}
	}

	now := svc.now()
	credentials, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query CPA credentials: %w", err)
	}
	views := make([]*CPACredentialView, 0, len(credentials))
	for _, credential := range credentials {
		view := buildCPACredentialView(instance, credential, now)
		if input.AbnormalOnly && !view.Abnormal {
			continue
		}
		if !statusFilter.matches(view) {
			continue
		}
		views = append(views, view)
	}
	sort.SliceStable(views, func(i, j int) bool {
		return compareCPACredentials(views[i].credential, views[j].credential) < 0
	})

	start := 0
	if input.After != nil && *input.After != "" {
		cursor, decodeErr := decodeCPACredentialCursor(*input.After)
		if decodeErr != nil {
			return nil, decodeErr
		}
		start = sort.Search(len(views), func(index int) bool {
			return compareCredentialToCursor(views[index].credential, cursor) > 0
		})
	}
	pageSize := input.First
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	end := min(start+pageSize, len(views))
	page := views[start:end]
	edges := make([]*CPACredentialEdge, 0, len(page))
	for _, view := range page {
		cursor, encodeErr := encodeCPACredentialCursor(view.credential)
		if encodeErr != nil {
			return nil, encodeErr
		}
		edges = append(edges, &CPACredentialEdge{Cursor: cursor, Node: view})
	}
	pageInfo := &CPAPageInfo{
		HasPreviousPage: start > 0,
		HasNextPage:     end < len(views),
	}
	if len(edges) > 0 {
		pageInfo.StartCursor = &edges[0].Cursor
		pageInfo.EndCursor = &edges[len(edges)-1].Cursor
	}
	return &CPACredentialConnection{Edges: edges, PageInfo: pageInfo, TotalCount: len(views)}, nil
}

func (svc *CPAService) CredentialStats(ctx context.Context, instanceID int) (*CPACredentialStats, error) {
	instance, err := svc.entFromContext(ctx).CPAInstance.Get(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("get CPA instance for stats: %w", err)
	}
	credentials, err := svc.entFromContext(ctx).CPACredential.Query().
		Where(cpacredential.CpaInstanceIDEQ(instanceID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query CPA credential stats: %w", err)
	}
	stats := &CPACredentialStats{Total: len(credentials)}
	now := svc.now()
	for _, credential := range credentials {
		view := buildCPACredentialView(instance, credential, now)
		if view.Available {
			stats.Available++
		}
		if view.Abnormal {
			stats.Abnormal++
		}
	}
	return stats, nil
}

func (svc *CPAService) ProviderCounts(ctx context.Context, instanceID int) ([]*CPAProviderCount, error) {
	if _, err := svc.entFromContext(ctx).CPAInstance.Get(ctx, instanceID); err != nil {
		return nil, fmt.Errorf("get CPA instance for provider counts: %w", err)
	}
	var rows []struct {
		Provider string `json:"provider"`
		Count    int    `json:"count"`
	}
	if err := svc.entFromContext(ctx).CPACredential.Query().
		Where(cpacredential.CpaInstanceIDEQ(instanceID)).
		GroupBy(cpacredential.FieldProvider).
		Aggregate(ent.Count()).
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("count CPA credentials by provider: %w", err)
	}
	result := make([]*CPAProviderCount, 0, len(rows))
	for _, row := range rows {
		result = append(result, &CPAProviderCount{Provider: row.Provider, Count: row.Count})
	}
	return result, nil
}

func (svc *CPAService) PlanTypes(ctx context.Context, instanceID int, provider string) ([]string, error) {
	plans, err := svc.entFromContext(ctx).CPACredential.Query().
		Where(
			cpacredential.CpaInstanceIDEQ(instanceID),
			cpacredential.ProviderEQ(strings.TrimSpace(provider)),
			cpacredential.PlanTypeNEQ(""),
		).
		Select(cpacredential.FieldPlanType).
		Strings(ctx)
	if err != nil {
		return nil, fmt.Errorf("query CPA plan types: %w", err)
	}
	plans = uniqueStrings(plans)
	sort.Strings(plans)
	return plans, nil
}

// buildCPACredentialView projects a credential row onto the API view. now is
// injected so expiry/cooldown derivation stays deterministic in tests.
func buildCPACredentialView(instance *ent.CPAInstance, credential *ent.CPACredential, now time.Time) *CPACredentialView {
	statusAbnormal := cpaStatusAbnormal(credential.Status)
	quotaError := credential.QuotaState == string(objects.CPAQuotaStateError)
	quotaAvailable := credential.QuotaState == string(objects.CPAQuotaStateSuccess) ||
		credential.QuotaState == string(objects.CPAQuotaStateUnsupported)
	cooling, cooldownUntil := cpaQuotaCooldown(credential.QuotaData, now)
	abnormal := !credential.Disabled && (credential.Unavailable || statusAbnormal || quotaError)
	available := !credential.Disabled && !credential.Unavailable && !statusAbnormal && quotaAvailable && !cooling
	instanceStale := !instance.Enabled || (instance.LastError != nil && strings.TrimSpace(*instance.LastError) != "")
	stale := instanceStale || quotaError
	return &CPACredentialView{
		ID:                 credential.ID,
		InstanceID:         credential.CpaInstanceID,
		RemoteName:         credential.RemoteName,
		DisplayName:        credential.DisplayName,
		Provider:           credential.Provider,
		Email:              credential.Email,
		Status:             credential.Status,
		StatusMessage:      credential.StatusMessage,
		Disabled:           credential.Disabled,
		Unavailable:        credential.Unavailable,
		RuntimeOnly:        credential.RuntimeOnly,
		Priority:           credential.Priority,
		PlanType:           credential.PlanType,
		QuotaState:         credential.QuotaState,
		QuotaData:          credential.QuotaData,
		QuotaLastAttemptAt: credential.QuotaLastAttemptAt,
		QuotaLastSuccessAt: credential.QuotaLastSuccessAt,
		QuotaLastFailureAt: credential.QuotaLastFailureAt,
		QuotaLastError:     credential.QuotaLastError,
		Available:          available,
		Abnormal:           abnormal,
		Stale:              stale,
		Expired:            deriveCPAExpired(credential, now),
		Cooling:            cooling,
		CooldownUntil:      cooldownUntil,
		CreatedAt:          credential.CreatedAt,
		UpdatedAt:          credential.UpdatedAt,
		credential:         credential,
	}
}

type cpaStatusFilter struct {
	enabled  bool
	disabled bool
	abnormal bool
	cooldown bool
	any      bool
}

func parseCPAStatusFilter(statuses []string) cpaStatusFilter {
	filter := cpaStatusFilter{}
	for _, status := range statuses {
		switch strings.ToLower(strings.TrimSpace(status)) {
		case "enabled":
			filter.enabled = true
		case "disabled":
			filter.disabled = true
		case "abnormal":
			filter.abnormal = true
		case "cooldown":
			filter.cooldown = true
		}
	}
	filter.any = !filter.enabled && !filter.disabled && !filter.abnormal && !filter.cooldown
	return filter
}

func (filter cpaStatusFilter) matches(view *CPACredentialView) bool {
	if filter.any {
		return true
	}
	if filter.enabled && !view.Disabled {
		return true
	}
	if filter.disabled && view.Disabled {
		return true
	}
	if filter.abnormal && view.Abnormal {
		return true
	}
	if filter.cooldown && view.Cooling {
		return true
	}
	return false
}

// cpaQuotaCooldown reports whether any quota window is exhausted and still
// cooling down at now. Windows whose reset time already passed are ignored:
// they recovered server-side and only await the next successful refresh.
func cpaQuotaCooldown(snapshot objects.CPAQuotaSnapshot, now time.Time) (bool, *time.Time) {
	var cooling bool
	var until *time.Time
	for _, item := range snapshot.Items {
		if !cpaQuotaItemExhausted(item) {
			continue
		}
		if item.ResetAt != nil && !item.ResetAt.After(now) {
			continue
		}
		cooling = true
		if item.ResetAt == nil {
			continue
		}
		if until == nil || item.ResetAt.Before(*until) {
			reset := item.ResetAt.UTC()
			until = &reset
		}
	}
	return cooling, until
}

func cpaQuotaItemExhausted(item objects.CPAQuotaItem) bool {
	if item.UsedPercent != nil && *item.UsedPercent >= 100 {
		return true
	}
	if item.RemainingPercent != nil && *item.RemainingPercent <= 0 {
		return true
	}
	return item.Limit != nil && *item.Limit > 0 && item.Remaining != nil && *item.Remaining <= 0
}

func cpaStatusAbnormal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "active", "available", "enabled", "ok", "ready", "unknown":
		return false
	case "disabled":
		return false
	default:
		return true
	}
}

// cpaExpiredStatusMessages lists remote status_message values that indicate a
// permanently unusable credential (HTTP 401/402/403/404 class failures).
var cpaExpiredStatusMessages = map[string]struct{}{
	"unauthorized":     {},
	"payment_required": {},
	"forbidden":        {},
	"not_found":        {},
}

// deriveCPAExpired reports whether a credential should be displayed as expired.
// It is purely derived: a renewed subscription or a recovered remote status
// clears the flag without any persisted state.
func deriveCPAExpired(credential *ent.CPACredential, now time.Time) bool {
	// A successful quota refresh is live proof the credential still works;
	// stale JWT subscription dates must not override it.
	if credential.QuotaState == string(objects.CPAQuotaStateSuccess) {
		return false
	}
	if cpaSubscriptionExpired(credential.QuotaContext.SubscriptionEnd, now) {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(credential.Status), "error") {
		return false
	}
	_, ok := cpaExpiredStatusMessages[strings.ToLower(strings.TrimSpace(credential.StatusMessage))]
	return ok
}

// cpaSubscriptionExpired parses the subscription end value from JWT claims and
// reports whether it has passed. Date-only values are granted until the end of
// that day; numeric values are treated as unix seconds.
func cpaSubscriptionExpired(raw string, now time.Time) bool {
	value := strings.TrimSpace(raw)
	if value == "" {
		return false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		return now.After(time.Unix(seconds, 0))
	}
	parsed, ok := parseSubscriptionEndTime(value)
	if !ok {
		return false
	}
	return now.After(parsed)
}

// parseSubscriptionEndTime accepts common date/time layouts used by providers.
func parseSubscriptionEndTime(value string) (time.Time, bool) {
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			// Date-only values stay valid through the entire last day.
			if layout == "2006-01-02" {
				parsed = parsed.Add(24*time.Hour - time.Second)
			}
			return parsed, true
		}
	}
	return time.Time{}, false
}

func compareCPACredentials(left, right *ent.CPACredential) int {
	if left.Priority != right.Priority {
		if left.Priority > right.Priority {
			return -1
		}
		return 1
	}
	leftName := strings.ToLower(left.DisplayName)
	rightName := strings.ToLower(right.DisplayName)
	if result := naturalStringCompare(leftName, rightName); result != 0 {
		return result
	}
	if left.ID < right.ID {
		return -1
	}
	if left.ID > right.ID {
		return 1
	}
	return 0
}

func naturalStringCompare(left, right string) int {
	for leftIndex, rightIndex := 0, 0; leftIndex < len(left) && rightIndex < len(right); {
		leftDigit := left[leftIndex] >= '0' && left[leftIndex] <= '9'
		rightDigit := right[rightIndex] >= '0' && right[rightIndex] <= '9'
		if leftDigit && rightDigit {
			leftEnd, rightEnd := leftIndex, rightIndex
			for leftEnd < len(left) && left[leftEnd] >= '0' && left[leftEnd] <= '9' {
				leftEnd++
			}
			for rightEnd < len(right) && right[rightEnd] >= '0' && right[rightEnd] <= '9' {
				rightEnd++
			}
			leftNumber := strings.TrimLeft(left[leftIndex:leftEnd], "0")
			rightNumber := strings.TrimLeft(right[rightIndex:rightEnd], "0")
			if leftNumber == "" {
				leftNumber = "0"
			}
			if rightNumber == "" {
				rightNumber = "0"
			}
			if len(leftNumber) < len(rightNumber) {
				return -1
			}
			if len(leftNumber) > len(rightNumber) {
				return 1
			}
			if leftNumber < rightNumber {
				return -1
			}
			if leftNumber > rightNumber {
				return 1
			}
			leftIndex, rightIndex = leftEnd, rightEnd
			continue
		}
		if left[leftIndex] < right[rightIndex] {
			return -1
		}
		if left[leftIndex] > right[rightIndex] {
			return 1
		}
		leftIndex++
		rightIndex++
		if leftIndex == len(left) && rightIndex == len(right) {
			return 0
		}
		if leftIndex == len(left) {
			return -1
		}
		if rightIndex == len(right) {
			return 1
		}
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}

func compareCredentialToCursor(credential *ent.CPACredential, cursor cpaCredentialCursor) int {
	return compareCPACredentials(credential, &ent.CPACredential{
		ID:          cursor.ID,
		Priority:    cursor.Priority,
		DisplayName: cursor.DisplayName,
	})
}

func encodeCPACredentialCursor(credential *ent.CPACredential) (string, error) {
	encoded, err := json.Marshal(cpaCredentialCursor{
		Priority:    credential.Priority,
		DisplayName: credential.DisplayName,
		ID:          credential.ID,
	})
	if err != nil {
		return "", fmt.Errorf("encode CPA credential cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeCPACredentialCursor(value string) (cpaCredentialCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cpaCredentialCursor{}, fmt.Errorf("decode CPA credential cursor: %w", err)
	}
	var cursor cpaCredentialCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return cpaCredentialCursor{}, fmt.Errorf("decode CPA credential cursor: %w", err)
	}
	return cursor, nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
