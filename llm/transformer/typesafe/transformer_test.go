package typesafe

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestInboundTransformerPreservesSystemOneBody(t *testing.T) {
	body := []byte(`{"model":"jev-latest","state":{"ticket":"urgent"},"questions":{"urgent":{"type":"noul","instructions":"Urgent?"}},"future_field":true}`)
	inbound := NewInboundTransformer()

	req, err := inbound.TransformRequest(context.Background(), &httpclient.Request{
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    body,
	})

	require.NoError(t, err)
	require.Equal(t, "jev-latest", req.Model)
	require.Equal(t, llm.RequestTypeSystemOne, req.RequestType)
	require.Equal(t, llm.APIFormatTypeSafeSystemOne, req.APIFormat)
	require.JSONEq(t, string(body), string(req.SystemOne.Body))
}

func TestInboundTransformerRequiresModel(t *testing.T) {
	inbound := NewInboundTransformer()
	_, err := inbound.TransformRequest(context.Background(), &httpclient.Request{
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte(`{"state":"x","questions":{}}`),
	})
	require.ErrorContains(t, err, "model is required")
}

func TestOutboundTransformerBuildsBearerRequestAndPatchesModel(t *testing.T) {
	outbound, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL:        "https://api.typesafe.ai",
		APIKeyProvider: auth.NewStaticKeyProvider("typesafe-key"),
	})
	require.NoError(t, err)

	request, err := outbound.TransformRequest(context.Background(), &llm.Request{
		Model:       "jev-1.13.0",
		RequestType: llm.RequestTypeSystemOne,
		SystemOne: &llm.SystemOneRequest{Body: []byte(
			`{"model":"jev-latest","state":"hello","questions":{"ok":{"type":"noul","instructions":"OK?"}},"future_field":true}`,
		)},
	})

	require.NoError(t, err)
	require.Equal(t, http.MethodPost, request.Method)
	require.Equal(t, "https://api.typesafe.ai/v1/systemone", request.URL)
	require.Equal(t, httpclient.AuthTypeBearer, request.Auth.Type)
	require.Equal(t, "typesafe-key", request.Auth.APIKey)
	require.Equal(t, llm.APIFormatTypeSafeSystemOne.String(), request.APIFormat)

	var body map[string]any
	require.NoError(t, json.Unmarshal(request.Body, &body))
	require.Equal(t, "jev-1.13.0", body["model"])
	require.Equal(t, true, body["future_field"])
}

func TestOutboundTransformerUsesCustomEndpointPath(t *testing.T) {
	outbound, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL:        "https://relay.example/api#",
		EndpointPath:   "/custom/systemone",
		APIKeyProvider: auth.NewStaticKeyProvider("key"),
	})
	require.NoError(t, err)

	request, err := outbound.TransformRequest(context.Background(), &llm.Request{
		Model:       "jev-latest",
		RequestType: llm.RequestTypeSystemOne,
		SystemOne:   &llm.SystemOneRequest{Body: []byte(`{"model":"jev-latest"}`)},
	})

	require.NoError(t, err)
	require.Equal(t, "https://relay.example/api/custom/systemone", request.URL)
}

func TestOutboundTransformerPreservesResponseAndExtractsUsage(t *testing.T) {
	outbound, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL:        "https://api.typesafe.ai/v1",
		APIKeyProvider: auth.NewStaticKeyProvider("key"),
	})
	require.NoError(t, err)
	body := []byte(`{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.95}},"usage":{"input_tokens":296,"output_tokens":20}}`)

	resp, err := outbound.TransformResponse(context.Background(), &httpclient.Response{StatusCode: http.StatusOK, Body: body})

	require.NoError(t, err)
	require.Equal(t, "jev-1.13.0", resp.Model)
	require.JSONEq(t, string(body), string(resp.SystemOne.Body))
	require.Equal(t, int64(296), resp.Usage.PromptTokens)
	require.Equal(t, int64(20), resp.Usage.CompletionTokens)
	require.Equal(t, int64(316), resp.Usage.TotalTokens)

	clientResp, err := NewInboundTransformer().TransformResponse(context.Background(), resp)
	require.NoError(t, err)
	require.JSONEq(t, string(body), string(clientResp.Body))
}

func TestOutboundTransformerReturnsProviderError(t *testing.T) {
	outbound, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL:        "https://api.typesafe.ai/v1",
		APIKeyProvider: auth.NewStaticKeyProvider("key"),
	})
	require.NoError(t, err)

	_, err = outbound.TransformResponse(context.Background(), &httpclient.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       []byte(`{"detail":"rate limited"}`),
	})

	var respErr *llm.ResponseError
	require.ErrorAs(t, err, &respErr)
	require.Equal(t, http.StatusTooManyRequests, respErr.StatusCode)
	require.Contains(t, respErr.Detail.Message, "rate limited")
}
