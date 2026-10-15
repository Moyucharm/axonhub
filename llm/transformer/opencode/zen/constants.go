package zen

const (
	DefaultBaseURL   = "https://opencode.ai/zen/v1"
	AnonymousBaseURL = "https://opencode.ai/inference/v1"
	PublicAPIKey     = "public"
	OfficialUA       = "opencode/latest/2.0.29/cli"
	DefaultModel     = "mimo-v2.6-flash-free"
)

const (
	clientHeader    = "X-Opencode-Client"
	projectHeader   = "X-Opencode-Project"
	sessionHeader   = "X-Opencode-Session"
	sessionIDHeader = "X-Opencode-Session-Id"
	requestHeader   = "X-Opencode-Request"
)
