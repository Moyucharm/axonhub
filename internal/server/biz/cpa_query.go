package biz

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/ent/predicate"
	"github.com/looplj/axonhub/internal/objects"
)

// CPACredentialFilterStatus is the closed set of API credential filters.
type CPACredentialFilterStatus string

const (
	CPACredentialFilterEnabled  CPACredentialFilterStatus = "enabled"
	CPACredentialFilterDisabled CPACredentialFilterStatus = "disabled"
	CPACredentialFilterAbnormal CPACredentialFilterStatus = "abnormal"
	CPACredentialFilterCooldown CPACredentialFilterStatus = "cooldown"
)

// QueryCPACredentialsInput contains one-instance list filters.
type QueryCPACredentialsInput struct {
	InstanceID   int
	First        int
	After        *string
	Search       *string
	Provider     *string
	Statuses     []CPACredentialFilterStatus
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

// CPAProviderOverview contains one provider's count and plan filter options.
type CPAProviderOverview struct {
	Provider  string
	Count     int
	PlanTypes []string
}

// CPAOverview contains all metadata needed above the credential table.
type CPAOverview struct {
	Stats     *CPACredentialStats
	Providers []*CPAProviderOverview
}

type cpaCredentialCursor struct {
	Version     int    `json:"v,omitempty"`
	Priority    int    `json:"priority"`
	SortKey     string `json:"sort_key,omitempty"`
	SortLength  int    `json:"sort_length,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	ID          int    `json:"id"`
}

func (svc *CPAService) QueryCredentials(ctx context.Context, input QueryCPACredentialsInput) (*CPACredentialConnection, error) {
	instance, err := svc.entFromContext(ctx).CPAInstance.Get(ctx, input.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("get CPA instance for credential query: %w", err)
	}
	now := svc.now()
	filtered := applyCPACredentialFilters(
		svc.entFromContext(ctx).CPACredential.Query(),
		input,
		now,
	)
	totalCount, err := filtered.Clone().Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("count CPA credentials: %w", err)
	}

	pageSize := input.First
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	pageQuery := filtered
	hasPreviousPage := input.After != nil && strings.TrimSpace(*input.After) != ""
	if hasPreviousPage {
		cursor, decodeErr := decodeCPACredentialCursor(strings.TrimSpace(*input.After))
		if decodeErr != nil {
			return nil, decodeErr
		}
		pageQuery = pageQuery.Where(cpaCredentialAfterPredicate(cursor))
	}
	credentials, err := pageQuery.
		Order(
			cpacredential.ByPriority(sql.OrderDesc()),
			cpacredential.ByDisplayNameSortKey(),
			cpacredential.ByDisplayNameSortLength(),
			cpacredential.ByID(),
		).
		Limit(pageSize + 1).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query CPA credential page: %w", err)
	}
	hasNextPage := len(credentials) > pageSize
	if hasNextPage {
		credentials = credentials[:pageSize]
	}

	edges := make([]*CPACredentialEdge, 0, len(credentials))
	for _, credential := range credentials {
		cursor, encodeErr := encodeCPACredentialCursor(credential)
		if encodeErr != nil {
			return nil, encodeErr
		}
		edges = append(edges, &CPACredentialEdge{
			Cursor: cursor,
			Node:   buildCPACredentialView(instance, credential, now),
		})
	}
	pageInfo := &CPAPageInfo{
		HasPreviousPage: hasPreviousPage,
		HasNextPage:     hasNextPage,
	}
	if len(edges) > 0 {
		pageInfo.StartCursor = &edges[0].Cursor
		pageInfo.EndCursor = &edges[len(edges)-1].Cursor
	}
	return &CPACredentialConnection{Edges: edges, PageInfo: pageInfo, TotalCount: totalCount}, nil
}

func applyCPACredentialFilters(query *ent.CPACredentialQuery, input QueryCPACredentialsInput, now time.Time) *ent.CPACredentialQuery {
	query = query.Where(cpacredential.CpaInstanceIDEQ(input.InstanceID))
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
	statusPredicates := make([]predicate.CPACredential, 0, 4)
	if statusFilter.enabled {
		statusPredicates = append(statusPredicates, cpacredential.DisabledEQ(false))
	}
	if statusFilter.disabled {
		statusPredicates = append(statusPredicates, cpacredential.DisabledEQ(true))
	}
	if statusFilter.abnormal {
		statusPredicates = append(statusPredicates, cpacredential.HealthStateEQ(string(objects.CPACredentialHealthAbnormal)))
	}
	if statusFilter.cooldown {
		statusPredicates = append(statusPredicates, effectiveCPACooldownPredicate(now))
	}
	if len(statusPredicates) > 0 {
		query = query.Where(cpacredential.Or(statusPredicates...))
	}
	if input.AbnormalOnly {
		query = query.Where(cpacredential.HealthStateEQ(string(objects.CPACredentialHealthAbnormal)))
	}
	return query
}

func effectiveCPACooldownPredicate(now time.Time) predicate.CPACredential {
	return cpacredential.And(
		cpacredential.QuotaCoolingEQ(true),
		cpacredential.Or(
			cpacredential.QuotaCooldownUntilIsNil(),
			cpacredential.QuotaCooldownUntilGT(now),
		),
	)
}

func cpaCredentialAfterPredicate(cursor cpaCredentialCursor) predicate.CPACredential {
	return cpacredential.Or(
		cpacredential.PriorityLT(cursor.Priority),
		cpacredential.And(
			cpacredential.PriorityEQ(cursor.Priority),
			cpacredential.DisplayNameSortKeyGT(cursor.SortKey),
		),
		cpacredential.And(
			cpacredential.PriorityEQ(cursor.Priority),
			cpacredential.DisplayNameSortKeyEQ(cursor.SortKey),
			cpacredential.DisplayNameSortLengthGT(cursor.SortLength),
		),
		cpacredential.And(
			cpacredential.PriorityEQ(cursor.Priority),
			cpacredential.DisplayNameSortKeyEQ(cursor.SortKey),
			cpacredential.DisplayNameSortLengthEQ(cursor.SortLength),
			cpacredential.IDGT(cursor.ID),
		),
	)
}

func (svc *CPAService) Overview(ctx context.Context, instanceID int) (*CPAOverview, error) {
	if _, err := svc.entFromContext(ctx).CPAInstance.Get(ctx, instanceID); err != nil {
		return nil, fmt.Errorf("get CPA instance for overview: %w", err)
	}
	stats, err := svc.credentialStatsAggregate(ctx, instanceID, svc.now())
	if err != nil {
		return nil, err
	}
	providers, err := svc.providerOverviewAggregate(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	return &CPAOverview{Stats: stats, Providers: providers}, nil
}

func (svc *CPAService) credentialStatsAggregate(ctx context.Context, instanceID int, now time.Time) (*CPACredentialStats, error) {
	base := func() *ent.CPACredentialQuery {
		return svc.entFromContext(ctx).CPACredential.Query().
			Where(cpacredential.CpaInstanceIDEQ(instanceID))
	}
	total, err := base().Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("count CPA credentials: %w", err)
	}
	abnormal, err := base().
		Where(cpacredential.HealthStateEQ(string(objects.CPACredentialHealthAbnormal))).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("count abnormal CPA credentials: %w", err)
	}
	available, err := base().
		Where(
			cpacredential.HealthStateEQ(string(objects.CPACredentialHealthHealthy)),
			cpacredential.Not(effectiveCPACooldownPredicate(now)),
		).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("count available CPA credentials: %w", err)
	}
	return &CPACredentialStats{Available: available, Total: total, Abnormal: abnormal}, nil
}

func (svc *CPAService) providerOverviewAggregate(ctx context.Context, instanceID int) ([]*CPAProviderOverview, error) {
	var rows []struct {
		Provider string `json:"provider"`
		PlanType string `json:"plan_type"`
		Count    int    `json:"count"`
	}
	if err := svc.entFromContext(ctx).CPACredential.Query().
		Where(cpacredential.CpaInstanceIDEQ(instanceID)).
		GroupBy(cpacredential.FieldProvider, cpacredential.FieldPlanType).
		Aggregate(ent.Count()).
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("aggregate CPA provider overview: %w", err)
	}
	byProvider := make(map[string]*CPAProviderOverview, len(rows))
	for _, row := range rows {
		provider := byProvider[row.Provider]
		if provider == nil {
			provider = &CPAProviderOverview{Provider: row.Provider}
			byProvider[row.Provider] = provider
		}
		provider.Count += row.Count
		if row.PlanType != "" {
			provider.PlanTypes = append(provider.PlanTypes, row.PlanType)
		}
	}
	result := make([]*CPAProviderOverview, 0, len(byProvider))
	for _, provider := range byProvider {
		sort.Strings(provider.PlanTypes)
		result = append(result, provider)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Provider < result[j].Provider })
	return result, nil
}

// buildCPACredentialView projects one SQL-selected credential row onto the API.
// Time-dependent cooldown expiry and instance staleness remain lazy read logic.
func buildCPACredentialView(instance *ent.CPAInstance, credential *ent.CPACredential, now time.Time) *CPACredentialView {
	quotaError := credential.QuotaState == string(objects.CPAQuotaStateError)
	cooling := effectiveCPACredentialCooling(credential.QuotaCooling, credential.QuotaCooldownUntil, now)
	var cooldownUntil *time.Time
	if cooling {
		cooldownUntil = credential.QuotaCooldownUntil
	}
	healthState := objects.CPACredentialHealthState(credential.HealthState)
	abnormal := healthState == objects.CPACredentialHealthAbnormal
	available := healthState == objects.CPACredentialHealthHealthy && !cooling
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
		StatusMessage:      sanitizeCPAErrorMessage(credential.StatusMessage),
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
		QuotaLastError:     sanitizeCPAErrorMessage(credential.QuotaLastError),
		Available:          available,
		Abnormal:           abnormal,
		Stale:              stale,
		Expired:            deriveCPAExpired(credential),
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

func parseCPAStatusFilter(statuses []CPACredentialFilterStatus) cpaStatusFilter {

	filter := cpaStatusFilter{}
	for _, status := range statuses {
		switch CPACredentialFilterStatus(strings.ToLower(strings.TrimSpace(string(status)))) {
		case CPACredentialFilterEnabled:
			filter.enabled = true
		case CPACredentialFilterDisabled:
			filter.disabled = true
		case CPACredentialFilterAbnormal:
			filter.abnormal = true
		case CPACredentialFilterCooldown:
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
	cooling, until, _ := cpaQuotaCooldownDetail(snapshot, now)
	return cooling, until
}

func cpaQuotaCooldownDetail(snapshot objects.CPAQuotaSnapshot, now time.Time) (bool, *time.Time, *objects.CPAQuotaItem) {
	return cpaQuotaCooldownDetailFor(snapshot, now, false)
}

// cpaAutoManageQuotaCooldownDetail reports quota exhaustion that is eligible
// for remote auto-disable. Five-hour windows remain visible to the UI through
// cpaQuotaCooldown, but never disable a credential by themselves.
func cpaAutoManageQuotaCooldownDetail(snapshot objects.CPAQuotaSnapshot, now time.Time) (bool, *time.Time, *objects.CPAQuotaItem) {
	return cpaQuotaCooldownDetailFor(snapshot, now, true)
}

func cpaQuotaCooldownDetailFor(snapshot objects.CPAQuotaSnapshot, now time.Time, ignoreFiveHour bool) (bool, *time.Time, *objects.CPAQuotaItem) {
	var cooling bool
	var until *time.Time
	var indefinite bool
	var exhaustedItem *objects.CPAQuotaItem
	for i := range snapshot.Items {
		item := &snapshot.Items[i]
		if ignoreFiveHour && cpaQuotaItemIsFiveHour(*item) {
			continue
		}
		if !cpaQuotaItemExhausted(*item) {
			continue
		}
		if item.ResetAt != nil && !item.ResetAt.After(now) {
			continue
		}
		cooling = true
		if exhaustedItem == nil {
			exhaustedItem = item
		}
		if item.ResetAt == nil {
			// A valid exhausted window without a reset time is an indefinite
			// cooldown. It must take precedence over any finite reset time.
			indefinite = true
			until = nil
			exhaustedItem = item
			continue
		}
		if indefinite {
			continue
		}
		if until == nil || item.ResetAt.Before(*until) {
			reset := item.ResetAt.UTC()
			until = &reset
			exhaustedItem = item
		}
	}
	return cooling, until, exhaustedItem
}

func cpaQuotaItemIsFiveHour(item objects.CPAQuotaItem) bool {
	if item.PeriodSeconds != nil {
		return *item.PeriodSeconds == 5*60*60
	}
	for _, value := range []string{item.ID, item.Label, item.Group} {
		normalized := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(value)))
		switch normalized {
		case "5h", "5hour", "fivehour":
			return true
		}
	}
	return false
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

func encodeCPACredentialCursor(credential *ent.CPACredential) (string, error) {
	encoded, err := json.Marshal(cpaCredentialCursor{
		Version:    2,
		Priority:   credential.Priority,
		SortKey:    credential.DisplayNameSortKey,
		SortLength: credential.DisplayNameSortLength,
		ID:         credential.ID,
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
	if cursor.Version > 2 {
		return cpaCredentialCursor{}, fmt.Errorf("decode CPA credential cursor: unsupported version %d", cursor.Version)
	}
	if cursor.SortKey == "" {
		cursor.SortKey = cpaDisplayNameSortKey(cursor.DisplayName)
		cursor.SortLength = cpaDisplayNameSortLength(cursor.DisplayName)
	}
	return cursor, nil
}
