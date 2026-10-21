# Changelog

## 0.5.0-rc.36 — 2026-10-21

- Notify active classroom practice and homework items even when they are not rapid-answer claimable. These items now enter the watcher decision set only while active and unsigned, produce a review notification, and can never fall through to an upstream claim.
- Separate answer notifications from sign-in notifications: rapid answers say “答题进行中，尚未作答” instead of “尚未签到”; practice/homework alerts state that the question was published and that no automatic submission occurred.
- Fix `chaoxing watch windows` rejecting its single `windows` subcommand argument.

## 0.5.0-rc.35 — 2026-10-21

- Fix the two production pages that failed on a multi-course account. `list_action_items` now falls back to the bounded global assignment feed when no course is selected, and the todo page derives pending rows from one `list_assignments` response instead of issuing a second ambiguous scan.
- Add a course selector to classroom activities and always pass exact `course_id`/`class_id`; the default follows the current Asia/Shanghai schedule when possible, with the first non-retired course as a fallback.
- Keep tool failures as parseable JSON through Cloudflare. Ambiguous/input errors return 400, absent resources 404, access/auth errors 403, rate limits 429, and upstream or state failures 424 instead of origin 5xx responses that Cloudflare replaces with HTML.
- Map stable API error codes to Chinese recovery messages and fall back to `HTTP n` when an upstream or proxy response has no JSON body.
- Fix the work-answer crash after a successful AI draft: `answer_work` returns draft metadata, not a question array, so the page refreshes the existing question view instead of replacing it.
- Preserve local location presets and bindings when one catalog, schedule or map request fails; show a distinct read-failure state instead of clearing successful data. Members likewise keep the member list when the account summary fails.
- Clear stale study chapters/materials/schedule on course or week changes and label loaded cards with the active course/week.
- Reduce capture write amplification with a conditional UPSERT: identical observations no longer rewrite a row or increment `update_count`; a five-minute heartbeat preserves prune semantics.
- Give automatic sign-in a 30-second action budget and add a 20/minute global login cap. Move media to a four-slot bounded pool with a short wait, and take captures off the tool-call pool.

## 0.5.0-rc.23 — 2026-10-21

- Close the control-plane session fail-open: authenticated requests now carry an explicit server session record, every handler re-checks that the record still exists, and a missing/deleted session can never become `owner` or receive all account grants. Zero-value roles are audited as `anonymous`, never as the owner.
- Bound session abuse per identity. Each member has at most six live sessions; owner/admin logins may replace their own oldest session, and an owner login can evict the lowest-privilege session instead of being locked out by 32 viewer sessions. Lower roles still receive `429 SESSION_LIMIT` at their own cap.
- Add security audit for previously silent denials: account-selection scope, capture/sign-audit permission, owner/admin protection, self role/status changes and administrator escalation attempts now produce bounded `security` events.
- Attribute MCP and loopback tool execution to the control-plane audit. OAuth calls record the requesting client fingerprint, local `/call` records `local`, and every allowed tool call records account, tool, success/failure, safe error code and duration without arguments or response bodies.
- Require a 32-character minimum local management bearer token at startup, so an empty token cannot authenticate `Bearer `.
- Move member password hashing outside the SQLite write transaction, read committed member rows with a detached short context, marshal JSON before writing response headers, and make random-token generation fail closed.
- Use `synchronous=FULL` for the authorization-bearing `control.db`; reconstructible tenant health/capture state keeps its existing policy.
- Make session validation fail closed at the shared exit: control-plane sessions must carry a positive user ID and auth version, admin/capability checks revalidate account scope, and a transient `control.db` outage preserves the browser session and returns `503` instead of logging the operator out.
- Re-check member authorization inside the SQLite write transaction. An administrator can no longer modify a target that was promoted to administrator between the handler read and the update, and own-role/self-status changes are rejected at the store boundary as well as in the HTTP handler.
- Rebuild the management UI around a Linear-style, dark-first component system: semantic surface/token ladder, one indigo accent, compact controls, hairline borders, no backdrop blur, no decorative shadows and no color gradients. Theme variables now live on `<html>`, avoid a body/root fork and preserve `color-scheme`.
- Refactor every page to a shared `PageHeader` with a single `h1`, add `Alert`/`Checkbox` primitives and use the same card, table, input and focus treatment throughout. The shell now has a compact breadcrumb header, desktop-only theme control, mobile account selector, mobile page selector instead of a 14-item horizontal strip, a first-focus skip link and a hidden-by-default edit panel below 1536px.
- Finish the login and shell accessibility pass: no hard-coded owner username, no login password-policy hint that only applies to member creation, one page heading, explicit permission-unloaded states instead of an owner fallback, and visible keyboard focus targets on all controls.

