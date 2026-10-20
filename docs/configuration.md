# Configuration and tunnel setup

Build the single binary with the Go version in `go.mod`. `CHAOXING_CONFIG` selects the private config file; its default is `/etc/chaoxing/config.json`. `configure` refuses to replace an existing configuration implicitly.

## Initialize private state

```sh
chaoxing configure --state-dir /var/lib/chaoxing --key-file /etc/chaoxing/secret.key --issuer https://mcp.example.com
```

Supply a JSON object on stdin using a trusted private input mechanism. Required fields are `phone`, `password` (upstream login) and `owner_password` (a distinct owner OAuth credential, at least 16 characters). Do not type actual credentials into command arguments, shell history, issue reports, examples or tracked files. No working credentials are shipped in this repository.

The generated config records `state_dir`, `key_path`, `issuer`, `listen`, `local_listen`, `local_token` and `owner_password_hash`. Treat the whole config as private because it includes the local bearer token. The state directory holds sealed upstream credentials, sealed cookie state and owner OAuth state. Keep the encryption key separately from state backups; a backup containing both can be decrypted by whoever reads it.

On Linux, use a root-owned private state directory (0700), config and state files (0600), and a separate root-only key (0600). The code creates private files but does not establish your OS account ownership or fix permissions of an existing key. Verify them before starting. On Windows, apply equivalent restrictive ACLs; Unix mode bits alone do not provide that boundary.

Run `chaoxing serve` under the trusted service identity. Keep one daemon per state directory. The local identity running `chaoxing call` must have permission to read the private configuration; do not grant untrusted clients access to the vault key.

## Optional egress proxy pool

Upstream requests use direct egress by default. To route through SOCKS5 endpoints, add a private `proxies` array to the config (each entry has `url` (a socks5 URL with optional userinfo) and optional `bind` (a local source IP); at most 16). Selection is round-robin among healthy endpoints; transport failures cool an endpoint down with exponential backoff (30 s up to 5 min) and the direct path is the permanent fallback. There is no background probing. Endpoint health persists in `<state_dir>/chaoxing.db` (SQLite, WAL, mode 0600), which exists only when proxies are configured. Proxy URLs contain credentials: keep the config private and never log it; status output identifies endpoints by a truncated hash only. The local management listener exposes `GET /status` (same bearer token as `POST /call`) returning the version, sanitized egress health and tenant ids.

## Optional additional tenants

The default deployment serves one account at `/mcp`. To serve additional accounts, add a private `tenants` array to the config; each entry needs `id` (`[a-z0-9][a-z0-9-]{0,31}`, unique), `state_dir`, `key_path` and `owner_password_hash` (at most 32). Use sibling account-state directories, not nested ones. Startup resolves relative components and existing symlink ancestors, rejects overlapping default/tenant state directories, shared key paths or existing hard-linked keys, and keys located in another account's state. This intentionally rejects ambiguous layouts accepted by older releases; move state only while its owner is stopped and preserve a recovery copy.

