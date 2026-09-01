package biz

import (
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

// cpaCredentialDisableReason contains the side-effect-free policy for the
// enabled patrol. Remote patching and snapshot synchronization stay outside
// this function so the policy can be tested without a CPA server.
func cpaCredentialDisableReason(credential *ent.CPACredential, now time.Time) (string, *objects.CPAQuotaItem) {
	if deriveCPAExpired(credential, now) {
		return "expired", nil
	}
	_, _, exhaustedItem := cpaAutoManageQuotaCooldownDetail(credential.QuotaData, now)
	if exhaustedItem != nil {
		return "quota exhausted", exhaustedItem
	}
	return "", nil
}