## 0.5.0-rc.22 — 2026-10-21

- Add a minimal multi-member control plane without changing tenant state schema. `StateDir/control.db` stores platform users, explicit account grants and control-plane audit events; upstream credentials, cookies, coordinates and sign-in evidence remain in each tenant's private state store.
- Add four fixed roles. `owner` bootstraps from the existing deployment password hash and is the only super-admin; `admin` manages operators/viewers and all accounts; `operator` gets read/sign/work/answer/upload on explicitly granted accounts; `viewer` is read-only, cannot read captures or sign-in IP/address audits, and cannot enter admin APIs.
- Add the Members and Permissions page and Audit page to the embedded Web UI. Password, role, status and grant changes increment `auth_version`, invalidating existing sessions on their next request. Password hashes remain PBKDF2-SHA256 with 600,000 iterations.
- Keep the owner password under deployment-config control because the same hash guards OAuth consent. Browser-side member password resets cannot change the owner credential or diverge the two authentication paths.
- Add a sanitized, read-only account-policy view for administrators. Secrets, proxy URLs, notification tokens, upstream credentials and coordinates never round-trip through the browser.
- Audit login success/failure, logout, account switching, member and grant changes, tool calls, permission denials, and location preset/binding writes. Details are bounded JSON with no passwords, tokens, question bodies or page text; retention is 180 days and pruning runs at startup.
- Keep control-plane writes on the same origin/CSRF/Host boundary as the existing UI, enforce a single validation path in Go, and reject client-selected accounts outside the member's grant set before dispatching any tool.

## 0.5.0-rc.21 — 2026-10-21

- Fix the ordinary-sign-in regression that survived rc.20: a disabled location service now produces a nil resolver instead of a non-nil wrapper, so overlapping timetable rows no longer suppress automatic sign-in when no per-schedule location binding exists. Add a watcher regression test for the exact assembled-service shape.
- Keep DCR from monopolising owner authentication. Public registration now has its own concurrency and burst budget, plus an in-memory per-network pending and request ceiling; `/oauth/authorize`, `/oauth/login` and `/oauth/consent` retain their independent interactive budget.
- Show the complete callback URI, not only its origin, on the OAuth consent page and mark write/biometric scopes as high risk so the owner can recognise consent phishing.
- Reject direct `chaoxing query` submissions of `sign_activity` and `sign_activity_with_face`; sign-in must go through the daemon, where every attempt is written to `sign_audits`.
- Fix a dashboard privacy bug where switching accounts could leave the previous account's sign-audit rows and status visible. Account-scoped pages now remount before the switch request, stale responses are ignored, and the audit request has independent loading/error states instead of treating failure as an empty history.
- Add shareable `?page=` navigation with browser back/forward support, keep the active mobile navigation item in view, and raise Nova light-theme primary/success/warning/destructive contrast to WCAG AA for normal text.

## 0.5.0-rc.20 — 2026-10-21

