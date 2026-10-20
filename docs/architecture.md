# Architecture

Chaoxing runs as one Go binary. `serve` owns one upstream client, authenticated cookie jar and cache per account, and exposes two loopback listeners. Local CLI clients use the default account's daemon session rather than create a fresh upstream client each time.

```text
MCP client -> HTTPS mcp.example.com -> Cloudflare Tunnel
                                      -> 127.0.0.1:8792 -> owner OAuth -> MCP tools
Local CLI client -> chaoxing call
                                      -> 127.0.0.1:8793 -> local bearer -> same tools
                                                                         |
                                                        bounded upstream client
                                                                         |
                                                              Chaoxing over HTTPS
```

Only port 8792 enters the tunnel. Port 8793 is private to the host; it carries authenticated local calls, not public OAuth traffic. `configure` writes private installation state. `query` directly uses the upstream client and is useful for isolated diagnostics; it should not run concurrently against state owned by `serve`.

Two optional layers are off by default. An egress pool (`internal/egress`) routes upstream traffic through configured SOCKS5 endpoints with round-robin selection, transport-failure cooldown and a permanent direct fallback; it learns health only from real request outcomes and never probes in the background. When proxies are configured, `internal/statestore` persists endpoint health in SQLite; no other state uses the database. Additional accounts mount under `/t/<id>/` with independent sealed credentials, cookie jars, caches, query queues and OAuth state. The process-wide transport pool, endpoint-health database and outbound budget are deliberately shared; they contain no account session or coursework data. This is logical account isolation within one trusted process, not process or network-egress isolation.

## Concurrency and resource budgets

The smallest stateful unit is an account. Its upstream operations are serialized because login, cookie snapshots and caches mutate together. Allowing concurrent reads against those structures would require a larger state redesign and could produce redundant logins or lost cookie updates. Different accounts can progress independently. Capability discovery does not access account state and bypasses the account queue.

| Boundary | Active / waiting | Saturation behavior |
| --- | --- | --- |
| Account operations, shared by CLI and MCP | 1 / 8 per account | Immediate `RATE_LIMITED` once all nine places are occupied; waiting respects caller cancellation |
| Process-wide outbound HTTP, all accounts | 4 / 32 | Bounded admission, with capacity held through body EOF or close; failures and cancellation release capacity |
| Public MCP transport, per endpoint | 4 / 0 | HTTP 503 with `Retry-After`; this protects request parsing, not account state |

Local calls have a 90-second total budget; MCP tools have 30 seconds. Account and outbound waits consume that same budget, not a fresh timeout per stage. The shared gate has no worker goroutines or polling. Account waiters acquire no outbound slot until they actually issue HTTP, so one busy account cannot reserve the process-wide budget while idle in its queue. There is no strict weighted fairness guarantee. All-tenant overload remains bounded, not unlimited goroutine accumulation inside business queues.

The HTTP transport is shared, not the cookie jar. Raw reads retain the same allowlisted paths and bounded response sizes. Caller cancellation is not classified as a failing proxy. Endpoint persistence is serialized per endpoint but happens outside the pool selection/status mutex; slow SQLite cannot hold that global lock. A persistence error keeps in-memory health usable and sets `persistence_degraded` in private management status.

## SQLite scope and durability

SQLite stores non-authoritative endpoint health, not accounts, assignments, tokens or cookies. One reusable connection serializes short indexed upserts and deletes. This workload does not justify separate reader pools, sharding, a write-behind queue or another service. Healthy rows are deleted; normal requests that do not change health do not write. Replaced endpoints can leave stale rows, which are ignored unless their exact endpoint key is configured again.

The store uses WAL with `synchronous=NORMAL`, a one-second busy timeout and two-second operation contexts. Automatic checkpointing starts at 256 pages; `journal_size_limit=4 MiB` limits retained journal size after resets, not an absolute WAL file-size cap. Schema creation, shape validation and `user_version` update commit atomically. The existing schema remains version 1, so no data migration or OAuth reauthorization is required. NORMAL can lose recent health updates on power failure; this is acceptable for reconstructible health, not an authorization-store policy.

