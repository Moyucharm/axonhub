package channel

import (
	"strings"
)

// Legacy channel types identify removed provider-specific channel types in old databases and backups.
const (
	LegacyTypeAtlascloud     Type = "atlascloud"
	LegacyTypeQiniu          Type = "qiniu"
	LegacyTypeQiniuAnthropic Type = "qiniu_anthropic"
	LegacyTypeFenno          Type = "fenno"
)

// NormalizeLegacyType maps removed channel types to their supported compatibility type.
func NormalizeLegacyType(t Type) Type {
	switch t {
	case LegacyTypeAtlascloud, LegacyTypeQiniu:
		return TypeOpenai
	case LegacyTypeQiniuAnthropic:
		return TypeAnthropic
	case LegacyTypeFenno:
		return TypeOpenaiResponses
	default:
		return t
	}
}

func (t Type) IsAnthropic() bool {
	return t == TypeAnthropic
}

func (t Type) IsAnthropicLike() bool {
	return strings.HasSuffix(string(t), "_anthropic")
}

func (t Type) IsGemini() bool {
	return t == TypeGemini
}

func (t Type) IsOpenAI() bool {
	return !t.IsAnthropicLike() && !t.IsAnthropic() && !t.IsGemini()
}

// UsesAnthropicModelAPI returns true if the channel type should use Anthropic-style
// /v1/models endpoint with X-Api-Key authentication when fetching models.
func (t Type) UsesAnthropicModelAPI() bool {
	return t.IsAnthropic() || t.IsAnthropicLike() || t == TypeClaudecode
}

// SupportsGoogleNativeTools returns true if the channel type supports Google native tools.
// Google native tools (google_search, google_url_context, google_code_execution) are only
// supported by native Gemini API format channels (gemini, gemini_vertex).
// OpenAI-compatible endpoints (gemini_openai) do NOT support these tools.
func (t Type) SupportsGoogleNativeTools() bool {
	return t == TypeGemini || t == TypeGeminiVertex
}

// SupportsAnthropicNativeTools returns true if the channel type supports Anthropic native tools.
// Anthropic native tools (web_search_20250305) are only supported by direct Anthropic API.
// Note: Bedrock (anthropic_aws) and Vertex (anthropic_gcp) do NOT currently support
// the web search beta feature, so they are excluded from native tool support.
// Channels using Anthropic format but not native Anthropic API (e.g., deepseek_anthropic,
// moonshot_anthropic) also do NOT support these tools.
func (t Type) SupportsAnthropicNativeTools() bool {
	return t == TypeAnthropic || t == TypeAnthropicAWS || t == TypeAnthropicGcp || t == TypeClaudecode
}
