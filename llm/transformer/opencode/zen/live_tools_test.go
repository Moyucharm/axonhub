package zen

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestZenLiveClientTools(t *testing.T) {
	if os.Getenv("AXONHUB_ZEN_LIVE_TEST") != "1" {
		t.Skip("set AXONHUB_ZEN_LIVE_TEST=1 to send fixed public-credential probes")
	}
	for _, tc := range []struct {
		format llm.APIFormat
		model  string
	}{
		{llm.APIFormatOpenAIChatCompletion, DefaultModel},
		{llm.APIFormatOpenAIResponse, museSparkContributorFreeModel},
	} {
		t.Run(tc.format.String(), func(t *testing.T) {
			outbound := protocolOutbound(t, tc.format, DefaultBaseURL, PublicAPIKey)
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			// These are declarations only. No tools are executed, no files read.
			result, err := zenTestPipeline(outbound).Process(ctx, &httpclient.Request{
				Headers: http.Header{"Content-Type": []string{"application/json"}},
				Body:    []byte(fmt.Sprintf(`{"model":%q,"stream":false,"max_tokens":512,"messages":[{"role":"user","content":"Call the client read tool with filePath example.txt. This is a synthetic tool-call test. Do not call any other tools."}],"tools":[{"type":"function","function":{"name":"read","description":"Client tool to read a virtual file.","parameters":{"type":"object","properties":{"filePath":{"type":"string"}},"required":["filePath"]}}},{"type":"function","function":{"name":"write","description":"Client virtual-file writer.","parameters":{"type":"object","properties":{"filePath":{"type":"string"},"content":{"type":"string"}},"required":["filePath","content"]}}}],"tool_choice":"auto"}`, tc.model)),
			})
			require.NoError(t, err)
			require.False(t, result.Stream)
			assert.Equal(t, "read", gjson.GetBytes(result.Response.Body, "choices.0.message.tool_calls.0.function.name").String())
			arguments := gjson.GetBytes(result.Response.Body, "choices.0.message.tool_calls.0.function.arguments").String()
			assert.Equal(t, "example.txt", gjson.Get(arguments, "filePath").String())
			t.Logf("model=%s response=%s", tc.model, result.Response.Body)
		})
	}
}
