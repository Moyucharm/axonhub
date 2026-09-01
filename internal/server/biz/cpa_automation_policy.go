package biz

import (
	"time"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

type cpaAutomationAction string

const (
	cpaAutomationNone    cpaAutomationAction = "none"
	cpaAutomationDisable cpaAutomationAction = "disable"
	cpaAutomationEnable  cpaAutomationAction = "enable"
)

type cpaAutomationDecision struct {
	action        cpaAutomationAction
	reason        string
	exhaustedItem *objects.CPAQuotaItem
}

func decideCPAAutomation(credential *ent.CPACredential, now time.Time) cpaAutomationDecision {
	if credential.Disabled {
		if cpaCredentialRecovered(credential, now) {
			return cpaAutomationDecision{action: cpaAutomationEnable, reason: "quota recovered"}
		}
		return cpaAutomationDecision{action: cpaAutomationNone}
	}
	reason, exhaustedItem := cpaCredentialDisableReason(credential, now)
	if reason == "" {
		return cpaAutomationDecision{action: cpaAutomationNone}
	}
	return cpaAutomationDecision{action: cpaAutomationDisable, reason: reason, exhaustedItem: exhaustedItem}
}

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
