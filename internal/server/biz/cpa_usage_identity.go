package biz

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/ent/cpainstance"
	"github.com/looplj/axonhub/internal/log"
)

func newCPAUsageCollectorID() string {
	return uuid.NewString()
}

func (svc *CPAService) ensureCPAUsageCollectorIdentities(ctx context.Context) error {
	instances, err := svc.entFromContext(ctx).CPAInstance.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("query CPA instances for usage identities: %w", err)
	}
	for _, instance := range instances {
		if _, err := svc.ensureCPAUsageCollectorIdentity(ctx, instance); err != nil {
			return fmt.Errorf("initialize CPA usage identity for instance %d: %w", instance.ID, err)
		}
	}
	return nil
}

func (svc *CPAService) ensureCPAUsageCollectorIdentity(ctx context.Context, instance *ent.CPAInstance) (*ent.CPAInstance, error) {
	if instance == nil {
		return nil, fmt.Errorf("CPA instance is nil")
	}
	if collectorID := strings.TrimSpace(instance.UsageCollectorID); collectorID != "" {
		return instance, nil
	}

	collectorID, err := svc.legacyCPAUsageCollectorID(ctx, instance.ID)
	if err != nil {
		return nil, err
	}
	if collectorID == "" {
		collectorID = newCPAUsageCollectorID()
	}

	var resolved *ent.CPAInstance
	err = svc.withCPAInstanceWriteRetry(ctx, instance.ID, func() error {
		client := svc.entFromContext(ctx)
		current, getErr := client.CPAInstance.Get(ctx, instance.ID)
		if getErr != nil {
			return fmt.Errorf("reload CPA instance for usage identity: %w", getErr)
		}
		if strings.TrimSpace(current.UsageCollectorID) != "" {
			resolved = current
			return nil
		}
		updated, updateErr := client.CPAInstance.UpdateOneID(instance.ID).
			Where(cpainstance.UsageCollectorIDEQ("")).
			SetUsageCollectorID(collectorID).
			Save(ctx)
		if updateErr != nil {
			if ent.IsNotFound(updateErr) {
				resolved, updateErr = client.CPAInstance.Get(ctx, instance.ID)
				if updateErr == nil && strings.TrimSpace(resolved.UsageCollectorID) != "" {
					return nil
				}
			}
			return fmt.Errorf("persist CPA usage identity: %w", updateErr)
		}
		resolved = updated
		return nil
	})
	if err != nil {
		return nil, err
	}
	if resolved == nil || strings.TrimSpace(resolved.UsageCollectorID) == "" {
		return nil, fmt.Errorf("CPA usage identity is empty after initialization")
	}
	return resolved, nil
}

func (svc *CPAService) legacyCPAUsageCollectorID(ctx context.Context, instanceID int) (string, error) {
	credentials, err := svc.entFromContext(ctx).CPACredential.Query().
		Where(cpacredential.CpaInstanceIDEQ(instanceID)).
		All(ctx)
	if err != nil {
		return "", fmt.Errorf("query CPA credentials for usage identity: %w", err)
	}

	known := make(map[string]struct{})
	for _, credential := range credentials {
		if collectorID := strings.TrimSpace(credential.QuotaObserved.SecondaryCollectorSessionID); collectorID != "" {
			known[collectorID] = struct{}{}
		}
		for _, item := range credential.QuotaData.Items {
			if collectorID := strings.TrimSpace(item.EstimateCollectorSessionID); collectorID != "" {
				known[collectorID] = struct{}{}
			}
		}
	}
	if len(known) == 1 {
		for collectorID := range known {
			return collectorID, nil
		}
	}
	if len(known) > 1 {
		log.Warn(ctx, "ambiguous legacy CPA usage identities; creating a new identity",
			log.Int("cpa_instance_id", instanceID),
			log.Int("identity_count", len(known)),
		)
	}
	return "", nil
}
