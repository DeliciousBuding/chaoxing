# Architecture

Chaoxing runs as one Go binary. `serve` owns a single upstream client, authenticated cookie jar and caches, and exposes two loopback listeners. Local CLI clients use the daemon so repeated commands share its session rather than create a fresh upstream client each time.

```text
MCP client -> HTTPS mcp.example.com -> Cloudflare Tunnel
                                      -> 127.0.0.1:8792 -> owner OAuth -> MCP tools
Local CLI client -> chaoxing call
                                      -> 127.0.0.1:8793 -> local bearer -> same tools
                                                                         |
                                                        read-only upstream client
                                                                         |
                                                              Chaoxing over HTTPS
```

Only port 8792 enters the tunnel. Port 8793 is private to the host; it carries authenticated local calls, not public OAuth traffic. `configure` writes private installation state. `query` directly uses the upstream client and is useful for isolated diagnostics; it should not run concurrently against state owned by `serve`.

## Authentication boundaries

The public owner authorization layer grants the `chaoxing.read` OAuth scope. Its password uses a salted PBKDF2-SHA256 hash with 600,000 iterations. This owner credential is separate from the upstream credential, which must be recoverable for automatic login. Use different passwords.

Authorization codes expire after 60 seconds and use S256 PKCE. Random opaque access tokens last 15 minutes; refresh families have a 30-day lifetime. Refresh rotation preserves the original expiry. Issuer-bound MAC proofs authenticate family/counter/nonce values so old replay can revoke a family without retaining every historical token. State stores only active digests and one bounded record per family. The authorization server is database-free and does not issue JWTs or publish JWKS. DCR is the default registration flow; optional CIMD requires explicit opt-in and external metadata reachability.

The upstream client implements the upstream AES login protocol over HTTPS. Its GET helper detects login responses, reauthenticates once and retries once. A second login rejection returns `AUTH_REQUIRED`; notices and schedules have their own POST rejection handling after establishing a session with a read. This is not an unlimited retry loop or a verification bypass.

AES-GCM protects upstream credentials and persisted cookies with separate authenticated purposes. A separately stored key is required for unattended operation. Restrict the key and state to the service's trusted identity (root-only for a root-run installation). Root and a compromised service process can recover the credentials; file encryption does not defeat those actors.

## Data boundaries

Tools only read coursework. Login, token issuance and local persistence change authentication state, not coursework. Tool allowlisting is enforced at the interfaces; clients do not supply arbitrary upstream URLs or remote commands.

Without a course filter, assignment enumeration uses the incomplete global feed. A course-name query enumerates matching enrolled classes, obtains work-list signatures, pages each class's work list and checks unique rows against upstream totals. `complete: true` is scoped to those matched classes and requires every scan to agree with its total. Output pagination and status filtering then slice the fetched source. Consumers must retain `complete`, `coverage` and timestamp fields. [Tool contracts](tools.md) describes bounds and remaining limits.

The MCP callback has a shared 30-second context. Sequential scans across multiple matching classes may exhaust it before completing; use a narrower course name when possible.
