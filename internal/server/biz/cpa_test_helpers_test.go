package biz

import (
	"time"

	"github.com/looplj/axonhub/internal/ent"
)

func newCPAServiceForTest(client *ent.Client, now func() time.Time) *CPAService {
	svc := NewCPAService(CPAServiceParams{Ent: client})
	if now != nil {
		svc.now = now
		svc.usageRepository.now = now
		svc.pricingRepository.now = now
		svc.repository.now = now
		svc.quotaExecutor = newCPAQuotaExecutor(svc.quotaRegistry, svc.applyQuotaEstimate, svc.now)
	}
	return svc
}
