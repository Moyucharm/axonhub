## [Unreleased]

### Added

- Added per-channel API key rules with status/keyword matching, configurable error thresholds, temporary auto-recovery, and permanent disable/delete actions.
- Added per-window CPA quota value estimates for Codex 5-hour and 7-day usage headers and refresh-based Claude, Kimi, Antigravity, and xAI windows, limited to credential-wide windows (model-scoped sub-limits such as Claude Opus/Sonnet stay display-only), with legacy observation compatibility.

v0.4.0

- Introduced thread-aware tracing with zero-SDK integration and configurable trace headers
- Added trace visualization interface for following end-to-end conversations
- Added configurable data storage policies to keep or trim trace payloads based on compliance needs

v0.3.0

- Launched project workspace management with per-project API keys and dashboards
- Improved project-scoped permissions and usage insights

v0.2.0

- Added multimodal image generation via chat completions and image_generation tools
- Documented provider support and sample usage for image workflows

v0.1.1

- Add OpenRouter outbound transformer
- Support Google Gemini OpenAI compatible API