A tenant mounts a full OAuth + MCP stack under `/t/<id>/` with issuer `<issuer>/t/<id>`, its own sealed credentials (initialize with `chaoxing configure` pointed at a tenant-scoped config, then merge the tenant fields) and OAuth state. Upstream clients are created lazily. The private CLI remains default-account-only; the local bearer is an operator identity, not a tenant selector. Cookie jars, caches, credentials and tokens are independent, while transport connections, endpoint health and outbound capacity are shared. See [resource and durability design](architecture.md#concurrency-and-resource-budgets).

Discovery supports both existing `/t/<id>/.well-known/...` routes and standard path-insertion routes: `/.well-known/oauth-authorization-server/t/<id>` and `/.well-known/oauth-protected-resource/t/<id>/mcp`. Publish these discovery paths through the same HTTPS origin, along with the tenant's OAuth and MCP paths. This does not make the private management listener public.

## Publish only the MCP listener

Default listeners are `127.0.0.1:8792` (public MCP and OAuth behind the tunnel) and `127.0.0.1:8793` (private CLI). Both stay bound to loopback. The issuer must match the public HTTPS origin.

Use this ingress fragment in your own private Cloudflare Tunnel configuration:

```yaml
ingress:
  - hostname: mcp.example.com
    service: http://127.0.0.1:8792
  - service: http_status:404
```

Keep tunnel credentials outside this repository. Never add an ingress rule or port forward for 8793, and never expose both listeners. Configure the MCP client with `https://mcp.example.com/mcp`, then complete owner login and consent. Read tools require `chaoxing.read`; remote sign-in additionally requires `chaoxing.sign`.

Dynamic client registration (DCR) is enabled by default. CIMD is opt-in with `CHAOXING_ENABLE_CIMD=true`; enable it only after verifying that the host can fetch HTTPS metadata from `chatgpt.com`. Discovery advertises the selected capability truthfully. DCR does not require fetching that external metadata.

OAuth state v2 uses a separate `oauth-refresh.key` to authenticate refresh proofs. Keep it with the private OAuth state when backing up; losing or replacing it invalidates refresh families. Pre-release v1 state is not migrated silently. Stop the service, preserve a recovery copy, and explicitly reauthorize clients when initializing v2.

Before use, verify that listeners are loopback-only, 8793 has no public route, and an unauthenticated MCP request cannot call tools.

## Persistent device identity

The first startup creates `<state_dir>/device-state.json` (0600) with a stable randomized device code, Android-like device identifiers, the configured user agent and package name. The file is not a credential, but keep it private and stable: changing it makes the account appear to move to a new device. A private top-level `device` object can pin these values:

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
    "interval_seconds": 60,
    "interval_jitter_percent": 10,
    "repeat_seconds": 120,
    "max_repeats": 5,
    "lead_seconds": 120,
    "window": "07:30-22:30",
    "classes": [
      {
        "course_id": "synthetic-course",
        "class_id": "synthetic-class",
        "schedule": [
          {"days": ["Mon", "Wed"], "start": "08:00", "end": "09:40"}
        ],
        "location": {
          "latitude": 31.234567,
          "longitude": 121.456789,
          "address": "Synthetic classroom"
        },
        "sign_code": {"code": "1234"}
      }
    ],
    "feishu_webhook": "https://example.com/feishu-webhook",
    "ntfy_url": "https://ntfy.example.com",
    "ntfy_topic": "synthetic-topic",
    "gotify_url": "https://gotify.example.com",
    "gotify_token": "synthetic-application-token"
  }
}
```

Bounds and behavior:

- `group_activities` defaults to false. When true, `list_activities` and the watcher also read Easemob course-group history, parse `attachmentType=15` course cards, and merge them with the class feed by `course_id:class_id:activity_id`. The group list is cached for 10 minutes, at most 20 groups are scanned, and parsed cards are cached for 15 seconds to avoid a fixed high-rate request pattern. Group failures produce partial coverage or a safe watcher log; they do not erase successful class-feed rows.
- `auto_sign` defaults to false. When true, normal and location activities are attempted once. Password and gesture activities are attempted only when the matching class has a nonempty `sign_code.code`. A slider-captcha response returns `CAPTCHA_REQUIRED` and sends one alert; use the MCP tools to complete the challenge manually. QR activities alert because they require the operator-scanned raw payload in `sign_activity`; photo and face activities remain alert-only.
- `interval_seconds` is at least 30 (default 60), with `interval_jitter_percent` randomized around it (default 10, maximum 25). Each poll uses the account-bounded query queue.
- `repeat_seconds` defaults to 120 and must be at least 30; `max_repeats` defaults to 5 and is capped at 20. Manual reminders repeat only while the activity is still `signable`.
- `lead_seconds` defaults to 120 and is capped at 600. A class schedule is polled from `start - lead_seconds` through `end`; set it to `-1` to poll only from the exact start.
- `window` is a global `HH:MM-HH:MM` bound in China Standard Time. Per-class `schedule` further limits polling to listed days/times; a class without `schedule` follows the global window. Midnight-crossing ranges are supported.
- `classes` requires exact `course_id`/`class_id` pairs from `list_courses`; at most eight classes are watched. Days accept `0`..`7` or `Sun`..`Sat` (both 0 and 7 mean Sunday).
- A class `location` is the preset latitude/longitude/address for location sign-in. Coordinates are validated against the activity's published range. Before submission, the service applies the reference client's ±0.00005-degree random offset; after `errorLocation`, it retries once with the tightened ±0.00001 offset.
- Configure any combination of `feishu_webhook`, `ntfy_url` + `ntfy_topic`, and `gotify_url` + `gotify_token`. ntfy and Gotify require both fields; a partially configured channel is a startup error instead of being silently dropped. With no complete channel, serve also refuses to start. ntfy and Gotify use priority 5; Feishu uses a text card.
- Watch state persists in `<state_dir>/watch-state.json` (0600). It records the last alert/attempt time, result and safe fault code so a restart neither repeats a successful sign-in nor blindly retries a failed one. A failed attempt remains eligible for repeated manual alerts, but not another sign submission. Entries are pruned after 24 hours; no message bodies are stored.
- A tenant can contain its own `group_activities` and `watch` object. Its watcher uses that tenant's upstream client, private state directory, classes, sign codes and notification destinations. The default account remains controlled by the top-level `watch` object.

The watcher logs class failures by safe fault code, continues with the other classes, and never exposes raw upstream bodies. Notification text contains course/activity identity and result; it is operator data and must not be treated as instructions.

## Manual answer drafts

Manual answer support needs no extra configuration. The first `upsert_work_draft`, `list_work_drafts`, `delete_work_draft` or `export_work_draft` call creates `<state_dir>/work-drafts.json` with mode 0600. Drafts are validated against the current work question set, persisted atomically, and exported as both a `pyFlag=1` save payload and a final submit payload. The daemon never posts either payload upstream.
