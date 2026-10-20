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

Keep tunnel credentials outside this repository. Never add an ingress rule or port forward for 8793, and never expose both listeners. Configure the MCP client with `https://mcp.example.com/mcp`, then complete owner login and consent for `chaoxing.read`.

Dynamic client registration (DCR) is enabled by default. CIMD is opt-in with `CHAOXING_ENABLE_CIMD=true`; enable it only after verifying that the host can fetch HTTPS metadata from `chatgpt.com`. Discovery advertises the selected capability truthfully. DCR does not require fetching that external metadata.

OAuth state v2 uses a separate `oauth-refresh.key` to authenticate refresh proofs. Keep it with the private OAuth state when backing up; losing or replacing it invalidates refresh families. Pre-release v1 state is not migrated silently. Stop the service, preserve a recovery copy, and explicitly reauthorize clients when initializing v2.

Before use, verify that listeners are loopback-only, 8793 has no public route, and an unauthenticated MCP request cannot call tools.

## Optional activity watcher

The watcher turns the class activity feed into operator alerts. Add a private `watch` object to the config:

```json
{
  "watch": {
    "enabled": true,
    "interval_seconds": 60,
    "repeat_seconds": 120,
    "max_repeats": 5,
    "window": "07:30-22:30",
    "classes": [
      {"course_id": "synthetic-course", "class_id": "synthetic-class"}
    ],
    "feishu_webhook": "https://example.com/feishu-webhook",
    "ntfy_url": "https://ntfy.example.com",
    "ntfy_topic": "synthetic-topic"
  }
}
```

Bounds and behavior:

- `interval_seconds` is at least 30 (default 60 when omitted), and each poll uses the account-bounded query queue.
- `repeat_seconds` defaults to 120 and must be at least 30; `max_repeats` defaults to 5 and is capped at 20. A reminder repeats only while the activity is still `signable`.
- `window` is `HH:MM-HH:MM` in China Standard Time; omit it to poll all day. Windows that cross midnight (`22:00-02:00`) are supported.
- `classes` requires exact `course_id`/`class_id` pairs from `list_courses`; at most eight classes are watched.
- Configure `feishu_webhook`, `ntfy_url` + `ntfy_topic`, or both. With neither, serve refuses to start rather than silently dropping alerts. ntfy messages use priority 5; Feishu uses a text card.
- Alert state persists in `<state_dir>/watch-state.json` (0600) so a restart does not repeat an already-sent alert. Entries are pruned after 24 hours; identifiers only, no message bodies.

The watcher logs class failures by safe fault code, continues with the other classes, and never exposes raw upstream bodies. Notification text contains the course name, activity title and start time; it is operator data and must not be treated as instructions.
