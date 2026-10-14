# Changelog

## 0.2.3 — 2026-10-14

- Reserve visible assignment, exam and notice slots in `study.digest`, so a long assignment list cannot crowd out the other available digest sections.

## 0.2.2 — 2026-10-14

- Add `study.digest` / `get_study_digest`, a bounded read-only digest combining assignment action items, normalized exam rows and trimmed notice summaries without inferring tasks from notice prose.
- Keep OpenWrt procd services respawning indefinitely with a 10-second delay and one-hour threshold, matching the hardened router deployment and avoiding startup dependency races.
- Advertise contract version 3 and 13 verified read-only tools.

## 0.2.1 — 2026-10-13

- Fix `list_chapters` against the live service: the upstream chapter chain depends on its own redirects, so allowlisted redirects are followed again while learning, logging, task-entry and API paths stay blocked. The previous build refused every redirect and failed before parsing.
- Log only a fixed endpoint label, status and redirect count for a rejected chapter response, so diagnostics carry no raw URL or request signature.

## 0.2.0 — 2026-10-13

- One operation registry drives MCP and CLI input validation, discoverable schemas and additive coverage metadata; retain legacy tool names and fields.
- Add daemon-backed domain commands, capability discovery, bounded study overview and source-backed assignment action items.
- Add pure-HTML chapter summaries and explicitly reported aggregate task-point counts, without entering learning pages or fetching task cards.
- Support exact course/class selection, conservative submission states and partial source failures; improve exam and notice result structure without starting exams or marking messages read.
- Declare MCP output schemas and account-bounded annotations; safe structured errors share transport-independent codes.
- Successful JSON stays on stdout; complete CLI failures move to stderr and partial results return exit code 3.
- Publish new release tags instead of overwriting existing release artifacts. Group messages, inbox and task points remain explicitly unsupported until safe read endpoints are verified.

## 0.1.2 — 2026-10-12

- Keep public OAuth client registrations for 90 days across setup retries. A registration does not grant access: owner consent, PKCE and exact redirect matching remain required. The 128-client pool still evicts unapproved registrations first to prevent anonymous capacity exhaustion.

## 0.1.1 — 2026-10-12

- Fix browser owner login: use a same-origin referrer policy so Chromium sends the real Origin for form POSTs rather than `null`. Cross-origin referrers remain suppressed; origin validation and CSRF checks remain enforced.

## 0.1.0 — 2026-10-12

- Public English and Chinese product documentation, tool contracts, configuration and security boundaries.
- Documented verified per-course signed work-list pagination and conditional count-agreement coverage, while retaining the global feed's incomplete status.
- Offline tests for upstream parsing, encrypted state and authorization rejection paths.
- CI for Go tests, race detection, vet, formatting, build, dependency vulnerability scanning.
- CI and release toolchains resolve the latest Go 1.26 patch; build documentation requires Go 1.26.9 or newer in that series.
- Tag-triggered Linux arm64/amd64 and Windows amd64 release builds with SHA256 checksums.
- Automatic upstream login and renewal; AES-GCM encrypted credentials and sessions with a separate local key.
- OAuth read-only authorization with PKCE, short-lived pending registrations, issuer-bound refresh proofs and bounded per-family rotation state.
- Consent pages display the real callback origin and client identifier, not just a self-reported name.

### Known limitations

Global assignment feed coverage remains incomplete. Per-course completeness is scoped to matched classes and conditional on upstream count agreement. Notices and materials also have incomplete coverage. MCP callbacks are limited to 30 seconds; use specific course queries to keep multi-class scans bounded.
