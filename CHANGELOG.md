## [Unreleased]

### Added

- Added per-channel API key rules with status/keyword matching, configurable error thresholds, temporary auto-recovery, and permanent disable/delete actions.
- Added per-window CPA quota value estimates for credential-wide spending windows: Codex 5-hour and 7-day usage headers, Claude `five_hour`/`seven_day` totals, and the xAI aggregate credits window. Model pools, per-app sub-limits, product windows, and request-count allowances (Antigravity pools, Kimi requests, xAI products/monthly balance) stay percentage-only because the aggregated cost is not their denominator.
- NeuralWatt channels now show the elapsed monthly energy window marker on the kWh bar and an estimated period quota, matching the other windowed providers.

### Fixed

- CPA Codex reset credits are no longer marked as attempted when the local provider concurrency permit fails before any upstream call; a cancelled or unavailable permit leaves the card available for a later attempt.
- A failed CPA quota refresh no longer offers cached Reset credits: the row bubble follows the expanded panel and hides the action until a successful read.
- CPA quota estimates now price cache reads and cache writes with their own rates instead of dropping the write counter and double counting the read counter, and Claude windows use the wire's raw input plus both cache counters.
- CPA quota estimates no longer inherit another window's interval when two ungrouped windows share a period and reset, and OpenCode Zen protocol badges/colors and the channel edit echo now follow the explicit per-model protocol before falling back to the endpoint list.
- Personal API Key names can be reused by different creators in a project; creating or renaming a key rejects collisions with non-personal keys visible to its creator. Duplicate checks run after create authorization.

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
