package biz

import (
	"regexp"
	"strings"
)

const maxCPAErrorMessageLength = 512

var (
	cpaAuthorizationValuePattern = regexp.MustCompile(`(?i)(authorization\s*:\s*(?:bearer\s+)?|bearer\s+)[^\s,;]+`)
	cpaTokenFieldPattern         = regexp.MustCompile(`(?i)((?:access_token|refresh_token|api_key|id_token)\s*[:=]\s*)(["']?)[^\s,"'}]+["']?`)
	cpaJWTPattern                = regexp.MustCompile(`\b[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)
)

func sanitizeCPAErrorMessage(raw string) string {
	message := strings.TrimSpace(raw)
	if message == "" {
		return ""
	}
	message = cpaAuthorizationValuePattern.ReplaceAllString(message, "$1[REDACTED]")
	message = cpaTokenFieldPattern.ReplaceAllString(message, "$1[REDACTED]")
	message = cpaJWTPattern.ReplaceAllString(message, "[REDACTED]")
	if len(message) > maxCPAErrorMessageLength {
		message = message[:maxCPAErrorMessageLength]
	}
	return message
}