- Make the sign-in audit operational end to end: successful and failed manual/CLI/MCP and watcher attempts persist the submitted address and jittered coordinates in `sign_audits`, probe the same account-sticky egress path for the observed public IP, return those fields on the sign result, and include the address/IP in success and failure notifications. The private state store is now opened even when watcher, capture and proxy features are disabled so manual sign-ins cannot bypass the audit.
- Bound automatic sign-in retries. Transient upstream failures now use a 30s/2m/5m/10m backoff with at most four attempts, persist the verified/retry state before sending a notification, and keep retry rows from being pruned in the same tick. Successful sign-ins can no longer be repeated by a notification timeout or process interruption.
- Only block automatic sign-in for overlapping schedule windows when a per-schedule location resolver is configured; ordinary sign-ins and class-level locations no longer get skipped.
- Classify upstream HTTP status correctly: 429 as retryable rate limiting, 5xx as retryable upstream unavailability, and ordinary 4xx as non-retryable rejection.
- Report a partial group sweep as partial in `list_activities`; a single unreadable group, the six-group cap or the byte budget can no longer be hidden behind `complete:true`.
- Fix cross-platform release builds by adding a Windows `LockFileEx` state-directory lock and making Unix ownership/mode checks conditional. The Windows upload path no longer fails on POSIX mode checks.
- On watcher shutdown timeout, leave the state store open for the still-running watchers and return a shutdown error instead of marking a clean exit.

## 0.5.0-rc.19 — 2026-10-21

- Put sign-in and answer decisions on the critical path: class discovery now processes actionable rows immediately, before page capture or the slower group fallback. The tick has a 60s discovery budget, a 30s group-scan budget and a 150s overall supervision budget; exhausting discovery no longer discards already-discovered actions.
- Run the group fallback before page evidence capture, cap page capture to one page per tick and cap page HTML at 1 MiB before parsing. This keeps a slow or oversized H5 page from delaying a group-discovered sign-in or the next polling cycle.
- Refuse automatic location selection when multiple schedule windows overlap, instead of silently choosing the first configured room. Overlapping schedules fall through to an operator alert.
- Retry transient sign-in failures (`UPSTREAM_UNAVAILABLE`, rate limits and auth cooldown) on the next tick, while still treating upstream business rejections as terminal. Bound each activity notification to 8s so a slow push cannot consume the remaining sign-in window.
- Quarantine corrupt or unsupported watch state to a timestamped `.corrupt-*` file, start with empty in-memory suppression state and expose `state_degraded` through ready checks instead of entering a procd restart loop.
- Keep the six-hour disappearing-activity prune and restore the independent 24-hour hard ceiling, so an upstream row that stays in the feed cannot grow the private state file indefinitely.
- Join watcher goroutines during SIGTERM, wait up to 10s before closing the state store, and only write the supervisor clean marker after watchers have stopped. Raise the procd `term_timeout` to 20s.
- Fix the `workdraft` data race: answer/export now copy question maps while holding the store lock and never iterate shared maps after unlock. Add a concurrent `-race` regression test.
- Harden OAuth login availability: two-minute login nonces, a per-network pending cap, per-network login windows, a 60/minute global ceiling and a single PBKDF2 verification slot. Add CSP/COOP/XFO/Permissions/HSTS/robots headers to OAuth pages and remove the unnecessary external consent `form-action`.
- Count only actual password-verification failures against the OAuth global failure ceiling, clear a network's failure bucket after a successful login, and give `/oauth/token` and `/oauth/revoke` a separate concurrency/burst budget so anonymous login or registration traffic cannot starve them.
- Strip query strings and fragments from activity URLs returned by tools and stored in captures, while retaining the internal raw URL only for the one-shot page evidence read. Redact `enc`/`ktoken`/`cpi` and other session-bound work-form values from public question results; full values remain available only to the local draft exporter.
- Check and tighten private upload staging: require a real `0700` uploads directory, reject group/world-readable files, verify service ownership on Unix, and delete the staged file after a successful pan upload.
- Reject `0,0` as a location preset and make the management console distinguish new versus editing state, so an empty coordinate field cannot silently become Null Island or overwrite an existing preset.
- Catch page-level render failures with a retryable error boundary, and remove the Dashboard watcher TDZ crash by computing staleness before the first use.
- Cap a group-activity sweep at six groups and 2 MiB, retry a failed sweep after 30s instead of waiting the full cadence, and unify the answer cache with the atomic `persist.Write` path.
- Set the process local zone to fixed UTC+8 so OpenWrt logs and backup timestamps agree with the already-fixed Asia/Shanghai scheduler.
- Add a private `sign_audits` SQLite table (schema v5) and a Web UI audit table. Every watcher or manual sign-in records the submitted method, result/code, address and jittered coordinates, plus a best-effort public egress IP obtained through the same sticky egress path.

