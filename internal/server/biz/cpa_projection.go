package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/objects"
)

const (
	currentCPAProjectionVersion = 1
	cpaProjectionBackfillBatch  = 200
)

type cpaCredentialProjection struct {
	displayNameSortKey    string
	displayNameSortLength int
	healthState           objects.CPACredentialHealthState
	quotaCooling          bool
	quotaCooldownUntil    *time.Time
}

func projectCPACredential(
	displayName string,
	status string,
	disabled bool,
	unavailable bool,
	quotaState objects.CPAQuotaState,
	quotaData objects.CPAQuotaSnapshot,
	now time.Time,
) cpaCredentialProjection {
	cooling, cooldownUntil := cpaQuotaCooldown(quotaData, now)
	healthState := objects.CPACredentialHealthPending
	switch {
	case disabled:
		healthState = objects.CPACredentialHealthDisabled
	case unavailable || cpaStatusAbnormal(status) || quotaState == objects.CPAQuotaStateError:
		healthState = objects.CPACredentialHealthAbnormal
	case quotaState == objects.CPAQuotaStateSuccess || quotaState == objects.CPAQuotaStateUnsupported:
		healthState = objects.CPACredentialHealthHealthy
	}
	return cpaCredentialProjection{
		displayNameSortKey:    cpaDisplayNameSortKey(displayName),
		displayNameSortLength: cpaDisplayNameSortLength(displayName),
		healthState:           healthState,
		quotaCooling:          cooling,
		quotaCooldownUntil:    cooldownUntil,
	}
}

func projectStoredCPACredential(credential *ent.CPACredential, now time.Time) cpaCredentialProjection {
	return projectStoredCPACredentialWithQuota(
		credential,
		objects.CPAQuotaState(credential.QuotaState),
		credential.QuotaData,
		now,
	)
}

func projectStoredCPACredentialWithQuota(
	credential *ent.CPACredential,
	quotaState objects.CPAQuotaState,
	quotaData objects.CPAQuotaSnapshot,
	now time.Time,
) cpaCredentialProjection {
	return projectCPACredential(
		credential.DisplayName,
		credential.Status,
		credential.Disabled,
		credential.Unavailable,
		quotaState,
		quotaData,
		now,
	)
}

func effectiveCPACredentialCooling(cooling bool, until *time.Time, now time.Time) bool {
	return cooling && (until == nil || until.After(now))
}

// cpaDisplayNameSortKey encodes the primary case-insensitive natural ordering
// into a bytewise-sortable string. Equal numeric runs intentionally ignore
// leading zeroes, matching naturalStringCompare.
func cpaDisplayNameSortKey(value string) string {
	value = strings.ToLower(value)
	var key strings.Builder
	key.Grow(len(value) + 16)
	for index := 0; index < len(value); {
		if value[index] < '0' || value[index] > '9' {
			key.WriteByte(value[index])
			index++
			continue
		}
		end := index
		for end < len(value) && value[end] >= '0' && value[end] <= '9' {
			end++
		}
		number := strings.TrimLeft(value[index:end], "0")
		if number == "" {
			number = "0"
		}
		// ASCII digits occupy one contiguous range before ':'; using '0' as
		// the run marker preserves digit-vs-text ordering while the fixed-width
		// length prefix makes arbitrary practical integer lengths sortable.
		fmt.Fprintf(&key, "0%08x%s", len(number), number)
		index = end
	}
	return key.String()
}

// cpaDisplayNameSortLength preserves naturalStringCompare's unusual final
// length tie-break: raw byte length matters only when comparison ends on a
// numeric run. Names ending in text remain equal on the natural key and use ID.
func cpaDisplayNameSortLength(value string) int {
	value = strings.ToLower(value)
	if value == "" || value[len(value)-1] < '0' || value[len(value)-1] > '9' {
		return 0
	}
	return len(value)
}

// backfillCPACredentialProjections upgrades rows created before the persisted
// query projection existed. It is idempotent and bounded by an ID keyset batch.
func (svc *CPAService) backfillCPACredentialProjections(ctx context.Context) error {
	lastID := 0
	for {
		rows, err := svc.entFromContext(ctx).CPACredential.Query().
			Where(
				cpacredential.ProjectionVersionLT(currentCPAProjectionVersion),
				cpacredential.IDGT(lastID),
			).
			Order(cpacredential.ByID()).
			Limit(cpaProjectionBackfillBatch).
			All(ctx)
		if err != nil {
			return fmt.Errorf("query CPA credential projection backfill: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		batchNow := svc.now()
		if err := svc.RunInTransaction(ctx, func(txCtx context.Context) error {
			db := svc.entFromContext(txCtx)
			for _, credential := range rows {
				projection := projectStoredCPACredential(credential, batchNow)
				update := db.CPACredential.UpdateOneID(credential.ID).
					SetDisplayNameSortKey(projection.displayNameSortKey).
					SetDisplayNameSortLength(projection.displayNameSortLength).
					SetHealthState(string(projection.healthState)).
					SetQuotaCooling(projection.quotaCooling).
					SetProjectionVersion(currentCPAProjectionVersion)
				if projection.quotaCooldownUntil == nil {
					update.ClearQuotaCooldownUntil()
				} else {
					update.SetQuotaCooldownUntil(*projection.quotaCooldownUntil)
				}
				if err := update.Exec(txCtx); err != nil {
					return fmt.Errorf("backfill CPA credential %d projection: %w", credential.ID, err)
				}
			}
			return nil
		}); err != nil {
			return err
		}
		lastID = rows[len(rows)-1].ID
	}
}