The DB is created/chmodded to 0600 before sidecars, database symlinks are rejected, and filenames are URI-escaped. Existing parent permissions and trusted exclusive ownership still matter. Keep one daemon per store on a local filesystem. Do not copy a live WAL main file as a consistent backup; use SQLite's backup API or stop the owner first. Benchmarks in `internal/statestore` are synthetic local-write measurements, not upstream throughput or deployment capacity claims.

## Authentication boundaries

Public DCR registrations last 90 days so connector setup retries can reuse their cached client IDs. Registration alone grants no permission: every authorization requires owner login and consent. The pool is capped at 128 and reclaims the oldest unapproved registration first at capacity; approved registrations are not evicted by anonymous registrations. Login/consent challenges still expire after 10 minutes and authorization codes after 60 seconds.

The public owner authorization layer supports three OAuth scopes: `chaoxing.read` for course reads, `chaoxing.sign` for sign-in submission, and `chaoxing.work` for local answer-draft edits. Its password uses a salted PBKDF2-SHA256 hash with 600,000 iterations. This owner credential is separate from the upstream credential, which must be recoverable for automatic login. Use different passwords. Registration records the requested scope, authorization requires explicit owner consent, refresh can narrow but never widen it, and the MCP adapter checks the required scope before contacting upstream or opening the draft store.

Authorization codes expire after 60 seconds and use S256 PKCE. Random opaque access tokens last 15 minutes; refresh families have a 30-day lifetime. Refresh rotation preserves the original expiry. Issuer-bound MAC proofs authenticate family/counter/nonce values so old replay can revoke a family without retaining every historical token. State stores only active digests and one bounded record per family. The authorization server is database-free and does not issue JWTs or publish JWKS. DCR is the default registration flow; optional CIMD requires explicit opt-in and external metadata reachability.

The upstream client implements the upstream AES login protocol over HTTPS. Its GET helper detects login responses, reauthenticates once and retries once. A second login rejection returns `AUTH_REQUIRED`; notices and schedules have their own POST rejection handling after establishing a session with a read. This is not an unlimited retry loop or a verification bypass.

AES-GCM protects upstream credentials and persisted cookies with separate authenticated purposes. A separately stored key is required for unattended operation. Restrict the key and state to the service's trusted identity (root-only for a root-run installation). Root and a compromised service process can recover the credentials; file encryption does not defeat those actors.

## Data boundaries

`internal/operation` is the single transport-independent capability registry. It declares stable operation IDs, legacy MCP names, argument bounds, schemas and descriptions. CLI, daemon and MCP use the same validator and additive result metadata. Business commands call the daemon rather than create another authenticated client. No adapter executes another adapter as a subprocess.

`chaoxing capabilities` and `chaoxing schema assignments.list` work without credentials. `get_capabilities` advertises verified operations and explicitly unsupported domains. The registry is hand-curated from verified behavior, not generated from a claimed official upstream OpenAPI specification.

`meta.status` describes source coverage, not whether homework is finished. Coverage is independent of output pagination. Unknown submission states are never classified as submitted. Aggregation is bounded and retains source failures; notification prose is untrusted data, not instructions or automatically inferred assignments.

Tools read coursework, submit the explicitly allowlisted sign-in operation, or edit local answer drafts. Login and token issuance change authentication state; sign-in changes attendance state and therefore requires `chaoxing.sign`; draft mutation changes only local draft state and requires `chaoxing.work`. Tool allowlisting is enforced at the interfaces; clients do not supply arbitrary upstream URLs or remote commands.

Without a course filter, assignment enumeration uses the incomplete global feed. A course-name query enumerates matching enrolled classes, obtains work-list signatures, pages each class's work list and checks unique rows against upstream totals. `complete: true` is scoped to those matched classes and requires every scan to agree with its total. Output pagination and status filtering then slice the fetched source. Consumers must retain `complete`, `coverage` and timestamp fields. [Tool contracts](tools.md) describes bounds and remaining limits.