## 0.5.0-rc.18 — 2026-10-21

- Add an embedded SVG favicon and a hidden username field to the owner login form, removing avoidable browser 404/accessibility console noise.

## 0.5.0-rc.17 — 2026-10-21

- Isolate the optional Baidu GL map in a sandboxed `/map` iframe with an opaque origin. The authenticated application CSP no longer trusts any third-party origin; the map receives only its AK and selected point over `postMessage`.
- Split WebUI admission into a light control-plane semaphore (8) and a heavy capture/upstream semaphore (2), so a slow capture query or MCP tool call cannot starve login, session and status requests on the router.

## 0.5.0-rc.16 — 2026-10-21

- Make every watcher schedule a week-aware, independently keyed occurrence. Polling now defaults to 20 minutes before through 20 minutes after class in Asia/Shanghai, supports explicit stable keys for regular/guidance collisions, and projects the exact same windows through `watch windows`.
- Restore `watch.auto_answer` propagation through the daemon/tenant assembly boundary; the flag can no longer be silently dropped while the answerer is constructed.
- Serialize `get_activity_page` through the per-account query gate, eliminating a race with the shared cookie/cache/session state used by reads, sign-in and answer calls.
- Capture the active schedule key before automatic sign-in delay, so a delayed submission cannot cross the post-class boundary and fall back to the wrong location.
- Make schedule binding resolution try the exact week-qualified key, then the legacy pre-week alias, then the class-wide default; add a strict account filter to capture retention.
- Harden runtime state: single-instance state-directory locks, unique temporary state files with directory fsync, one tick at a time, bounded tick execution, watch-state size limit, completion-based health timestamps and `/readyz`.
- Reduce public health disclosure (`/healthz` now exposes only liveness/version) and keep detailed PID/tick data on the authenticated dashboard and loopback management listener.

## Unreleased

