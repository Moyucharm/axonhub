## [Unreleased]

### Added

- Added per-channel API key rules with status/keyword matching, configurable error thresholds, temporary auto-recovery, and permanent disable/delete actions.
- Added per-window CPA quota value estimates for credential-wide spending windows: Codex 5-hour and 7-day usage headers, Claude `five_hour`/`seven_day` totals, and the xAI aggregate credits window. Model pools, per-app sub-limits, product windows, and request-count allowances (Antigravity pools, Kimi requests, xAI products/monthly balance) stay percentage-only because the aggregated cost is not their denominator.
- NeuralWatt channels now show the elapsed monthly energy window marker on the kWh bar and an estimated period quota, matching the other windowed providers.

### Changed

- The `/analytics` time range is now precise to the second. `AnalyticsFilter.startTime`/`endTime` take RFC3339 timestamps instead of `YYYY-MM-DD` strings, `analyticsMetadata.earliestTime` replaces `earliestDate` with the earliest usage instant, every range is filtered as an inclusive absolute instant down to the millisecond, and the page uses the shared date-and-time range picker. The trend chart still aggregates per local calendar day.

### Fixed

- CPA Codex reset credits are no longer marked as attempted when the local provider concurrency permit fails before any upstream call; a cancelled or unavailable permit leaves the card available for a later attempt.
- A failed CPA quota refresh no longer offers cached Reset credits: the row bubble follows the expanded panel and hides the action until a successful read.
- CPA quota estimates now price cache reads and cache writes with their own rates instead of dropping the write counter and double counting the read counter, and Claude windows use the wire's raw input plus both cache counters.
- CPA quota estimates no longer inherit another window's interval when two ungrouped windows share a period and reset.
- Dashboard analytics no longer rescan the full usage history for every widget: request, cost, and token widgets share one grouped query, expensive 30-day aggregates are cached, SQLite uses window functions instead of correlated `MAX(id)` scans, and off-screen sections load only when they approach the viewport.
- PostgreSQL fastest-model and fastest-channel resolver rankings use `DISTINCT ON` to select each request's latest successful execution before aggregation; SQLite retains the `ROW_NUMBER` path. `modelPerformanceStats` keeps `ROW_NUMBER` because `DISTINCT ON` measured a 5% regression without the covering index (not yet approved).
- Personal API Key names can be reused by different creators in a project; creating or renaming a key rejects collisions with non-personal keys visible to its creator. Duplicate checks run after create authorization.
- Synthetic weekly credit amounts that parse to NaN or infinity are now ignored instead of leaking into the period money fields and breaking quota persistence.

### Removed

- Wafer quota monitor removed — Wafer is PAYG-only. Legacy wafer quota rows are purged at startup before schema migration when automatic migration is enabled.

## [v1.0.0-beta12+azusa.v0.6.11] - 2026-11-04

### Fixed

- CPA Antigravity quota and subscription requests now use the official management UI's Hub client identity instead of the obsolete CLI 1.0.13 identity.

### Removed

- Removed the dedicated OpenCode Zen channel, request fingerprinting, tool injection, and forced streaming. OpenCode Go is unchanged.

### Migration

- With automatic database migration enabled, startup converts existing OpenCode Zen channels to ordinary OpenAI Chat Completions before schema migration. Legacy backup restores apply the same conversion.
- Migration preserves channel identity, Base URL, models, status, configured credentials, and unrelated settings. Existing model protocols are forced to Chat; Responses/Messages custom endpoints are removed. Credential-free channels explicitly retain `public`.
- Back up the database before upgrading. Ordinary OpenAI requests no longer apply Zen-specific URL rewriting or identity emulation; anonymous free access is not guaranteed.

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