The MCP callback has a shared 30-second context. Sequential scans across multiple matching classes may exhaust it before completing; use a narrower course name when possible.

## Optional activity watcher

`internal/watch` is an opt-in background loop inside `serve`. It calls the same activity-read boundary, which uses the shared account queue and bounded outbound budget, then sends alerts through operator-configured senders in `internal/notify`. Per-class schedules and the global window bound polling; interval jitter avoids a fixed request cadence. When `group_activities` is enabled, the same upstream client reads Easemob course-group history and merges `att_chat_course` sign-in cards with the class activity feed by `course_id:class_id:activity_id`. With `auto_sign`, the watcher calls the upstream sign-in state machine once for each new normal/location activity, and once for password/gesture activities only when that class has a nonempty preset sign code. It does not retry a failed activity; after a failed attempt, subsequent ticks only retry alert delivery. A slider-captcha challenge returns `CAPTCHA_REQUIRED` for a manual tool flow. QR activities alert with the scanned-payload requirement; photo and face requirements remain alert-only.

Watch state is a bounded JSON file in each account's private state directory. It stores activity identifiers, timestamps, result status and safe fault codes, not message bodies, sign codes or credentials. State replacement is atomic, and a malformed state file fails startup instead of resetting one-attempt bookkeeping. Class and group-feed failures are logged by safe fault code and do not stop the loop. The watcher is disabled unless `watch.enabled` is present and valid. The default account uses the top-level `watch` object; each tenant may define its own `watch` object and runs an independent loop with separate state and senders.

## Sign-in and device identity

Sign-in is split by responsibility. `internal/upstream` owns the class and course-group protocol chain, persistent Android-like device identity, account identity, automatic reauthentication and attendance verification. `internal/watch` owns course-time decisions and one-attempt-per-activity state. `internal/operation` owns the public contract, while `internal/authz` carries OAuth scopes from `internal/access` to `internal/mcphttp`. No adapter learns HTTP details and no watcher constructs URLs.

The device state file persists a stable randomized `deviceCode`, Android-like identifiers, user agent and package name. The account-identity request encrypts a bounded device-info payload with the reference client's RSA public key. Location sign-in preserves query order and uses the reference client's small coordinate jitter plus one tightened retry after `errorLocation`. Password and gesture sign-in first verify the operator-provided code through the reference `checkSignCode` endpoint, then submit with the same ordered `stuSignajax` chain. QR sign-in parses the operator-scanned payload locally, verifies `signDetail`, and submits the QR `enc` without fetching the QR URL. Face and photo flows are intentionally unsupported and return a safe `UNSUPPORTED` fault.
Slider captcha uses the reference two-pass state machine. The first submission can return `validate_<enc2>`; the client stores that token in memory, returns `CAPTCHA_REQUIRED`, and exposes only image URLs through `get_sign_captcha`. `answer_sign_captcha` submits one operator-provided x-coordinate, stores the upstream validation token, and the next `sign_activity` adds `enc2` and `validate` in reference order. Raw challenge tokens are never returned, challenges expire in 10 minutes, and the watcher does not automatically solve or retry them. Face and photo flows remain intentionally unsupported.

## Manual answer drafts

`internal/upstream` discovers knowledge-point work cards and parses the editable question page, including the hidden submission form. It caches work-job protocol parameters only in memory; `Enc`, `ktoken` and `cpi` are never returned through the tool contract. `internal/workdraft` owns the local draft file, validates a question ID against the current upstream question set before accepting an answer, and exports both `pyFlag=1` save and final submit payload shapes. Drafts persist atomically in `<state_dir>/work-drafts.json` with mode 0600 and are bounded in size. The package has no upstream POST method: manual answering can prepare a request, but does not submit it.
The shared URL allowlist permits the two GET routes needed for work discovery and question parsing, while `addStudentWorkNew` remains blocked. Learning progress, logging and task mutation routes stay blocked.