- Extend `chaoxing courses discover` to query both mooc2 course tabs (`courseType=1` and `courseType=0`) and report the source of each row, so a course that only appears in the unfiltered tab is still found.
- Add `chaoxing watch windows` to project the exact polling/sign-in windows (lead time included) from the live schedule, making it possible to compare configuration with the official timetable without waiting for a class.
- Add `chaoxing courses discover`, a read-only probe of the mooc2 course-list page (`mooc2-ans/visit/courselistdata`) that marks each course as present in the legacy `backclazzdata` feed and as currently watched. It explains why a course that exists in the timetable may be absent from `list_courses` without entering the course.
- Add `chaoxing watch classes list|add|remove` for the static watcher class list. It rewrites only `watch.classes`, preserves every other configuration field, validates days/times, caps the list at eight classes, keeps a timestamped backup and prints the resulting classes; the daemon must be restarted to apply.
- Capture sanitized H5 evidence for classroom homework activities (type 19) in addition to practice (type 42), so later submission-protocol work has both page shapes.
- Notify classroom practice and homework activities with their own titles instead of labelling every answerable activity as a rapid answer.
- Poll once immediately after the watcher starts instead of waiting for the first idle-sleep interval, so a deploy or crash respawn during class does not delay the first observation. Outside the configured window the tick is a no-op.
- Add `chaoxing location` for private coordinate presets on the deployment host: `list`, `add`, `update`, `delete`, `bind`, `unbind` and `resolve` write only to the account's SQLite store, so real campus coordinates and building labels never enter the repository, MCP tools or Web UI payloads.
- Add a staleness signal to operational health. `/healthz` now reports `stale` and `now`, and the authenticated dashboard marks a watcher whose polling loop has not advanced for twenty minutes (three times the idle sleep cap) while taking clock skew into account.
- Add process-level crash supervision across procd restarts. A private 0600 marker distinguishes SIGTERM-initiated restarts from unexpected exits, persists consecutive crash count and last crash time, sends asynchronous priority 9/10 Gotify/ntfy/Feishu alerts (three bounded attempts) on every abnormal respawn, exposes safe process metrics through `/healthz` and the authenticated dashboard, and resets the consecutive chain only after a clean shutdown. A corrupt or unwritable marker degrades to in-memory counters instead of blocking startup.
- Harden watcher supervision and idle polling: every polling tick is panic-recovered and counted, panic alerts carry consecutive counts, successful ticks reset the counter, polling sleeps up to five minutes outside active class windows, and course-group feeds are scanned only while a watched class is active.
- Add strong ETags, `Vary: Accept-Encoding` and precompressed gzip responses for immutable embedded WebUI assets. Identity, HEAD and range requests still work through `http.ServeContent`; conditional requests now return empty 304 responses.
- Raise notification transport limits for consumer uplinks: a shared HTTP transport now allows a 20-second TLS handshake and 20-second response header wait, and alarm paths retry transient delivery failures while ordinary activity notifications stay single-attempt.
- Harden the public WebUI trust boundary: API Host validation, trusted-proxy-only `X-Forwarded-For` parsing, client-network session binding, per-network login limits and a 32-session cap.
- Add private BD-09 location presets, course/schedule bindings and an optional Baidu GL map page. Coordinates live in schema-v4 SQLite, manual input is normalized from BD-09/GCJ-02/WGS-84, geocoding is daemon-proxied, the map loads only on the Locations page, and the watcher resolves schedule -> class bindings before the existing activity-range check and one tightened location retry.
- Add the bounded classroom answer agent. When `watch.auto_answer` is enabled together with `watch.classroom_agent`, rapid-answer claiming now runs through an explicit decision process: a scope gate rejects `homework`/`practice`, a deterministic planner chooses claim/notify/skip, generated answers must pass normalization and a confidence gate, and low confidence, missing providers, oversized question sets or parse failures become operator-review notifications instead of upstream writes. `notify_only` keeps every decision advisory and is recommended for production; ordinary coursework remains draft-only and can never be auto-submitted.
- Add the NewAPI chat-completions answer provider. `answer.providers[].type:"newapi"` takes an API-root `base_url` (the provider derives `/v1/chat/completions`), requires an explicit model and key source, sends `max_tokens:768` for reasoning-first models, and keeps the existing JSON contract, normalization, cache and fallback chain.
- Add an official-SDK OpenAI Responses answer provider. `type:"responses"` performs one stateless, non-stored, bounded call per question through `openai/openai-go`, reuses the existing answer cache/normalization/fallback chain, and requires an explicit API-key source plus an API-root `base_url`.
- Raise the Gotify client timeout from 10 to 30 seconds so slow-but-successful mobile pushes from high-latency home-router deployments are not reported as notification failures.
- Add an embedded React + Tailwind/shadcn-style owner management console at `/`. It ships inside the Go binary, logs in against the existing PBKDF2 owner hash, uses `__Host-` HttpOnly Strict session cookies, CSRF double submission, login rate limits, CSP/security headers, bounded JSON APIs and the same operation allowlist as MCP. The UI covers dashboard/runtime status, courses, assignments, schedule/chapters/materials, classroom activities, work-question/answer-draft flow, notices, exams, production captures, all tools and security settings.
- Harden and complete the owner console: sessions bind to the login remote address and are capped at 128 active entries, HTML/API responses add HSTS/permissions/COOP headers, hashed assets cache immutably, capture page JSON is fetched only through an explicit detail action, course-class selection uses a composite key, assignment rows can open questions and generate local drafts, schedule/chapter/material reads have a dedicated page, sign-in forms collect sign codes/location/QR/photo parameters, tool forms validate numeric and required inputs safely, and CI builds and verifies the embedded bundle. The UI remains same-origin only with no analytics or third-party tracker.
- Add per-account sticky egress. Requests labeled with an account keep one healthy SOCKS5 endpoint across restarts, use direct only while that endpoint cools, and never rotate one account across every request. Proxy assignments persist in SQLite and invalid configured endpoints are cleaned automatically.
- Add upstream request pacing (default 650 ms minimum with 35% random jitter), randomized delays between class polls (750 ms–3 s) and before automatic sign-in (5–25 s). This removes fixed-interval and same-second burst patterns from watcher and agent chains.
- Add private production activity capture for watcher feeds. Activity identity and sanitized activity/page projections are retained for a configurable period (default 168 hours); hidden values, cookies, credentials and raw HTML are never stored. Practice activities can automatically capture sanitized H5 structure for later submission-protocol reconstruction.
- Upgrade the default new-device profile to Android Chaoxing 7.0.4 / versionCode 10995. Existing device state is deliberately preserved so a running account does not suddenly appear to change phones.
- Add notification priority propagation. Auto-sign failures use priority 8 and repeated unresolved reminders use priority 9 for ntfy/Gotify, while normal activity alerts remain less intrusive.
- Add `get_activity_page` as a read-only evidence collector for unverified classroom H5 flows. It follows only a URL already returned by the verified activity feed and reports final URL, bounded text, iframe/script/link URLs, form actions and field names/types; hidden values and raw page bytes remain withheld.
- Add `get_assignment_questions`, a read-only bridge from the global assignment feed into the editable work page. It follows the signed `work/task` entry once, accepts the upstream `work/dowork` redirect, parses the new `questionLi`/`mark_name` and `stem_answer` templates plus hidden `answertype` fields, caches the resulting job for `answer_work`, and reports read-only preview/prompt states as `WORK_UNAVAILABLE`.
- Add the answer-generation and coursework-submission loop: configurable OpenAI-compatible LLM and TikuAdapter-style providers, local answer cache, question-type normalization, `answer_work`, `save_work` and final `submit_work` behind the new `chaoxing.answer` and `chaoxing.work.submit` scopes. Final submission requires every question to have a draft.
- Add classroom rapid-answer discovery and participation: activity types 4/19/42 normalize to `answer`/`homework`/`practice`, `answer_activity` claims active type-4 activities through the reference `stuAnswer` endpoint, and the opt-in watcher setting `auto_answer` claims each new rapid answer once.
- Add photo sign-in, pan upload, face enrollment and headless face sign-in. Uploads are restricted to filenames under each account's private uploads directory; face operations use the new `chaoxing.upload` and `chaoxing.face` scopes.
- Add a no-network local slider-captcha solver using the shade/cutout image pair; DamaGou remains available as an external fallback.
- Advertise MCP contract version 14 with 32 tools and seven separately grantable OAuth scopes. Classroom-practice submission remains explicitly unsupported until its H5 submission contract is verified.
- Reject partially configured ntfy or Gotify notification channels at startup so a mistyped alert destination cannot silently disable one channel.
- Advertise MCP contract version 11, remove the obsolete automatic-captcha-solver unsupported marker, and keep capability discovery explicit about unverified classroom-practice submission.
- Document the agent control matrix, layered authorization/security invariants and independent notification fan-out behavior in the project README and architecture guide.
- Add password and gesture sign-in using an operator-provided sign code, including reference-compatible code verification, ordered submission, attendance verification and watcher presets.
- Add QR sign-in with local parsing of both reference payload formats, `signDetail` expiry verification, ordered `enc` submission and no QR URL fetching.
- Add the reference two-pass slider-captcha state machine with `get_sign_captcha` and `answer_sign_captcha`. Raw `enc2`, captcha keys and validation tokens stay in memory; the watcher performs at most one solver-backed retry per sign attempt.
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
