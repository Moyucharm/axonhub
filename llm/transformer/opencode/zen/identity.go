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

const identityMetadataKey = "opencode_zen_identity"

// Private fields prevent the selected credential from being serialized with
// transformer metadata. The immutable value survives FinalizeAuthHeaders,
// which clears request.Auth before channel header overrides run.
type requestIdentity struct {
	apiKey        string
	project       string
	session       string
	childSession  string
	parentSession string
}

var canonicalSessionPattern = regexp.MustCompile(`^ses_[0-9a-f]{9}f[c-f][0-9a-f][0-9A-Za-z]{14}$`)

var canonicalProjectPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func initializeIdentity(ctx context.Context, source *llm.Request, request *httpclient.Request) error {
	header := http.Header(nil)
	if source.RawRequest != nil {
		header = source.RawRequest.Headers
	}
	signal := firstIdentitySignal(header, sessionHeader, sessionIDHeader, "X-Session-Affinity", "X-Session-Id", "Session_Id", "Conversation-Id", "Conversation_Id")
	if signal == "" {
		signal, _ = shared.GetSessionID(ctx)
	}
	if signal == "" {
		signal = source.Metadata["session_id"]
	}
	if signal == "" {
		// Hash the prelude and first user turn, not growing conversation history.
		seed := sha256.New()
		encoder := json.NewEncoder(seed)
		for _, message := range source.Messages {
			if message.Role == "system" || message.Role == "developer" || message.Role == "user" {
				_, _ = seed.Write([]byte(message.Role))
				if err := encoder.Encode(message.Content); err != nil {
					return err
				}
			}
			if message.Role == "user" {
				signal = hex.EncodeToString(seed.Sum(nil))
				break
			}
		}
	}
	if err := ensureIdentityMetadata(request); err != nil {
		return err
	}
	identity := request.TransformerMetadata[identityMetadataKey].(requestIdentity)
	scope, _ := shared.GetSessionScope(ctx)
	scopedSession := func(value string) string {
		if canonicalSessionPattern.MatchString(value) {
			return value
		}
		if scope != "" {
			value = scope + "\x00" + value
		}
		return canonicalSessionID(value)
	}
	if signal != "" {
		identity.session = scopedSession(signal)
	}
	identity.childSession = identity.session
	if child := strings.TrimSpace(header.Get(sessionIDHeader)); child != "" {
		identity.childSession = scopedSession(child)
	}
	parent := firstIdentitySignal(header, "X-Parent-Session-Id", "X-Opencode-Parent-Session-Id")
	if parent == "" {
		parent = source.Metadata["parent_session_id"]
	}
	if parent != "" {
		identity.parentSession = scopedSession(parent)
	}
	project := firstIdentitySignal(header, projectHeader)
	if project == "" {
		project = source.Metadata["project_id"]
	}
	if !canonicalProjectPattern.MatchString(project) {
		sum := sha256.Sum256([]byte(scope + "\x00" + project + "\x00axonhub-opencode-zen"))
		project = hex.EncodeToString(sum[:20])
	}
	identity.project = project
	request.TransformerMetadata[identityMetadataKey] = identity
	return nil
}

func firstIdentitySignal(header http.Header, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return value
		}
	}
	return ""
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
	if _, ok := request.TransformerMetadata[identityMetadataKey].(requestIdentity); !ok {
		sessionID, err := generateOpenCodeID("ses")
		if err != nil {
			return err
		}
		key := PublicAPIKey
		if request.Auth != nil && strings.TrimSpace(request.Auth.APIKey) != "" {
			key = strings.TrimSpace(request.Auth.APIKey)
		}
		project := sha256.Sum256([]byte("axonhub-opencode-zen"))
		request.TransformerMetadata[identityMetadataKey] = requestIdentity{
			apiKey: key, project: hex.EncodeToString(project[:20]), session: sessionID, childSession: sessionID,
		}
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
			"x-opencode-session-id", "x-session-id", "x-session-affinity", "x-opencode-request",
			"x-opencode-parent-session-id", "x-parent-session-id", "anthropic-beta", "anthropic-version":
			delete(request.Headers, header)
		}
	}
	if request.APIFormat == llm.APIFormatAnthropicMessage.String() {
		request.Auth = &httpclient.AuthConfig{Type: httpclient.AuthTypeAPIKey, APIKey: identity.apiKey, HeaderKey: "X-API-Key"}
		request.Headers.Set("X-API-Key", identity.apiKey)
		request.Headers.Set("Anthropic-Version", "2023-06-01")
		request.Headers.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
	} else {
		request.Auth = &httpclient.AuthConfig{Type: httpclient.AuthTypeBearer, APIKey: identity.apiKey}
		request.Headers.Set("Authorization", "Bearer "+identity.apiKey)
	}
	request.Headers.Set("Content-Type", "application/json")
	request.Headers.Set("User-Agent", OfficialUA)
	request.Headers.Set(clientHeader, "cli")
	request.Headers.Set(projectHeader, identity.project)
	rootSession := identity.session
	if identity.parentSession != "" {
		rootSession = identity.parentSession
		request.Headers.Set("X-Opencode-Parent-Session-Id", rootSession)
		request.Headers.Set("X-Parent-Session-Id", rootSession)
	}
	for _, header := range []string{sessionHeader, "X-Session-Id", "X-Session-Affinity"} {
		request.Headers.Set(header, rootSession)
	}
	request.Headers.Set(sessionIDHeader, identity.childSession)

	// Migrate only official standard inference paths. Custom gateways and
	// explicitly configured nonstandard endpoints retain their routing.
	parsed, err := url.Parse(request.URL)
	if err != nil {
		return err
	}
	if parsed.Scheme == "https" && parsed.Host == "opencode.ai" {
		family := "openai"
		if request.APIFormat == llm.APIFormatAnthropicMessage.String() {
			family = "anthropic"
		}
		for _, prefix := range []string{"/zen/v1/", "/inference/v1/"} {
			if suffix, ok := strings.CutPrefix(parsed.Path, prefix); ok {
				switch suffix {
				case "chat/completions", "responses", "messages":
					parsed.Path = "/inference/" + family + "/v1/" + suffix
				}
				break
			}
		}
	}
	if request.APIFormat == llm.APIFormatAnthropicMessage.String() {
		query := parsed.Query()
		query.Set("beta", "true")
		parsed.RawQuery = query.Encode()
		// BuildHttpRequest appends Query to the URL; keep beta in one place.
		request.Query.Del("beta")
	}
	request.URL = parsed.String()
	return nil
}

func cloneTransportRequest(request *httpclient.Request) *httpclient.Request {
	cloned := *request
	cloned.Body = append([]byte(nil), request.Body...)
	cloned.Headers = request.Headers.Clone()
	cloned.Query = maps.Clone(request.Query)
	cloned.TransformerMetadata = maps.Clone(request.TransformerMetadata)
	return &cloned
}
