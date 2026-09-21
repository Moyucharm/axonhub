# TypeSafe System One API Reference

## Overview

AxonHub exposes TypeSafe AI's native System One protocol for Jev. System One is an evaluation protocol, not a chat protocol: a request sends shared `state` plus typed `questions`, and Jev returns structured decisions and probabilities.

## Endpoint

- `POST /v1/systemone`

Authenticate to AxonHub with the same Bearer API key used by the other public APIs.

## Channel endpoint configuration

Add a custom endpoint to a channel that contains the Jev model:

| Field | Value |
|---|---|
| API format | `typesafe/systemone` |
| Base URL | `https://api.typesafe.ai/v1` |
| Path | `/systemone` (optional; this is the default path) |
| Channel credential | TypeSafe API key |

The endpoint is opt-in. A System One request is never sent to a chat, responses, or messages endpoint when `typesafe/systemone` is missing.

## Request

```bash
curl http://localhost:8090/v1/systemone \
  -H "Authorization: Bearer $AXONHUB_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "jev-latest",
    "state": "Help! My payouts have been failing for 3 days.",
    "questions": {
      "is_urgent": {
        "type": "noul",
        "instructions": "Does this convey urgency?"
      }
    }
  }'
```

AxonHub uses the top-level `model` for routing and applies normal model mapping. `state`, `questions`, and unknown extension fields are forwarded unchanged.

## Response

```json
{
  "model": "jev-1.13.0",
  "answers": {
    "is_urgent": {
      "type": "noul",
      "noul": 0.95
    }
  },
  "usage": {
    "input_tokens": 296,
    "output_tokens": 20
  }
}
```

The provider response is returned unchanged. AxonHub also maps TypeSafe usage to its internal input, output, and total token counters for request records, rate tracking, and cost calculation.

## Supported Jev model names

TypeSafe currently documents `jev-latest`, `jev-preview`, and versioned IDs such as `jev-1.13.0`. Configure the desired IDs in the channel's supported model list and in AxonHub model associations.

## Limitations

- Streaming is not supported by TypeSafe System One.
- This endpoint does not convert chat/messages requests into System One questions.
- AxonHub's `/v1/models` remains the unified AxonHub model catalog; this feature does not proxy TypeSafe's provider-side model listing endpoint.
