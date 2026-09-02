package biz

import (
	"strings"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

// cpaExpiredStatusMessages contains terminal status labels emitted by CPA's
// auth-files endpoint. These labels are interpreted only when CPA marks the
// credential status as error.
var cpaExpiredStatusMessages = map[string]struct{}{
	"unauthorized":     {},
	"payment_required": {},
	"forbidden":        {},
	"not_found":        {},
}

// deriveCPAExpired reports whether CPA has provided evidence that a
// credential is permanently unusable. JWT subscription claims are deliberately
// not part of this decision.
func deriveCPAExpired(credential *ent.CPACredential) bool {
	// A successful quota refresh is live proof that the credential still works.
	if credential.QuotaState == string(objects.CPAQuotaStateSuccess) {
		return false
	}

	// A known result from the latest quota request takes precedence over an older
	// auth-files status, including a stale terminal status from a prior sync.
	if quotaError := strings.TrimSpace(credential.QuotaLastError); quotaError != "" {
		switch cpaclient.ClassifyProviderErrorText(quotaError) {
		case cpaclient.ProviderErrorCredentialExpired:
			return true
		case cpaclient.ProviderErrorQuotaExhausted,
			cpaclient.ProviderErrorTransient,
			cpaclient.ProviderErrorManagement,
			cpaclient.ProviderErrorUnknown:
			return false
		}
	}

	if !strings.EqualFold(strings.TrimSpace(credential.Status), "error") {
		return false
	}
	statusMessage := strings.ToLower(strings.TrimSpace(credential.StatusMessage))
	if _, ok := cpaExpiredStatusMessages[statusMessage]; ok {
		return true
	}
	return cpaclient.ClassifyProviderErrorText(statusMessage) == cpaclient.ProviderErrorCredentialExpired
}
