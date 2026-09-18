package zen

const (
	DefaultBaseURL = "https://opencode.ai/zen/v1"
	PublicAPIKey   = "public"
	OfficialUA     = "opencode/1.18.31"
	DefaultModel   = "mimo-v2.5-free"
)

const stubToolsJSON = `[
	{"type":"function","function":{"name":"bash","description":"Execute bash commands in the environment.","parameters":{"type":"object","properties":{"command":{"type":"string","description":"The command to execute"}},"required":["command"]}}},
	{"type":"function","function":{"name":"read","description":"Read file contents from the environment.","parameters":{"type":"object","properties":{"filePath":{"type":"string","description":"The file path to read"}},"required":["filePath"]}}}
]`

const stubResponsesToolsJSON = `[
	{"type":"function","name":"bash","description":"Execute bash commands in the environment.","parameters":{"type":"object","properties":{"command":{"type":"string","description":"The command to execute"}},"required":["command"]}},
	{"type":"function","name":"read","description":"Read file contents from the environment.","parameters":{"type":"object","properties":{"filePath":{"type":"string","description":"The file path to read"}},"required":["filePath"]}}
]`

const (
	clientHeader  = "X-Opencode-Client"
	projectHeader = "X-Opencode-Project"
	sessionHeader = "X-Opencode-Session"
	requestHeader = "X-Opencode-Request"
)
