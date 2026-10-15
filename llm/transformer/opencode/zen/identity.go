package zen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

const (
	sessionMetadataKey  = "opencode_zen_session_id"
	identityMetadataKey = "opencode_zen_identity"
)

// Private fields prevent the selected credential from being serialized with
// transformer metadata. The immutable value survives FinalizeAuthHeaders,
// which clears request.Auth before channel header overrides run.
type requestIdentity struct {
	apiKey  string
	project string
}

var canonicalSessionPattern = regexp.MustCompile(`^ses_[0-9a-f]{9}f[c-f][0-9a-f][0-9A-Za-z]{14}$`)

func initializeIdentity(ctx context.Context, source *llm.Request, request *httpclient.Request) error {
	signal := ""
	if source.RawRequest != nil {
		for _, header := range []string{sessionHeader, sessionIDHeader, "X-Session-Id", "X-Session-Affinity", "Conversation-Id"} {
			if signal = strings.TrimSpace(source.RawRequest.Headers.Get(header)); signal != "" {
				break
			}
		}
	}
	if signal == "" {
		signal, _ = shared.GetSessionID(ctx)
	}
	if signal == "" {
		signal = source.Metadata["session_id"]
	}
	if signal == "" {
		for _, message := range source.Messages {
			if message.Role == "user" {
				content, err := json.Marshal(message.Content)
				if err == nil {
					signal = string(content)
				}
				break
			}
		}
	}
	if signal != "" {
		if scope, ok := shared.GetSessionScope(ctx); ok && scope != "" {
			signal = scope + "\x00" + signal
		}
		if request.TransformerMetadata == nil {
			request.TransformerMetadata = make(map[string]any)
		}
		request.TransformerMetadata[sessionMetadataKey] = canonicalSessionID(signal)
	}
	return ensureIdentityMetadata(request)
}

// canonicalSessionID encodes foreign client IDs deterministically with the
// descending timestamp layout (counter 1), retaining multi-turn cache affinity.
func canonicalSessionID(signal string) string {
	if canonicalSessionPattern.MatchString(signal) {
		return signal
	}
	sum := sha256.Sum256([]byte(signal))
	stamp := [6]byte{sum[0], sum[1], sum[2], sum[3], (sum[4] & 0xf0) | 0x0f, 0xfe}
	suffix := make([]byte, 14)
	for i := range suffix {
		suffix[i] = openCodeIDAlphabet[int(sum[i+6])%len(openCodeIDAlphabet)]
	}
	return "ses_" + hex.EncodeToString(stamp[:]) + string(suffix)
}

func ensureIdentityMetadata(request *httpclient.Request) error {
	if request.TransformerMetadata == nil {
		request.TransformerMetadata = make(map[string]any)
	}
	if sessionID, _ := request.TransformerMetadata[sessionMetadataKey].(string); sessionID == "" {
		generated, err := generateOpenCodeID("ses")
		if err != nil {
			return err
		}
		request.TransformerMetadata[sessionMetadataKey] = generated
	}
	if _, ok := request.TransformerMetadata[identityMetadataKey].(requestIdentity); !ok {
		key := PublicAPIKey
		if request.Auth != nil && strings.TrimSpace(request.Auth.APIKey) != "" {
			key = strings.TrimSpace(request.Auth.APIKey)
		}
		project := sha256.Sum256([]byte("axonhub-opencode-zen"))
		request.TransformerMetadata[identityMetadataKey] = requestIdentity{apiKey: key, project: hex.EncodeToString(project[:20])}
	}
	return nil
}

func applyIdentityFingerprint(request *httpclient.Request) error {
	if request.Headers == nil {
		request.Headers = make(http.Header)
	}
	if err := ensureIdentityMetadata(request); err != nil {
		return err
	}
	identity := request.TransformerMetadata[identityMetadataKey].(requestIdentity)
	// Inbound/header-override maps may contain non-canonical casing. Remove
	// every spelling before setting authoritative identity fields.
	for header := range request.Headers {
		switch strings.ToLower(header) {
		case "authorization", "x-api-key", "content-type", "accept", "user-agent",
			"x-opencode-client", "x-opencode-project", "x-opencode-session",
			"x-opencode-session-id", "x-session-id", "x-session-affinity", "x-opencode-request":
			delete(request.Headers, header)
		}
	}
	if request.APIFormat == llm.APIFormatAnthropicMessage.String() {
		request.Auth = &httpclient.AuthConfig{Type: httpclient.AuthTypeAPIKey, APIKey: identity.apiKey, HeaderKey: "X-API-Key"}
		request.Headers.Set("X-API-Key", identity.apiKey)
		request.Headers.Set("Anthropic-Version", "2023-06-01")
	} else {
		request.Auth = &httpclient.AuthConfig{Type: httpclient.AuthTypeBearer, APIKey: identity.apiKey}
		request.Headers.Set("Authorization", "Bearer "+identity.apiKey)
	}
	request.Headers.Set("Content-Type", "application/json")
	request.Headers.Set("Accept", "application/json, text/event-stream")
	request.Headers.Set("User-Agent", OfficialUA)
	request.Headers.Set(clientHeader, "cli")
	request.Headers.Set(projectHeader, identity.project)
	sessionID, _ := request.TransformerMetadata[sessionMetadataKey].(string)
	for _, header := range []string{sessionHeader, sessionIDHeader, "X-Session-Id", "X-Session-Affinity"} {
		request.Headers.Set(header, sessionID)
	}
	request.Headers.Del(requestHeader)

	// Only the official legacy base is migrated; custom gateways, paths and
	// explicit endpoint overrides retain their exact routing semantics.
	if identity.apiKey == PublicAPIKey {
		if parsed, err := url.Parse(request.URL); err == nil && parsed.Scheme == "https" && parsed.Host == "opencode.ai" {
			switch parsed.Path {
			case "/zen/v1/chat/completions", "/zen/v1/responses", "/zen/v1/messages":
				parsed.Path = strings.Replace(parsed.Path, "/zen/v1/", "/inference/v1/", 1)
				request.URL = parsed.String()
			}
		}
	}
	return nil
}

func cloneTransportRequest(request *httpclient.Request) *httpclient.Request {
	cloned := *request
	cloned.Body = append([]byte(nil), request.Body...)
	cloned.Headers = request.Headers.Clone()
	cloned.TransformerMetadata = maps.Clone(request.TransformerMetadata)
	return &cloned
}
