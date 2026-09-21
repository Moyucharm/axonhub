# Agent Note: TypeSafe System One endpoint support

Status: implemented

## Problem

Jev uses TypeSafe AI's System One evaluation protocol rather than a chat, responses, or messages protocol. Treating Jev as an OpenAI-compatible chat model would send the wrong request shape, lose typed questions and probabilities, and could route evaluation payloads to unrelated endpoints.

## Decision

AxonHub supports Jev through the explicit `typesafe/systemone` endpoint format and exposes `POST /v1/systemone`. The transformer keeps `state`, `questions`, answers, and unknown fields opaque, patches only the mapped top-level model, authenticates upstream with a Bearer channel key, and extracts TypeSafe input/output token usage into the unified usage model.

System One requests require a matching endpoint. They never fall back to a channel's primary chat endpoint. The endpoint is treated as non-message-shaped for tracing and does not create conversation traces. Streaming is rejected because the provider protocol is non-streaming.

This change intentionally uses existing channel and model association primitives. It does not add a TypeSafe channel enum, provider quick-create flow, Ent schema value, GraphQL type, or provider-side model-list proxy.

## Alternatives considered

- **Add a complete TypeSafe provider and channel type:** This would improve quick setup but would expand the change into channel enums, generated Ent/GraphQL code, provider UI, model discovery, icons, and migration compatibility. The requested scope was endpoint configuration support.
- **Reuse OpenAI Chat Completions:** Jev does not accept messages or generate chat text, so protocol reuse would be incorrect even if a gateway advertised an OpenAI-compatible wrapper.
- **Use a generic raw HTTP endpoint without an API format:** The orchestrator selects and validates capabilities by API format. An untyped raw endpoint could silently fall back to chat and would not provide stable request records, usage extraction, or model-protocol overrides.

## Consequences

Users can attach Jev to an existing channel with `typesafe/systemone`, a TypeSafe base URL, and a channel API key. Requests preserve the native TypeSafe contract and participate in model mapping, retry, request persistence, token accounting, and endpoint overrides.

The trade-off is that setup is manual: users must configure supported model IDs and model associations themselves, and AxonHub does not fetch the provider's `/v1/models` list. System One remains isolated from chat conversion by design.
