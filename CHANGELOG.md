# Changelog

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
