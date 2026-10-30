# Configuration and tunnel setup

## Assignment notifications

When the optional watcher is enabled, ordinary assignments are scanned for its
configured classes as well as time-sensitive classroom activities. During a
class window, both feeds for that course/class run each tick after participation
decisions. Outside class windows, at most one due class is scanned per tick,
with at least five minutes between its scans. A multi-class idle sweep may
take several ticks. The global watcher window still applies.

The first complete scan of each class establishes a persistent baseline without
announcing historical assignments. New verified pending assignments are combined
into one reminder per class; closed/not-open work is silent and unknown work is
not called pending. Partial or failed scans never replace the baseline. Failed
notifications remain eligible for the next complete scan; accepted reminders are
remembered across restart. This never generates, saves or submits answers.

Private `/status` watcher metrics include aggregated activity/assignment feed
attempts, failures and last-success timestamps. Three consecutive failed scans
degrade `/readyz`, independently of loop completion; a successful scan recovers
the corresponding feed. Inactive classroom windows clear their transient failure
streak, while ordinary assignment monitoring remains active within the global window.

Build the single binary with the Go version in `go.mod`. `CHAOXING_CONFIG` selects the private config file; its default is `/etc/chaoxing/config.json`. `configure` refuses to replace an existing configuration implicitly.

## Initialize private state

```sh
chaoxing configure --state-dir /var/lib/chaoxing --key-file /etc/chaoxing/secret.key --issuer https://mcp.example.com
```

Supply a JSON object on stdin using a trusted private input mechanism. Required fields are `phone`, `password` (upstream login) and `owner_password` (a distinct owner OAuth credential, at least 16 characters). An optional `owner_username` selects the platform owner login name; it defaults to `owner` and accepts 3–32 lowercase letters, digits, dots, dashes or underscores. Do not type actual credentials into command arguments, shell history, issue reports, examples or tracked files. No working credentials are shipped in this repository.

The generated config records `state_dir`, `key_path`, `issuer`, `listen`, `local_listen`, `local_token`, `owner_username` and `owner_password_hash`. On startup the configured owner username and password hash are reconciled onto the existing owner record, so an operator can rename the owner or rotate the password by editing only the private config and restarting; every existing Web UI session is invalidated. `local_token` must contain at least 32 characters; startup refuses an empty or short management token. Treat the whole config as private because it includes the local bearer token. The state directory holds sealed upstream credentials, sealed cookie state and owner OAuth state. Keep the encryption key separately from state backups; a backup containing both can be decrypted by whoever reads it.

On Linux, use a root-owned private state directory (0700), config and state files (0600), and a separate root-only key (0600). The code creates private files but does not establish your OS account ownership or fix permissions of an existing key. Verify them before starting. On Windows, apply equivalent restrictive ACLs; Unix mode bits alone do not provide that boundary.

Run `chaoxing serve` under the trusted service identity. Keep one daemon per state directory: startup takes an exclusive `flock` on `<state_dir>/.chaoxing.lock` and refuses a second process. The local identity running `chaoxing call` must have permission to read the private configuration; do not grant untrusted clients access to the vault key.

## Egress proxy pool and request pacing

Upstream requests use direct egress by default. To route through SOCKS5 endpoints, add a private `proxies` array to the config (each entry has `url` (a socks5 URL with optional userinfo) and optional `bind` (a local source IP); at most 16). Selection is account-sticky: each root/tenant account is bound to one healthy endpoint and keeps that assignment across restarts. Transport failures cool the selected endpoint with exponential backoff (30 s up to 5 min); while it cools, requests use the direct fallback, then return to the configured assignment when healthy. There is no background probing. Endpoint health, sticky assignments and the private sign-in audit persist in `<state_dir>/chaoxing.db` (SQLite, WAL, mode 0600); the store is opened for every account so manual sign-ins are auditable even when watcher, capture, proxy and location features are disabled. Proxy URLs contain credentials: keep the config private and never log them; status output identifies endpoints by a truncated hash only.

