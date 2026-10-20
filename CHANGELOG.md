# Changelog

## Unreleased

- Advertise MCP contract version 10 and explicitly report photo and face sign-in as unsupported capability domains.
- Document the agent control matrix, layered authorization/security invariants and independent notification fan-out behavior in the project README and architecture guide.
- Add password and gesture sign-in using an operator-provided sign code, including reference-compatible code verification, ordered submission, attendance verification and watcher presets.
- Add QR sign-in with local parsing of both reference payload formats, `signDetail` expiry verification, ordered `enc` submission and no QR URL fetching.
- Add the reference two-pass slider-captcha state machine with `get_sign_captcha` and `answer_sign_captcha`. Raw `enc2`, captcha keys and validation tokens stay in memory; the watcher never solves or blind-retries a challenge.
- Add optional course-group sign-in-card discovery. When enabled, class feeds and Easemob group cards are merged by stable course/class/activity identity for both `list_activities` and the watcher.
- Extend notifications with Gotify alongside Feishu webhooks and ntfy, and allow multiple channels in one watcher.
- Add per-tenant watchers with independent schedules, classes, coordinates, sign codes, notification configuration and private watch state.
- Add manual answer support: `list_work_jobs`, `get_work_questions`, local draft upsert/list/delete, and export of save/submit request payloads without upstream submission. Draft edits use the new `chaoxing.work` OAuth scope.
- Advertise MCP contract version 9 with 23 tools; `chaoxing.read`, `chaoxing.sign` and `chaoxing.work` remain separately grantable and refresh can only narrow scopes.
- Add `list_activities`, a bounded class activity feed covering sign-in and other classroom activities, with `kind`, `state`, `user_state` and a `signable` hint.
- Add `sign_activity`, a remotely callable normal/location sign-in operation with stable device identity, reference-compatible protocol ordering, preset coordinates, bounded jitter, automatic reauthentication and attendance verification. It requires the new `chaoxing.sign` OAuth scope.
- Extend the optional `watch` loop with per-class course schedules, preset locations, interval jitter and one-attempt automatic sign-in. Watch state and device identity persist in the private state directory; no new services, databases or scheduled jobs are created.
- Advertise MCP contract version 6 with 15 tools; `chaoxing.read` remains read-only and cannot invoke sign-in.

## 0.3.2 — 2026-10-18

- Unify account query admission across CLI and MCP with bounded waiting and cancellation; share a bounded process-wide outbound budget through response-body completion. Independent accounts keep independent sessions and can progress concurrently.
- Reject tenant state/key path aliases and overlaps, add standard path-insertion OAuth discovery, and document shared transport/health versus private account state precisely.
- Keep SQLite health storage on a short-transaction single-connection WAL path, with atomic migrations, escaped filenames, bounded busy/operation deadlines and checkpoint retention settings. Move health persistence outside the pool-wide mutex and expose degraded persistence without disclosing errors or credentials.
- Add offline burst, account/token isolation, slow-disk, response-lifecycle, cancellation, database integrity/reopen and WAL-write benchmark coverage. No authoritative state migration or new background services.

## 0.3.1 — 2026-10-18

- Queue local query bursts within the request budget instead of returning empty 429 responses; classify overload and non-JSON HTTP failures without exposing raw diagnostics.

## 0.3.0 — 2026-10-14

- Advertise MCP contract version 4: every tool carries a human-readable `title`, item-level output schemas for courses, assignments, exams, notices, action items, chapters and digest rows, and boolean argument support (`notices.list` gains `include_raw`, default `false`). `initialize` now returns operator instructions describing read-only scope, `meta.coverage` and untrusted notice prose.
- Allow anonymous MCP discovery (`initialize`, `ping`, `tools/list`, `server/discover`) so clients can inspect the tool catalog before completing OAuth; `tools/call` and every other method still require OAuth.
- Bind OAuth grants to the advertised resource and issuer: metadata declares `authorization_response_iss_parameter_supported`, authorization callbacks carry `iss`, token exchange verifies `resource`, and refresh tokens preserve it. Existing v2 state loads unchanged.
- Minimize notice payloads by default: `list_notices` no longer returns raw upstream envelopes or per-item raw fields unless `include_raw:true` is passed explicitly.
- Add an optional egress proxy pool: SOCKS5 endpoints with optional source-address binding, round-robin selection, transport-failure cooldown with exponential backoff, direct fallback and no background probing. Health persists in an embedded SQLite store (`<state_dir>/chaoxing.db`, WAL, mode 0600) created only when proxies are configured. The local management listener gains `GET /status` with sanitized egress health and tenant ids.
- Add optional multi-tenant serving: up to 32 isolated accounts under `/t/<id>/`, each with its own sealed credentials, OAuth state and lazy upstream client. The default single-account deployment is unchanged when `tenants` is empty.

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
