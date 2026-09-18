package zen

import "slices"

const (
	DefaultBaseURL = "https://opencode.ai/zen/v1"
	PublicAPIKey   = "public"
	OfficialUA     = "opencode/1.18.25 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.14"
	DefaultModel   = "mimo-v2.5-free"
)

var defaultModels = []string{
	DefaultModel,
	"nemotron-3.5-lightning-free",
	"nemotron-3-ultra-free",
	"ling-3.0-flash-fin-free",
}

// DefaultModels returns the OpenCode Zen free models known to satisfy the
// provider contract. The provider does not expose a stable public model catalog.
func DefaultModels() []string {
	return slices.Clone(defaultModels)
}

const stubToolsJSON = `[
	{"type":"function","function":{"name":"bash","description":"Execute bash commands in the environment.","parameters":{"type":"object","properties":{"command":{"type":"string","description":"The command to execute"}},"required":["command"]}}},
	{"type":"function","function":{"name":"read","description":"Read file contents from the environment.","parameters":{"type":"object","properties":{"filePath":{"type":"string","description":"The file path to read"}},"required":["filePath"]}}}
]`

const (
	clientHeader  = "X-Opencode-Client"
	projectHeader = "X-Opencode-Project"
	sessionHeader = "X-Opencode-Session"
	requestHeader = "X-Opencode-Request"
)