Add a top-level `request_pacing` object (tenants may override it) to avoid fixed-interval and same-second bursts. `min_interval_ms` defaults to 650 and accepts 0–60000; `jitter_percent` defaults to 35 and accepts 0–50. The interval applies to redirects and retries as well as ordinary upstream calls.

```json
{
  "request_pacing": {
    "min_interval_ms": 650,
    "jitter_percent": 35
  },
  "proxies": [
    {"url": "socks5://127.0.0.1:1080"}
  ]
}
```

For a stable residential route, configure it once and let each account remain bound to it. Do not rotate one account randomly across datacenter and residential exits.

## Optional additional tenants

The default deployment serves one account at `/mcp`. To serve additional accounts, add a private `tenants` array to the config; each entry needs `id` (`[a-z0-9][a-z0-9-]{0,31}`, unique), `state_dir`, `key_path` and `owner_password_hash` (at most 32). Use sibling account-state directories, not nested ones. Startup resolves relative components and existing symlink ancestors, rejects overlapping default/tenant state directories, shared key paths or existing hard-linked keys, and keys located in another account's state. This intentionally rejects ambiguous layouts accepted by older releases; move state only while its owner is stopped and preserve a recovery copy.

A tenant mounts a full OAuth + MCP stack under `/t/<id>/` with issuer `<issuer>/t/<id>`, its own sealed credentials (initialize with `chaoxing configure` pointed at a tenant-scoped config, then merge the tenant fields) and OAuth state. Upstream clients are created lazily. The private CLI remains default-account-only; the local bearer is an operator identity, not a tenant selector. Cookie jars, caches, credentials and tokens are independent, while transport connections, endpoint health and outbound capacity are shared. See [resource and durability design](architecture.md#concurrency-and-resource-budgets).

Discovery supports both existing `/t/<id>/.well-known/...` routes and standard path-insertion routes: `/.well-known/oauth-authorization-server/t/<id>` and `/.well-known/oauth-protected-resource/t/<id>/mcp`. Publish these discovery paths through the same HTTPS origin, along with the tenant's OAuth and MCP paths. This does not make the private management listener public.

## Publish only the MCP listener

Default listeners are `127.0.0.1:8792` (public Web UI, OAuth and MCP behind the tunnel) and `127.0.0.1:8793` (private CLI). Both stay bound to loopback. The issuer must match the public HTTPS origin.

Use this ingress fragment in your own private Cloudflare Tunnel configuration:

```yaml
ingress:
  - hostname: mcp.example.com
    service: http://127.0.0.1:8792
  - service: http_status:404
```

Keep tunnel credentials outside this repository. Never add an ingress rule or port forward for 8793, and never expose both listeners. Configure the MCP client with `https://mcp.example.com/mcp`, then complete owner login and consent. Read tools require `chaoxing.read`; answer generation requires `chaoxing.answer`; sign-in requires `chaoxing.sign`; work submission requires `chaoxing.work.submit`; upload requires `chaoxing.upload`; face operations require `chaoxing.face`.

Dynamic client registration (DCR) is enabled by default. CIMD is opt-in with `CHAOXING_ENABLE_CIMD=true`; enable it only after verifying that the host can fetch HTTPS metadata from `chatgpt.com`. Discovery advertises the selected capability truthfully. DCR does not require fetching that external metadata.

OAuth state v2 uses a separate `oauth-refresh.key` to authenticate refresh proofs. Keep it with the private OAuth state when backing up; losing or replacing it invalidates refresh families. Pre-release v1 state is not migrated silently. Stop the service, preserve a recovery copy, and explicitly reauthorize clients when initializing v2.

Before use, verify that listeners are loopback-only, 8793 has no public route, and an unauthenticated MCP request cannot call tools.

## Owner Web UI

Open `https://mcp.example.com/` in a browser and log in with the same owner password used for OAuth login. The Web UI is embedded in the Go binary; no Node runtime is needed in production.

If a non-loopback reverse proxy terminates the tunnel locally, declare only its exact CIDR in the private top-level config:

```json
{
  "trusted_proxies": ["127.0.0.0/8", "::1/128"]
}
```

- Session cookies use the `__Host-` prefix, `Secure`, `HttpOnly` and `SameSite=Strict`; sessions last eight hours absolutely and thirty minutes while idle.
- Sessions are bound to the originating client network: IPv4 `/24` and IPv6 `/64`. A replay from another network is rejected and invalidated; a mobile address change inside the same network does not force a re-login.
- All browser requests must use the configured issuer Host. Invalid Host headers are rejected with `INVALID_HOST`.
- By default only `127.0.0.0/8` and `::1/128` are trusted proxies. A trusted immediate proxy may supply `X-Forwarded-For`; the service reads it right-to-left and ignores it for direct clients. Add a private top-level `trusted_proxies` array only for the exact reverse-proxy CIDRs in front of the listener.
- Active sessions are capped at 32 and expired sessions are pruned during login.
- Every write request uses CSRF double submission and an origin check.
- Login is limited per client network to five attempts per minute and twenty per fifteen minutes.
- The browser API reuses the operation allowlist. It cannot fetch arbitrary URLs, read arbitrary files, run commands or expose cookies.
- The static bundle has no analytics or tracker. Its APIs are same-origin; the optional Baidu map page is the only opt-in third-party script and loads only when configured. Hashed assets are cached immutably; HTML and API responses are not cached.
- Capture page evidence is omitted from list responses and fetched only through the explicit page-detail action.
- Root and each configured tenant appear as account contexts. The UI can switch between their isolated executors.
- Coursework submission and sign-in actions require explicit confirmation in the interface.

Rebuild the embedded bundle after changing `web/src`:

```sh
npm --prefix web install
npm --prefix web run build
go build ./cmd/chaoxing
```

## Persistent device identity

The first startup creates `<state_dir>/device-state.json` (0600) with a stable randomized device code, Android-like device identifiers, the configured user agent and package name. New profiles default to Chaoxing Android 7.0.4 / versionCode 10995. The file is not a credential, but keep it private and stable: changing it makes the account appear to move to a new device; existing files are not automatically upgraded. A private top-level `device` object can pin these values:

```json
{
  "device": {
    "code": "base64-device-code",
    "user_agent": "Dalvik/2.1.0 ...",
    "package_name": "com.chaoxing.mobile"
  }
}
```

Omit any field to use its persisted or default value. `code` must be standard Base64 (max 512 bytes); `user_agent` must be printable ASCII (max 1024 bytes); `package_name` must be a valid Android package name. The default UA and package match the reference Android client. Device info sent to the account-identity endpoint is RSA-encrypted; the sign endpoint receives the persisted `deviceCode`. A malformed existing device-state file fails startup instead of silently generating a new device identity.

## Optional activity watcher and auto sign-in

The watcher turns the class activity feed and optional course-group sign-in cards into alerts, and can automatically submit normal, location or operator-coded password/gesture sign-ins. Add a private `watch` object; set `group_activities` only when course-group cards should also be discovered:

```json
{
  "group_activities": true,
  "watch": {
    "enabled": true,
    "auto_sign": true,
    "auto_answer": false,
    "classroom_agent": {
      "enabled": true,
      "notify_only": true,
      "min_confidence": 0.82,
      "max_questions": 20
    },
    "interval_seconds": 60,
    "interval_jitter_percent": 10,
    "class_poll_delay_min_ms": 750,
    "class_poll_delay_max_ms": 3000,
    "auto_sign_delay_min_seconds": 5,
    "auto_sign_delay_max_seconds": 25,
    "repeat_seconds": 120,
    "max_repeats": 5,
    "poll_before_minutes": 20,
    "poll_after_minutes": 20,
    "group_scan_seconds": 300,
    "term_start": "2026-09-13",
    "window": "07:30-22:30",
    "classes": [
      {
        "course_id": "synthetic-course",
        "class_id": "synthetic-class",
        "schedule": [
          {
            "key": "synthetic-mon-regular",
            "days": ["Mon"],
            "start": "08:00",
            "end": "09:40",
            "weeks": "1-16",
            "room": "Synthetic room A"
          },
          {
            "key": "synthetic-wed-guidance",
            "days": ["Wed"],
            "start": "08:00",
            "end": "09:40",
            "weeks": "7,13",
            "room": "Synthetic room B"
          }
        ],
        "location": {
          "latitude": 31.234567,
          "longitude": 121.456789,
          "address": "Synthetic classroom"
        },
        "sign_code": {"code": "1234"}
      }
    ],
    "capture": {
      "enabled": true,
      "retention_hours": 168,
      "activity_pages": true
    },
    "feishu_webhook": "https://example.com/feishu-webhook",
    "ntfy_url": "https://ntfy.example.com",
    "ntfy_topic": "synthetic-topic",
    "gotify_url": "https://gotify.example.com",
    "gotify_token": "synthetic-application-token"
  }
}
```

Bounds and behavior:

- `group_activities` defaults to false. When true, `list_activities` and the watcher also read Easemob course-group history, parse `attachmentType=15` course cards, and merge them with the class feed by `course_id:class_id:activity_id`. The group list is cached for 10 minutes and at most 20 groups are scanned. The watcher treats the class feed as primary and scans course groups at most once per `group_scan_seconds` (default 300, range 60–1800) while a watched class is active. Group failures produce partial coverage or a safe watcher log; they do not erase successful class-feed rows.
- `auto_answer` defaults to false. When true, an active `kind:"answer"` activity is claimed once through `answer_activity`; failed attempts do not resubmit and only repeat the alert.
- `classroom_agent` optionally inserts the bounded classroom decision process in front of that claim. `enabled` turns it on, `notify_only` (recommended for production) degrades every decision to an operator alert, `min_confidence` defaults to 0.82, and `max_questions` defaults to 20 (capped at 100). The agent only ever considers `kind:"answer"` classroom activities: `homework` and `practice` are scope-gated and can never be auto-submitted. With no classroom question source configured it stays in claim-only mode; once question parsing lands, answers must pass normalization and the confidence gate before any action. Low confidence, missing providers, oversized question sets and parse failures all become a review notification instead of an upstream write.
- `auto_sign` defaults to false. Normal/location cards, and password/gesture cards with an operator-provided `sign_code.code`, enter the pre-sign state check. List `userStatus` never proves attendance. The signer checks `preSign`, submits only when needed, then independently verifies attendance. `verified=true, submitted=false, method=none` is an already-completed sign-in, not an automatic submission. A configured solver may handle one slider challenge; otherwise the activity requires the manual captcha flow. QR/photo/face remain alert-only in the watcher and require the operator-supplied payload or object IDs in the explicit tools.
- `interval_seconds` is at least 5 (default 60), with `interval_jitter_percent` randomized around it (default 10, maximum 25). The interval is a start-to-start target while a watched class window is active; elapsed work is deducted from the next sleep. Ticks never overlap or catch up in bursts: slow upstream requests stretch the interval and leave at least one second before the next tick. Idle periods still sleep up to five minutes. Each poll uses the account-bounded query queue.
- During an explicit course schedule (including its before/after padding), both activities and ordinary assignments for that exact course/class are scanned each tick. New pending assignments and actionable classroom activities (sign-ins, rapid answers, practice, homework and other active unsigned activities) use priority 9. Completed items remain silent; existing sign-in/answer decisions, repeat limits and notification retries are unchanged. This does not enable automatic coursework answers or submission. A watch without explicit schedules keeps the ordinary notification policy.
- Outside a course's active schedule, ordinary assignments retain a minimum five-minute per-class scan interval and at most one idle class is scanned per tick. The first complete scan establishes a silent historical baseline. Subsequent new pending assignments use priority 6 outside class, and are suppressed only after notification acceptance; failed or partial scans do not replace the baseline. Exact course/class matching prevents another course from inheriting classroom urgency.
- `class_poll_delay_min_ms` / `class_poll_delay_max_ms` default to 750–3000 and add a randomized pause between two class polls. `auto_sign_delay_min_seconds` / `auto_sign_delay_max_seconds` default to 5–25 and delay the automatic sign attempt after discovery.
- `repeat_seconds` defaults to 120 and must be at least 30; `max_repeats` defaults to 5 and is capped at 20. Manual reminders repeat only while the activity is still `signable`.
- `poll_before_minutes` / `poll_after_minutes` default to 20 and are capped at 30. Scheduling is polled from `start - poll_before_minutes` through `end + poll_after_minutes`, in **Asia/Shanghai**. `lead_seconds` remains a compatibility fallback for older deployments; it is ignored when the new fields are present.
- `term_start` is the first day of teaching week 1 in `YYYY-MM-DD` (also Asia/Shanghai). It is required when any schedule has `weeks`.
- `window` is a global `HH:MM-HH:MM` bound in China Standard Time. Per-class `schedule` further limits polling to listed days/times; a class without `schedule` follows the global window. Midnight-crossing ranges are supported.
- `classes` requires exact `course_id`/`class_id` pairs from `list_courses`; at most eight classes are watched. Days accept `0`..`7` or `Sun`..`Sat` (both 0 and 7 mean Sunday).
- Each schedule entry is an independent occurrence. `key` is the stable identity used by `location_bindings`; explicit keys are preferred whenever a regular class and a guidance class can share the same weekday/time. If omitted, the key is generated from sorted days, start/end and `weeks`, for example `Tue-08:00-09:40-W7.13`. `weeks` accepts `1-6`, `7,13`, `13-16`; empty means every teaching week. `room` is display metadata only. Cross-midnight schedules such as `23:50-00:30` are supported.
- Bind a schedule-specific location with `chaoxing location bind --course ... --class ... --schedule <key> --preset <id>`. A class-wide binding (`--schedule ''`) remains the fallback for any schedule without its own binding.
- A class `location` is the preset latitude/longitude/address for location sign-in. Coordinates are validated against the activity's published range. Before submission, the service applies the reference client's ±0.00005-degree random offset; after `errorLocation`, it retries once with the tightened ±0.00001 offset.
- Configure any combination of `feishu_webhook`, `ntfy_url` + `ntfy_topic`, and `gotify_url` + `gotify_token`. ntfy and Gotify require both fields; a partially configured channel is a startup error instead of being silently dropped. With no complete channel, serve also refuses to start. The watcher supplies urgency to ntfy/Gotify; Feishu uses a text card. Phone sound and delivery remain client-dependent; classroom priority 9 is not a priority-10 process-emergency alarm.
- Watch state persists in `<state_dir>/watch-state.json` (0600), including attendance proof and whether this attempt submitted a request. Verified outcomes suppress repeat attempts across restart. Legacy success entries without attendance proof are rechecked through the pre-sign flow. Transient transport/auth/capacity failures receive at most four attempts with bounded backoff; upstream cooldown is honored. Terminal failures require manual handling. Entries absent from the active feed for six hours are pruned, and every entry has a hard 24-hour ceiling. Corrupt or unsupported state is quarantined and degrades readiness.
- Every sign-in attempt, including MCP/CLI manual calls, writes one private row to `sign_audits` in `<state_dir>/chaoxing.db`: account, course/class/activity, method, state/code, verified flag, submitted address and jittered coordinates, timestamp, and a best-effort public egress IP/source. The egress probe uses a neutral HTTPS trace endpoint through the same sticky egress path; probe failure leaves the IP empty and never blocks the sign-in. The Web UI exposes the newest rows for the current account at `GET /api/sign-audits?limit=20`.
- `capture.enabled` stores sanitized activity observations in `<state_dir>/chaoxing.db`. `retention_hours` defaults to 168 (one week). `activity_pages` fetches a sanitized practice H5 page once through `get_activity_page`. Hidden values, cookies, credentials and raw HTML are never stored.
- The WebUI capture API is account-scoped from the authenticated session. List responses default to 20 rows (maximum 100), do not read `page_json` from SQLite, and expose only `has_page`; page evidence requires an exact `activity_key` and is fetched one row at a time.
- A tenant can contain its own `group_activities` and `watch` object. Its watcher uses that tenant's upstream client, private state directory, classes, sign codes and notification destinations. The default account remains controlled by the top-level `watch` object.
- Outside the watch window, or when no watched schedule is active, the loop sleeps up to five minutes instead of using `interval_seconds`. It wakes at the next window/class boundary and still honors the configured before/after padding.
- Each tick is panic-supervised. A panic is recovered, counted, logged and sent as a priority-9 notification; the next successful cycle resets the consecutive counter.
- Each active class poll logs `class_poll` with its start/end timestamps, duration, status, item count and up to eight compact sign-in observations in the form `activity_id:user_state:raw_user_status:signable`. Automatic sign-in adds `sign_candidate`, `auto sign start`, `auto sign cancelled` and `auto sign result` lines. These records contain IDs and normalized states only, never URLs, cookies, sign codes, coordinates or notification credentials.
- Private `protocol-evidence.jsonl` retains bounded request/response projections and correlated sign outcomes independently of the system log ring. It uses 0600 files, 2 MiB segments and three predecessors; old segments are removed after seven days. Raw bodies and secrets are excluded. `/readyz` reports default-account evidence-write failure counters and degrades after three consecutive failures. Details and redaction rules are in [Architecture](architecture.md#optional-activity-watcher).
- Upstream throttling returns `UPSTREAM_RATE_LIMITED` and `retry_after_seconds`. The shared account/endpoint cooldown backs off from five minutes to one hour and honors a longer valid upstream `Retry-After` (up to 24 hours). It persists in private `upstream-cooldowns.json`; neither idle time nor restart resets history. An accepted endpoint response resets it. Polls during cooldown do not hit upstream, and a short configured interval cannot override it. Invalid state fails startup. Successful class lists share a 30-second cache across watcher and interactive reads, with original fetch time in coverage details; failures and cooldowns never become cached success.
- Process exits are supervised across procd restarts. Unexpected respawns increment `process.consecutive_crashes`, asynchronously send priority-9 alerts (priority-10 from the third consecutive crash) with three bounded retries, and a SIGTERM shutdown resets the consecutive chain.
- `/healthz` is a minimal liveness endpoint (`ok`, `version`). `/readyz` returns 503 when a watcher is stale, a process is degraded, consecutive panics reach three, or a watcher had to quarantine corrupt state (`state_degraded:true`). The degraded flag is fail-closed for the life of the process; inspect the `.corrupt-*` file and restart after preserving it. Detailed process and per-account watcher counters remain on the authenticated Web UI status and the loopback management listener.
- Embedded WebUI assets use content-hash ETags, immutable cache policy and gzip when the client accepts it.
- Private coordinate presets are managed with `chaoxing location add|bind|resolve` on the deployment host. Values are stored per account in the private SQLite store and are never exposed through MCP or committed to git.
- `chaoxing watch windows --days 7` prints the exact active intervals, per-schedule key, week restriction and room the watcher will use, which is the quickest way to compare configuration with the official timetable.
- The watcher class list is editable with `chaoxing watch classes list|add|remove`; only `watch.classes` is rewritten, a timestamped backup is kept and a daemon restart is required.
- `chaoxing courses discover` reads the mooc2 course-list page and marks which courses the legacy feed also returns, which explains a timetable course that is missing from `list_courses`.
- `/readyz` reports `stale`: a live watcher that has not completed a cycle for fifteen minutes (three times the five-minute idle sleep cap) is flagged so a wedged loop is distinguishable from an idle deployment.

- Course-group history is read only while at least one watched class is active; idle deployments do not scan it every minute.

The watcher logs class failures by safe fault code, continues with the other classes, and never exposes raw upstream bodies. Notification text contains course/activity identity and result; it is operator data and must not be treated as instructions.

## Optional location presets and Baidu map

Set a private top-level `location` object to enable location presets in the management UI and watcher:

```json
{
  "location": {
    "enabled": true,
    "baidu_ak_file": "/etc/chaoxing/baidu.key",
    "center_latitude": 39.9042,
    "center_longitude": 116.4074,
    "zoom": 17,
    "timeout_seconds": 5
  }
}
```

- `location.enabled` turns on schema v4, the private SQLite tables and the `/api/locations` + `/api/location-bindings` management endpoints. Location storage works without a map key, so manual coordinate entry remains available.
- Coordinates are stored as BD-09. Manual input may specify `coordinate_system` as `bd09`, `gcj02` or `wgs84`; the service converts to BD-09 before storage.
- `baidu_ak`, `baidu_ak_file` or `baidu_ak_env` supply the browser map key. Use one source only. The key stays private and is never committed. A browser map inherently receives its AK through `/api/map/config`; restrict the key to the deployment referer in the Baidu console. Geocoding/reverse geocoding are proxied by the daemon so those requests do not expose the key in URLs.
- `center_latitude`, `center_longitude` and `zoom` control the initial map view. The zoom default is 17.
- `timeout_seconds` bounds Baidu geocoding requests; the default is 5 seconds.
- The management page loads the Baidu GL script only when the map is enabled and the key is present. The script runs inside a sandboxed `/map` iframe (`allow-scripts`, no `allow-same-origin`) and receives only the AK, center and selected point over `postMessage`; the main application CSP contains no Baidu origin.
- Location presets and course bindings live in `<state_dir>/chaoxing.db`. They are never written to the repository or notification bodies.

Example binding:

```json
{
  "course_id": "synthetic-course",
  "class_id": "synthetic-class",
  "preset_id": 1,
  "schedule_key": ""
}
```

`schedule_key` is optional. An empty key binds the class-wide fallback. A schedule-specific binding wins while that watch schedule is active. The exact key is `Schedule.EffectiveKey()`: an explicit stable `key` when configured, otherwise `days,start,end` plus the normalized `-W<weeks>` suffix when `weeks` is set. For example, a week-limited schedule may resolve as `Tue-08:00-09:40-W7.13`; multi-day keys remain comma-joined (`Tue,Thu-08:00-09:40`). The watcher tries the exact key, then the pre-week legacy alias (`Tue-08:00-09:40`), then the class-wide fallback, then the existing sign layer applies activity-range validation, normal ±0.00005° jitter and the single ±0.00001° tightened retry after `errorLocation`. When a regular and a guidance class share a weekday/time, always assign distinct explicit `key` values such as `arch-tue-regular` and `arch-tue-guidance`; do not rely on the fallback.

## Optional slider-captcha solver

Set a private top-level `captcha_solver` object to let `sign_activity` resolve slider challenges automatically. `provider:"local"` uses the built-in no-network image-pair detector:

```json
{
  "captcha_solver": {"provider": "local"}
}
```

For the external DamaGou provider, the provider runs outside the Go binary, so the daemon stays small enough for embedded hosts:

```json
{
  "captcha_solver": {
    "provider": "damagou",
    "user_key_file": "/etc/chaoxing/damagou.key",
    "timeout_seconds": 60
  }
}
```

`provider` accepts `local` or `damagou`. DamaGou requires exactly one of `user_key`, `user_key_file`, or `user_key_env`; `user_key_file` is recommended so the secret stays outside the main JSON. Use an absolute path to a root-only file containing only the key. `timeout_seconds` defaults to 60. A solved challenge is submitted through the same `answer_sign_captcha` protocol and followed by exactly one sign retry. If the provider fails, the safe fault code is `CAPTCHA_SOLVER_FAILED` and the manual `get_sign_captcha` -> `answer_sign_captcha` -> `sign_activity` flow remains available. A tenant can contain its own `captcha_solver` object.

## Answer providers, uploads and face enrollment

Enable the answer chain with a private top-level `answer` object (tenants may define their own). Providers run in configured order; successful high-confidence answers are cached in `<state_dir>/answer-cache.json`:

```json
{
  "answer": {
    "enabled": true,
    "min_confidence": 0.8,
    "providers": [
      {
        "type": "tiku",
        "base_url": "https://tiku.example.com/query",
        "api_key_file": "/etc/chaoxing/tiku.key",
        "timeout_seconds": 30
      },
      {
        "type": "llm",
        "base_url": "https://api.example.com/v1/chat/completions",
        "api_key_env": "CHAOXING_LLM_KEY",
        "model": "gpt-4o-mini",
        "timeout_seconds": 45
      },
      {
        "type": "responses",
        "base_url": "https://api.openai.com/v1",
        "api_key_file": "/etc/chaoxing/openai.key",
        "model": "gpt-5.2",
        "timeout_seconds": 45
      },
      {
        "type": "newapi",
        "base_url": "https://newapi.example.com",
        "api_key_file": "/etc/chaoxing/newapi.key",
        "model": "deepseek-v4.1-flash",
        "timeout_seconds": 45
      }
    ]
  }
}
```

`type:"llm"` accepts any OpenAI-compatible non-streaming chat-completions endpoint. `type:"responses"` uses the official `openai/openai-go` SDK and performs one stateless Responses API call per question; `base_url` is the API root (for example `https://api.openai.com/v1` or a compatible gateway root), never a `/chat/completions` endpoint. It requires `model` and exactly one API-key source, disables server-side response storage, disables SDK retries, and uses `timeout_seconds` (default 45) as both the HTTP and per-request deadline. `type:"newapi"` targets NewAPI/one-api style gateways: `base_url` must be the API root (for example `https://newapi.example.com`, optionally with a trailing `/v1`), and the provider derives `/v1/chat/completions` itself so accidentally configured full endpoints are rejected. It requires `model` and exactly one API-key source, sends `max_tokens:768` because reasoning-first models spend tokens before the final JSON, and defaults to a 45-second timeout. `type:"tiku"` posts `{question,type,options}` and accepts either TikuAdapter-style `answer.bestAnswer` or `{data:{answer,confidence}}`. API keys may be supplied through at most one of `api_key`, `api_key_file`, or `api_key_env`; file/env forms are preferred. Keys are never returned by tools.

For a global assignment, call `get_assignment_questions` first; it follows the signed task entry to the editable work page and returns the `job_id` accepted by `answer_work`. For knowledge-point work, call `list_work_jobs` and `get_work_questions` instead. `answer_work` only generates and saves local drafts, and requires both `chaoxing.answer` and `chaoxing.work`. `save_work` and `submit_work` remain explicit tools and require the `chaoxing.work.submit` scope; final submission additionally requires every question to have a draft.

Stage uploads directly under `<state_dir>/uploads/`. The directory must be a real directory with mode 0700 and owned by the service user; group/world-accessible files are rejected, and the staged file is removed after a successful pan upload. `upload_file` accepts only a filename, rejects path separators, and limits content to 5 MiB. It returns the pan object ID needed by photo sign-in or face enrollment. Face enrollment requires `chaoxing.face`; face sign-in requires both `chaoxing.face` and `chaoxing.sign`.
## Local draft state

Draft support needs no extra provider configuration. The first draft operation creates `<state_dir>/work-drafts.json` with mode 0600. Manual and generated answers share this validated, atomically persisted store. Export remains local; only the explicit `save_work` and `submit_work` tools post upstream.
